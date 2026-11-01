// Package ghrelease implements store.Store using GitHub Releases.
package ghrelease

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
)

// logicalToWire converts a logical asset name to the wire form used in
// GitHub release assets. The only transformation is that the "runs/" prefix
// is mapped to "runs-" so the slash does not appear in the asset name.
func logicalToWire(name string) string {
	if strings.HasPrefix(name, "runs/") {
		return "runs-" + name[len("runs/"):]
	}
	return name
}

// wireToLogical reverses logicalToWire.
func wireToLogical(name string) string {
	if strings.HasPrefix(name, "runs-") {
		rest := name[len("runs-"):]
		// Check the suffix before trimming; a name shorter than ".pprof"
		// would otherwise panic the slice.
		if strings.HasSuffix(rest, ".pprof") && store.ValidRunID(strings.TrimSuffix(rest, ".pprof")) {
			return "runs/" + rest
		}
	}
	return name
}

// client holds the HTTP client and credentials for the GitHub API.
type client struct {
	baseURL string
	token   string
	http    *http.Client
}

// newClient returns a client pointing at baseURL with token.
func newClient(baseURL, token string, hc *http.Client) *client {
	return &client{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: hc}
}

// Store implements store.Store using GitHub Releases.
type Store struct {
	key    store.Key
	owner  string
	repo   string
	client *client
}

// Config holds the configuration for a GitHub Releases Store.
type Config struct {
	// Key is the profile key; Key.Repo must be "owner/name".
	Key store.Key
	// Token is the GitHub token used for all requests.
	Token string
	// BaseURL is the GitHub API base URL (defaults to https://api.github.com).
	BaseURL string
	// HTTPClient is the client used for requests (defaults to http.DefaultClient).
	HTTPClient *http.Client
}

// New returns a Store from cfg. It errors on an empty Token or Repo.
func New(cfg Config) (*Store, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("the GitHub Releases store needs a token")
	}
	if cfg.Key.Repo == "" {
		return nil, fmt.Errorf("the GitHub Releases store needs a repo (owner/name)")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.github.com"
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	parts := splitRepo(cfg.Key.Repo)
	return &Store{
		key:    cfg.Key,
		owner:  parts[0],
		repo:   parts[1],
		client: newClient(cfg.BaseURL, cfg.Token, hc),
	}, nil
}

// NewWithHTTPClient returns a Store using the given http.Client. Used in tests.
func NewWithHTTPClient(key store.Key, token, baseURL string, hc *http.Client) *Store {
	parts := splitRepo(key.Repo)
	return &Store{
		key:    key,
		owner:  parts[0],
		repo:   parts[1],
		client: newClient(baseURL, token, hc),
	}
}

func splitRepo(repo string) [2]string {
	if i := strings.Index(repo, "/"); i >= 0 {
		return [2]string{repo[:i], repo[i+1:]}
	}
	return [2]string{repo, ""}
}

// RepoInfo describes the minimal fields returned by the GitHub Repos API.
type RepoInfo struct {
	DefaultBranch string
	Visibility    string // "public" or "private"
}

// Repo returns repository metadata for the configured repo.
func (s *Store) Repo(ctx context.Context) (*RepoInfo, error) {
	return s.client.getRepoInfo(ctx, s.owner, s.repo)
}

// ListDays returns all days for s.key, newest date first.
func (s *Store) ListDays(ctx context.Context, key store.Key) ([]store.Day, error) {
	return s.client.listDays(ctx, key, s.owner, s.repo)
}

// ReadAsset returns the content of the named asset in day.
func (s *Store) ReadAsset(ctx context.Context, day store.Day, name string) (io.ReadCloser, error) {
	a, ok := day.Assets[name]
	if !ok {
		// Fallback: fetch the release's current assets in case the asset was
		// uploaded after the Day value was read, but the Assets map wasn't refreshed.
		rel, err := s.client.getRelease(ctx, s.owner, s.repo, day.ID)
		if err != nil {
			return nil, &assetErr{name: name, err: store.ErrNotFound}
		}
		// Look for the asset in the release's current assets.
		for _, asset := range rel.Assets {
			if wireToLogical(asset.Name) == name {
				return s.client.readAsset(ctx, s.owner, s.repo, fmt.Sprintf("%d", asset.ID))
			}
		}
		return nil, &assetErr{name: name, err: store.ErrNotFound}
	}
	return s.client.readAsset(ctx, s.owner, s.repo, a.ID)
}

// BeginDay returns the day for key/date/seq, creating a draft release if needed.
func (s *Store) BeginDay(ctx context.Context, key store.Key, date store.Date, seq int) (store.Day, error) {
	return s.client.beginDay(ctx, key, s.owner, s.repo, date, seq)
}

// PutAsset uploads or replaces the named asset in day.
func (s *Store) PutAsset(ctx context.Context, day store.Day, name string, r io.Reader) error {
	return s.client.putAsset(ctx, s.owner, s.repo, day, name, r)
}

// DeleteAsset removes the named asset from day.
func (s *Store) DeleteAsset(ctx context.Context, day store.Day, name string) error {
	a, ok := day.Assets[name]
	if !ok {
		// Fallback: fetch the release's current assets in case the asset was
		// uploaded after the Day value was read, but the Assets map wasn't refreshed.
		rel, err := s.client.getRelease(ctx, s.owner, s.repo, day.ID)
		if err != nil {
			return nil // idempotent: if we can't fetch the release, treat as deleted
		}
		// Look for the asset in the release's current assets.
		for _, asset := range rel.Assets {
			if wireToLogical(asset.Name) == name {
				return s.client.deleteAsset(ctx, s.owner, s.repo, fmt.Sprintf("%d", asset.ID))
			}
		}
		return nil // idempotent: asset not found in release either
	}
	return s.client.deleteAsset(ctx, s.owner, s.repo, a.ID)
}

// Publish marks day as published (draft=false, prerelease=true).
func (s *Store) Publish(ctx context.Context, day store.Day) error {
	return s.client.publishDay(ctx, s.owner, s.repo, day)
}

// DeleteDay removes the release and its tag ref.
func (s *Store) DeleteDay(ctx context.Context, day store.Day) error {
	return s.client.deleteDay(ctx, s.owner, s.repo, day)
}

type assetErr struct {
	name string
	err  error
}

func (e *assetErr) Error() string { return "asset " + e.name + ": " + e.err.Error() }
func (e *assetErr) Unwrap() error { return e.err }
