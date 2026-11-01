// Package artifact implements store.Store using GitHub Actions Artifacts.
package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
)

// maxZipEntrySize is the maximum number of bytes read from a single zip entry
// during decompression. It is a variable (not a constant) so tests can lower
// it without allocating huge buffers. Exported via export_test.go.
var maxZipEntrySize int64 = 512 * 1024 * 1024 // 512 MiB

// Config holds the configuration for an artifact Store.
type Config struct {
	// Key is the profile key.
	Key store.Key
	// RuntimeToken is ACTIONS_RUNTIME_TOKEN for uploading via the results service.
	RuntimeToken string
	// ResultsURL is ACTIONS_RESULTS_URL for the results service.
	ResultsURL string
	// APIURL is the GitHub API base URL (defaults to https://api.github.com).
	APIURL string
	// Token is the GitHub token for read/delete operations.
	Token string
	// LegacyName is the artifact name used by v0.1.x for RestoreLegacy.
	LegacyName string
}

// Store implements store.Store using GitHub Actions Artifacts.
type Store struct {
	cfg Config
	mu  sync.Mutex
	// drafts maps a day identity (key.Tag(date,seq)) to its staging directory.
	drafts map[string]string
	http   *http.Client
}

// New returns a Store with the given config.
func New(cfg Config) *Store {
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.github.com"
	}
	return &Store{
		cfg:    cfg,
		drafts: make(map[string]string),
		http:   &http.Client{},
	}
}

// NewWithHTTPClient returns a Store using the given http.Client. Used in tests.
func NewWithHTTPClient(cfg Config, hc *http.Client) *Store {
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.github.com"
	}
	return &Store{
		cfg:    cfg,
		drafts: make(map[string]string),
		http:   hc,
	}
}

// artifactName converts a tag to an artifact name (slashes become underscores).
func artifactName(tag string) string { return strings.ReplaceAll(tag, "/", "_") }

// tagFromName converts an artifact name back to a tag (underscores in the right
// positions become slashes). It tries to match using ParseTag for the key.
func tagFromName(key store.Key, name string) (date store.Date, seq int, ok bool) {
	// Try replacing underscores with slashes to recover the tag.
	// The prefix is key.Prefix + "/" + optional service + "/".
	prefix := key.Prefix + "_"
	if !strings.HasPrefix(name, prefix) {
		return store.Date{}, 0, false
	}
	rest := name[len(prefix):]
	if key.Service != "" {
		svcPrefix := key.Service + "_"
		if !strings.HasPrefix(rest, svcPrefix) {
			return store.Date{}, 0, false
		}
		rest = rest[len(svcPrefix):]
	}
	// rest must be <date>.<seq> with no further underscores (dates use '-').
	tag := key.Prefix + "/" + func() string {
		if key.Service != "" {
			return key.Service + "/" + rest
		}
		return rest
	}()
	return key.ParseTag(tag)
}

// apiArtifact is a subset of the GitHub Actions artifacts API response.
type apiArtifact struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	Expired            bool      `json:"expired"`
	CreatedAt          time.Time `json:"created_at"`
	ArchiveDownloadURL string    `json:"archive_download_url"`
}

// ListDays returns all days for key, including any in-process drafts.
func (s *Store) ListDays(ctx context.Context, key store.Key) ([]store.Day, error) {
	artifacts, err := s.listArtifacts(ctx, key)
	if err != nil {
		return nil, err
	}

	// Group by name; keep the one with the newest created_at per name.
	best := make(map[string]apiArtifact)
	for _, a := range artifacts {
		if cur, ok := best[a.Name]; !ok || a.CreatedAt.After(cur.CreatedAt) {
			best[a.Name] = a
		}
	}

	var out []store.Day
	for _, a := range best {
		d, seq, ok := tagFromName(key, a.Name)
		if !ok {
			continue
		}
		day := store.Day{
			Key:       key,
			Date:      d,
			Seq:       seq,
			Draft:     false,
			Immutable: true,
			ID:        strconv.FormatInt(a.ID, 10),
			Assets:    map[string]store.Asset{},
		}
		if assets, err := s.enumerateArtifactAssets(ctx, day); err == nil {
			day.Assets = assets
		}
		out = append(out, day)
	}

	// Add in-process drafts.
	s.mu.Lock()
	for tag, dir := range s.drafts {
		d, seq, ok := key.ParseTag(tag)
		if !ok {
			continue
		}
		// Collect assets from the staging directory.
		assets := make(map[string]store.Asset)
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			info, _ := e.Info()
			size := int64(0)
			ts := time.Time{}
			if info != nil {
				size = info.Size()
				ts = info.ModTime()
			}
			assets[name] = store.Asset{ID: "draft-" + name, Name: name, Size: size, UpdatedAt: ts}
		}
		out = append(out, store.Day{
			Key:    key,
			Date:   d,
			Seq:    seq,
			Draft:  true,
			ID:     "draft:" + tag,
			Assets: assets,
		})
	}
	s.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date.After(out[j].Date)
		}
		return out[i].Seq > out[j].Seq
	})
	return out, nil
}

func (s *Store) listArtifacts(ctx context.Context, key store.Key) ([]apiArtifact, error) {
	owner, repo, _ := strings.Cut(key.Repo, "/")
	var all []apiArtifact
	path := fmt.Sprintf("/repos/%s/%s/actions/artifacts?per_page=100", owner, repo)
	for path != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.APIURL+path, nil)
		if err != nil {
			return nil, fmt.Errorf("building request: %w", err)
		}
		s.setHeaders(req)
		resp, err := s.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", path, err)
		}
		if err := func() error {
			defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
			if resp.StatusCode == 403 {
				return fmt.Errorf("GET %s returned 403: %w", path, store.ErrPermission)
			}
			if resp.StatusCode == 404 {
				return fmt.Errorf("GET %s returned 404: %w", path, store.ErrNotFound)
			}
			if resp.StatusCode != 200 {
				return fmt.Errorf("GET %s returned %d", path, resp.StatusCode)
			}
			var page struct {
				Artifacts []apiArtifact `json:"artifacts"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
				return fmt.Errorf("decoding artifacts: %w", err)
			}
			for _, a := range page.Artifacts {
				if !a.Expired {
					all = append(all, a)
				}
			}
			path = nextLink(resp.Header.Get("Link"))
			return nil
		}(); err != nil {
			return nil, err
		}
	}
	return all, nil
}

func (s *Store) setHeaders(req *http.Request) {
	if s.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// ReadAsset reads an asset from the artifact zip identified by day.
func (s *Store) ReadAsset(ctx context.Context, day store.Day, name string) (io.ReadCloser, error) {
	if strings.HasPrefix(day.ID, "draft:") {
		tag := strings.TrimPrefix(day.ID, "draft:")
		s.mu.Lock()
		dir, ok := s.drafts[tag]
		s.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("draft %s: %w", name, store.ErrNotFound)
		}
		path := filepath.Join(dir, name)
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("asset %q: %w", name, store.ErrNotFound)
			}
			return nil, fmt.Errorf("opening %s: %w", name, err)
		}
		return f, nil
	}

	// Download and unzip the artifact.
	zipData, err := s.downloadArtifact(ctx, day)
	if err != nil {
		return nil, err
	}
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("opening artifact zip: %w", err)
	}
	for _, f := range r.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("opening %s in zip: %w", name, err)
			}
			data, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return nil, fmt.Errorf("reading %s from zip: %w", name, err)
			}
			return io.NopCloser(bytes.NewReader(data)), nil
		}
	}
	return nil, fmt.Errorf("asset %q not in artifact: %w", name, store.ErrNotFound)
}

func (s *Store) downloadArtifact(ctx context.Context, day store.Day) ([]byte, error) {
	owner, repo, _ := strings.Cut(day.Key.Repo, "/")
	url := fmt.Sprintf("%s/repos/%s/%s/actions/artifacts/%s/zip", s.cfg.APIURL, owner, repo, day.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	s.setHeaders(req)
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading artifact: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("GET artifact %s returned 403: %w", day.ID, store.ErrPermission)
	}
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("GET artifact %s returned 404: %w", day.ID, store.ErrNotFound)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET artifact %s returned %d", day.ID, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// enumerateArtifactAssets downloads the artifact zip for day and returns a map
// of logical asset name to Asset populated from the zip's central directory.
// Callers treat a non-nil error as "metadata unavailable" and keep the empty map.
func (s *Store) enumerateArtifactAssets(ctx context.Context, day store.Day) (map[string]store.Asset, error) {
	zipData, err := s.downloadArtifact(ctx, day)
	if err != nil {
		return nil, err
	}
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("opening artifact zip: %w", err)
	}
	assets := make(map[string]store.Asset, len(r.File))
	for _, f := range r.File {
		if f.UncompressedSize64 > math.MaxInt64 {
			return nil, fmt.Errorf("zip entry %q: UncompressedSize64 %d overflows int64", f.Name, f.UncompressedSize64)
		}
		assets[f.Name] = store.Asset{
			ID:        day.ID + "/" + f.Name,
			Name:      f.Name,
			Size:      int64(f.UncompressedSize64),
			UpdatedAt: f.Modified,
		}
	}
	return assets, nil
}

// BeginDay returns the day for key/date/seq. For artifacts, drafts are in-process
// staging directories; published days are immutable.
func (s *Store) BeginDay(_ context.Context, key store.Key, date store.Date, seq int) (store.Day, error) {
	tag := key.Tag(date, seq)

	s.mu.Lock()
	defer s.mu.Unlock()

	if dir, ok := s.drafts[tag]; ok {
		assets := draftAssets(dir)
		return store.Day{Key: key, Date: date, Seq: seq, Draft: true, ID: "draft:" + tag, Assets: assets}, nil
	}

	dir, err := os.MkdirTemp("", "pgoctl-artifact-*")
	if err != nil {
		return store.Day{}, fmt.Errorf("creating staging dir: %w", err)
	}
	s.drafts[tag] = dir
	return store.Day{Key: key, Date: date, Seq: seq, Draft: true, ID: "draft:" + tag, Assets: map[string]store.Asset{}}, nil
}

func draftAssets(dir string) map[string]store.Asset {
	assets := make(map[string]store.Asset)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		size := int64(0)
		ts := time.Time{}
		if info != nil {
			size = info.Size()
			ts = info.ModTime()
		}
		assets[e.Name()] = store.Asset{ID: "draft-" + e.Name(), Name: e.Name(), Size: size, UpdatedAt: ts}
	}
	return assets
}

// PutAsset writes data to the staging directory for this day.
func (s *Store) PutAsset(_ context.Context, day store.Day, name string, r io.Reader) error {
	if !strings.HasPrefix(day.ID, "draft:") {
		return fmt.Errorf("writing to published artifact %s: %w", day.ID, store.ErrImmutable)
	}
	tag := strings.TrimPrefix(day.ID, "draft:")
	s.mu.Lock()
	dir, ok := s.drafts[tag]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("no staging directory for %s: %w", tag, store.ErrNotFound)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading asset data: %w", err)
	}
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating directory for %s: %w", name, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	return nil
}

// DeleteAsset removes an asset from the staging directory.
func (s *Store) DeleteAsset(_ context.Context, day store.Day, name string) error {
	if !strings.HasPrefix(day.ID, "draft:") {
		return fmt.Errorf("deleting from published artifact %s: %w", day.ID, store.ErrImmutable)
	}
	tag := strings.TrimPrefix(day.ID, "draft:")
	s.mu.Lock()
	dir, ok := s.drafts[tag]
	s.mu.Unlock()
	if !ok {
		return nil // idempotent
	}
	path := filepath.Join(dir, name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", name, err)
	}
	return nil
}

// Publish uploads the staged files as an artifact and cleans up the staging dir.
func (s *Store) Publish(ctx context.Context, day store.Day) error {
	if !strings.HasPrefix(day.ID, "draft:") {
		return nil // already published
	}
	tag := strings.TrimPrefix(day.ID, "draft:")
	s.mu.Lock()
	dir, ok := s.drafts[tag]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("no staging directory for %s: %w", tag, store.ErrNotFound)
	}

	name := artifactName(tag)
	if err := upload(ctx, s.cfg, s.http, name, dir); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.drafts, tag)
	s.mu.Unlock()
	_ = os.RemoveAll(dir)
	return nil
}

// DeleteDay removes the artifact. Draft days remove the staging directory.
// If the draft was already published (Publish removes it from the drafts map),
// we fall through to delete the published artifact by name so that calling
// DeleteDay with the original draft handle after Publish still cleans up.
func (s *Store) DeleteDay(ctx context.Context, day store.Day) error {
	if strings.HasPrefix(day.ID, "draft:") {
		tag := strings.TrimPrefix(day.ID, "draft:")
		s.mu.Lock()
		dir, ok := s.drafts[tag]
		if ok {
			delete(s.drafts, tag)
		}
		s.mu.Unlock()
		if ok {
			_ = os.RemoveAll(dir)
			return nil
		}
		// Draft was already published; delete the published artifact by name.
		return s.deleteByName(ctx, day.Key, artifactName(tag))
	}

	return s.deleteByID(ctx, day)
}

func (s *Store) deleteByID(ctx context.Context, day store.Day) error {
	owner, repo, _ := strings.Cut(day.Key.Repo, "/")
	path := fmt.Sprintf("/repos/%s/%s/actions/artifacts/%s", owner, repo, day.ID)
	return s.deleteArtifactPath(ctx, path)
}

// deleteByName lists artifacts matching name and deletes each one.
func (s *Store) deleteByName(ctx context.Context, key store.Key, name string) error {
	artifacts, err := s.listArtifacts(ctx, key)
	if err != nil {
		return err
	}
	owner, repo, _ := strings.Cut(key.Repo, "/")
	for _, a := range artifacts {
		if a.Name != name {
			continue
		}
		path := fmt.Sprintf("/repos/%s/%s/actions/artifacts/%d", owner, repo, a.ID)
		if err := s.deleteArtifactPath(ctx, path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) deleteArtifactPath(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.cfg.APIURL+path, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	s.setHeaders(req)
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode == 403 {
		return fmt.Errorf("DELETE %s returned 403: grant the publish job \"permissions: actions: write\": %w", path, store.ErrPermission)
	}
	if resp.StatusCode == 404 {
		return fmt.Errorf("DELETE %s returned 404: %w", path, store.ErrNotFound)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("DELETE %s returned %d", path, resp.StatusCode)
	}
	return nil
}

// RestoreLegacy looks for an artifact with name s.cfg.LegacyName and extracts
// default.pgo into w. It is the v0.1.x migration path.
func (s *Store) RestoreLegacy(ctx context.Context, w io.Writer) error {
	owner, repo, _ := strings.Cut(s.cfg.Key.Repo, "/")
	url := fmt.Sprintf("%s/repos/%s/%s/actions/artifacts?per_page=100&name=%s",
		s.cfg.APIURL, owner, repo, s.cfg.LegacyName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	s.setHeaders(req)
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET artifacts: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GET artifacts returned %d", resp.StatusCode)
	}
	var page struct {
		TotalCount int           `json:"total_count"`
		Artifacts  []apiArtifact `json:"artifacts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return fmt.Errorf("decoding artifacts: %w", err)
	}
	if page.TotalCount == 0 || len(page.Artifacts) == 0 {
		return fmt.Errorf("no legacy artifact %q: %w", s.cfg.LegacyName, store.ErrNoSnapshot)
	}

	// Use the most recently created artifact.
	best := page.Artifacts[0]
	for _, a := range page.Artifacts[1:] {
		if a.CreatedAt.After(best.CreatedAt) {
			best = a
		}
	}

	legacyDay := store.Day{Key: s.cfg.Key, ID: strconv.FormatInt(best.ID, 10)}
	zipData, err := s.downloadArtifact(ctx, legacyDay)
	if err != nil {
		return err
	}
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("opening legacy artifact zip: %w", err)
	}
	for _, f := range r.File {
		if f.Name == store.AssetDefault {
			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("opening default.pgo in zip: %w", err)
			}
			defer rc.Close() //nolint:errcheck
			limit := maxZipEntrySize
			n, copyErr := io.Copy(w, io.LimitReader(rc, limit+1))
			if copyErr != nil {
				return fmt.Errorf("copying default.pgo: %w", copyErr)
			}
			if n > limit {
				return fmt.Errorf("default.pgo entry exceeds %d-byte decompression limit", limit)
			}
			return nil
		}
	}
	return fmt.Errorf("default.pgo not found in legacy artifact: %w", store.ErrNoSnapshot)
}

// nextLink extracts the URL of the "next" relation from a GitHub Link header.
func nextLink(header string) string {
	if header == "" {
		return ""
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		segments := strings.Split(part, ";")
		if len(segments) < 2 {
			continue
		}
		url := strings.TrimSpace(segments[0])
		url = strings.Trim(url, "<>")
		for _, seg := range segments[1:] {
			seg = strings.TrimSpace(seg)
			if seg == `rel="next"` {
				return url
			}
		}
	}
	return ""
}

// zipDir creates a zip archive of dir in memory and returns the bytes.
func zipDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading staging dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		f, err := zw.Create(e.Name())
		if err != nil {
			return nil, fmt.Errorf("creating zip entry %s: %w", e.Name(), err)
		}
		if _, err := f.Write(data); err != nil {
			return nil, fmt.Errorf("writing zip entry %s: %w", e.Name(), err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("closing zip: %w", err)
	}
	return buf.Bytes(), nil
}
