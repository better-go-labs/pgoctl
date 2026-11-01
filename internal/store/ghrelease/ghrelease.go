// Package ghrelease implements store.Store using GitHub Releases.
package ghrelease

import (
	"context"
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
		if store.ValidRunID(rest[:len(rest)-len(".pprof")]) && strings.HasSuffix(rest, ".pprof") {
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

// New returns a Store for key, authenticating with token against baseURL.
// baseURL defaults to "https://api.github.com" when empty.
func New(key store.Key, token, baseURL string) *Store {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	hc := &http.Client{Timeout: 0}
	parts := splitRepo(key.Repo)
	return &Store{
		key:    key,
		owner:  parts[0],
		repo:   parts[1],
		client: newClient(baseURL, token, hc),
	}
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

// GetRepoInfo returns repository metadata for the configured repo.
func (s *Store) GetRepoInfo(ctx context.Context) (*RepoInfo, error) {
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
		return nil // idempotent
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
