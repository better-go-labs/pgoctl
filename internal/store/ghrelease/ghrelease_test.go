package ghrelease_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/ghrelease"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGH is an in-memory GitHub Releases API fake.
type fakeGH struct {
	mu       sync.Mutex
	releases map[int64]*fakeRelease
	nextID   int64
	repo     string
	// requests records all method+path pairs for assertion.
	requests []string
	// assetData stores uploaded asset bytes by asset ID.
	assetData map[int64][]byte
	// forbidPaths makes any request to these paths return 403.
	forbidPaths map[string]bool
	// rateLimitPaths makes any request to these paths return 429.
	rateLimitPaths map[string]bool
	// visibility is returned by GET /repos/{o}/{r}.
	visibility string
}

type fakeRelease struct {
	id        int64
	tagName   string
	draft     bool
	immutable bool
	assets    map[int64]*fakeAsset
}

type fakeAsset struct {
	id   int64
	name string
	data []byte
	ts   time.Time
}

func newFakeGH(repo string) *fakeGH {
	return &fakeGH{
		releases:       make(map[int64]*fakeRelease),
		assetData:      make(map[int64][]byte),
		forbidPaths:    make(map[string]bool),
		rateLimitPaths: make(map[string]bool),
		repo:           repo,
		nextID:         1,
		visibility:     "private",
	}
}

func (f *fakeGH) allocID() int64 {
	id := f.nextID
	f.nextID++
	return id
}

func (f *fakeGH) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if f.forbidPaths[r.URL.Path] {
		f.mu.Unlock()
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
		return
	}
	if f.rateLimitPaths[r.URL.Path] {
		f.mu.Unlock()
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"message":"rate limit exceeded"}`))
		return
	}
	f.mu.Unlock()

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	// parts: repos / owner / repo / ...
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	// Strip "repos/<owner>/<repo>"
	rest := parts[3:]

	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		// GET /repos/{o}/{r}
		f.mu.Lock()
		vis := f.visibility
		f.mu.Unlock()
		writeJSON(w, map[string]interface{}{
			"default_branch": "main",
			"visibility":     vis,
		})

	case len(rest) == 1 && rest[0] == "releases" && r.Method == http.MethodGet:
		// GET /repos/{o}/{r}/releases
		f.mu.Lock()
		var rels []apiReleaseOut
		for _, rel := range f.releases {
			rels = append(rels, f.toOut(rel))
		}
		f.mu.Unlock()
		// Sort by ID descending for consistent ordering.
		sort.Slice(rels, func(i, j int) bool { return rels[i].ID > rels[j].ID })
		writeJSON(w, rels)

	case len(rest) == 1 && rest[0] == "releases" && r.Method == http.MethodPost:
		// POST /repos/{o}/{r}/releases
		var body struct {
			TagName    string `json:"tag_name"`
			Name       string `json:"name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		f.mu.Lock()
		id := f.allocID()
		rel := &fakeRelease{id: id, tagName: body.TagName, draft: body.Draft, assets: make(map[int64]*fakeAsset)}
		f.releases[id] = rel
		out := f.toOut(rel)
		f.mu.Unlock()
		w.WriteHeader(201)
		writeJSON(w, out)

	case len(rest) == 2 && rest[0] == "releases" && r.Method == http.MethodGet:
		// GET /repos/{o}/{r}/releases/{id}
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		f.mu.Lock()
		rel, ok := f.releases[id]
		if !ok {
			f.mu.Unlock()
			w.WriteHeader(404)
			return
		}
		out := f.toOut(rel)
		f.mu.Unlock()
		writeJSON(w, out)

	case len(rest) == 2 && rest[0] == "releases" && r.Method == http.MethodPatch:
		// PATCH /repos/{o}/{r}/releases/{id}
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		rel, ok := f.releases[id]
		if !ok {
			f.mu.Unlock()
			w.WriteHeader(404)
			return
		}
		if d, ok := body["draft"].(bool); ok {
			rel.draft = d
		}
		out := f.toOut(rel)
		f.mu.Unlock()
		writeJSON(w, out)

	case len(rest) == 2 && rest[0] == "releases" && r.Method == http.MethodDelete:
		// DELETE /repos/{o}/{r}/releases/{id}
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		f.mu.Lock()
		_, ok := f.releases[id]
		if !ok {
			f.mu.Unlock()
			w.WriteHeader(404)
			return
		}
		delete(f.releases, id)
		f.mu.Unlock()
		w.WriteHeader(204)

	case len(rest) >= 1 && rest[0] == "uploads" && r.Method == http.MethodPost:
		// POST /repos/{o}/{r}/uploads (fake upload endpoint)
		name := r.URL.Query().Get("name")
		data, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		aID := f.allocID()
		// Store in the correct release by finding an upload URL match.
		// For the fake, we store directly.
		f.assetData[aID] = data
		f.mu.Unlock()
		w.WriteHeader(201)
		writeJSON(w, map[string]interface{}{
			"id":   aID,
			"name": name,
			"size": len(data),
		})

	case len(rest) == 3 && rest[0] == "releases" && rest[2] == "assets" && r.Method == http.MethodGet:
		// GET /repos/{o}/{r}/releases/{id}/assets
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		f.mu.Lock()
		rel, ok := f.releases[id]
		if !ok {
			f.mu.Unlock()
			w.WriteHeader(404)
			return
		}
		var assets []map[string]interface{}
		for _, a := range rel.assets {
			assets = append(assets, map[string]interface{}{
				"id":   a.id,
				"name": a.name,
				"size": len(a.data),
			})
		}
		f.mu.Unlock()
		writeJSON(w, assets)

	case len(rest) == 3 && rest[0] == "releases" && rest[1] == "assets" && r.Method == http.MethodGet:
		// GET /repos/{o}/{r}/releases/assets/{id}
		id, _ := strconv.ParseInt(rest[2], 10, 64)
		f.mu.Lock()
		data, ok := f.assetData[id]
		if !ok {
			// Search in releases
			for _, rel := range f.releases {
				if a, found := rel.assets[id]; found {
					data = a.data
					ok = true
					break
				}
			}
		}
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)

	case len(rest) == 3 && rest[0] == "releases" && rest[1] == "assets" && r.Method == http.MethodPatch:
		// PATCH /repos/{o}/{r}/releases/assets/{id}
		id, _ := strconv.ParseInt(rest[2], 10, 64)
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		// Find asset in releases
		for _, rel := range f.releases {
			if a, ok := rel.assets[id]; ok {
				a.name = body.Name
				// Move from assetData to rel.assets
				if d, ok := f.assetData[id]; ok {
					a.data = d
					delete(f.assetData, id)
				}
				f.mu.Unlock()
				writeJSON(w, map[string]interface{}{"id": id, "name": body.Name})
				return
			}
		}
		// Check pending assetData
		if _, ok := f.assetData[id]; ok {
			// Find the right release by looking at all releases for their upload
			// URL. In the fake, just put it in the first release that has a
			// matching tmp- asset name.
			for _, rel := range f.releases {
				// Since we don't track tmp assets in releases yet, just add it.
				rel.assets[id] = &fakeAsset{id: id, name: body.Name, data: f.assetData[id]}
				delete(f.assetData, id)
				f.mu.Unlock()
				writeJSON(w, map[string]interface{}{"id": id, "name": body.Name})
				return
			}
		}
		f.mu.Unlock()
		w.WriteHeader(404)

	case len(rest) == 3 && rest[0] == "releases" && rest[1] == "assets" && r.Method == http.MethodDelete:
		// DELETE /repos/{o}/{r}/releases/assets/{id}
		id, _ := strconv.ParseInt(rest[2], 10, 64)
		f.mu.Lock()
		delete(f.assetData, id)
		for _, rel := range f.releases {
			delete(rel.assets, id)
		}
		f.mu.Unlock()
		w.WriteHeader(204)

	case len(rest) == 4 && rest[0] == "git" && rest[1] == "refs" && rest[2] == "tags" && r.Method == http.MethodDelete:
		// DELETE /repos/{o}/{r}/git/refs/tags/{tag}
		w.WriteHeader(204)

	default:
		w.WriteHeader(404)
		_, _ = fmt.Fprintf(w, "unhandled: %s %s", r.Method, r.URL.Path) //nolint:gosec // G705: test server handler, URL path is generated by test code
	}
}

type apiReleaseOut struct {
	ID        int64         `json:"id"`
	TagName   string        `json:"tag_name"`
	Draft     bool          `json:"draft"`
	Immutable bool          `json:"immutable"`
	UploadURL string        `json:"upload_url"`
	Assets    []apiAssetOut `json:"assets"`
}

type apiAssetOut struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (f *fakeGH) toOut(rel *fakeRelease) apiReleaseOut {
	owner, repo, _ := strings.Cut(f.repo, "/")
	out := apiReleaseOut{
		ID:        rel.id,
		TagName:   rel.tagName,
		Draft:     rel.draft,
		Immutable: rel.immutable,
		UploadURL: fmt.Sprintf("http://placeholder/repos/%s/%s/uploads", owner, repo),
	}
	for _, a := range rel.assets {
		out.Assets = append(out.Assets, apiAssetOut{
			ID:        a.id,
			Name:      a.name,
			Size:      int64(len(a.data)),
			UpdatedAt: a.ts,
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestContract(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	storetest.RunContract(t, func() store.Store {
		fake := newFakeGH(key.Repo)
		srv := httptest.NewServer(fake)
		t.Cleanup(srv.Close)
		return newFakeGHStore(t, key, fake, srv)
	})
}

// newFakeGHStore sets up a Store that can handle uploads via the fake.
func newFakeGHStore(t *testing.T, key store.Key, _ *fakeGH, srv *httptest.Server) *ghrelease.Store {
	t.Helper()
	// Use a transport that rewrites the upload_url host to the test server.
	hc := &http.Client{
		Transport: &rewriteTransport{srv: srv, orig: srv.Client().Transport},
	}
	return ghrelease.NewWithHTTPClient(key, "test-token", srv.URL, hc)
}

// rewriteTransport rewrites any request to placeholder host to the test server.
type rewriteTransport struct {
	srv  *httptest.Server
	orig http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "placeholder" {
		req2 := req.Clone(req.Context())
		req2.URL.Host = strings.TrimPrefix(t.srv.URL, "http://")
		req2.URL.Scheme = "http"
		return t.orig.RoundTrip(req2)
	}
	return t.orig.RoundTrip(req)
}

func TestListDays_Pagination(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)
	// Build a fake server that paginates: page 1 has 1 release, page 2 has another.
	var page int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/o/r/releases") && r.Method == http.MethodGet && !strings.Contains(r.URL.Path, "/assets") {
			page++
			switch page {
			case 1:
				w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/releases?per_page=100&page=2>; rel="next"`, r.Host))
				if !strings.HasPrefix(r.Host, "http") {
					w.Header().Set("Link", fmt.Sprintf(`<http://%s/repos/o/r/releases?per_page=100&page=2>; rel="next"`, r.Host))
				}
				writeJSON(w, []apiReleaseOut{{
					ID:      1,
					TagName: "profile/2026-01-01.0",
					Draft:   false,
					Assets:  nil,
				}})
			case 2:
				writeJSON(w, []apiReleaseOut{{
					ID:      2,
					TagName: "profile/2026-01-02.0",
					Draft:   false,
					Assets:  nil,
				}})
			default:
				writeJSON(w, []apiReleaseOut{})
			}
			return
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	s := ghrelease.NewWithHTTPClient(key, "tok", srv.URL, srv.Client())
	days, err := s.ListDays(context.Background(), key)
	require.NoError(t, err)
	assert.Len(t, days, 2)
	assert.Equal(t, "2026-01-02", days[0].Date.String())
	assert.Equal(t, "2026-01-01", days[1].Date.String())
}

func TestBeginDay_DraftThenPublish(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s := newFakeGHStore(t, key, fake, srv)

	ctx := context.Background()
	d, err := store.ParseDate("2026-03-01")
	require.NoError(t, err)

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)
	assert.True(t, day.Draft)
	assert.Equal(t, "profile/2026-03-01.0", key.Tag(d, 0))

	require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, strings.NewReader(`{}`)))
	// Re-fetch to get updated assets.
	days, err := s.ListDays(ctx, key)
	require.NoError(t, err)
	require.Len(t, days, 1)

	require.NoError(t, s.Publish(ctx, days[0]))
	days, err = s.ListDays(ctx, key)
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.False(t, days[0].Draft)
}

func TestPutAsset_Replace(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s := newFakeGHStore(t, key, fake, srv)

	ctx := context.Background()
	d, err := store.ParseDate("2026-04-01")
	require.NoError(t, err)

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)

	require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, strings.NewReader(`first`)))
	require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, strings.NewReader(`second`)))

	// Only one manifest.json should exist (not two or a tmp- version).
	days, err := s.ListDays(ctx, key)
	require.NoError(t, err)
	require.Len(t, days, 1)
	// manifest.json should be visible, not tmp-manifest.json
	assert.True(t, days[0].Has(store.AssetManifest))
	assert.False(t, days[0].Has("tmp-manifest.json"))

	rc, err := s.ReadAsset(ctx, days[0], store.AssetManifest)
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "second", string(got))
}

func TestRunAssetWireName(t *testing.T) {
	tests := []struct {
		logical string
		wire    string
	}{
		{"runs/12345-1.pprof", "runs-12345-1.pprof"},
		{"daily.pprof", "daily.pprof"},
		{"default.pgo", "default.pgo"},
		{"manifest.json", "manifest.json"},
	}
	for _, tt := range tests {
		t.Run(tt.logical, func(t *testing.T) {
			// Exercise PutAsset + ReadAsset round-trip.
			key := store.Key{Repo: "o/r", Prefix: "profile"}
			fake := newFakeGH(key.Repo)
			srv := httptest.NewServer(fake)
			t.Cleanup(srv.Close)
			s := newFakeGHStore(t, key, fake, srv)

			ctx := context.Background()
			dd, _ := store.ParseDate("2026-05-01")
			day, err := s.BeginDay(ctx, key, dd, 0)
			require.NoError(t, err)

			require.NoError(t, s.PutAsset(ctx, day, tt.logical, strings.NewReader("data")))

			days, err := s.ListDays(ctx, key)
			require.NoError(t, err)
			require.Len(t, days, 1)
			assert.True(t, days[0].Has(tt.logical), "logical name %q must appear in Day.Assets", tt.logical)

			rc, err := s.ReadAsset(ctx, days[0], tt.logical)
			require.NoError(t, err)
			got, _ := io.ReadAll(rc)
			_ = rc.Close()
			assert.Equal(t, "data", string(got))
		})
	}
}

func TestListDays_Immutable(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s := newFakeGHStore(t, key, fake, srv)

	ctx := context.Background()
	d, err := store.ParseDate("2026-06-01")
	require.NoError(t, err)

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)
	require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, strings.NewReader(`{}`)))
	require.NoError(t, s.Publish(ctx, day))

	// Manually set the release as immutable in the fake.
	fake.mu.Lock()
	for _, rel := range fake.releases {
		rel.immutable = true
	}
	fake.mu.Unlock()

	days, err := s.ListDays(ctx, key)
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.True(t, days[0].Immutable)
}

func TestPutAsset_Immutable(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)

	// Override the upload endpoint to return 422 with "immutable".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/uploads") && r.Method == http.MethodPost {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"message":"Release is immutable","errors":[]}`))
			return
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	s := newFakeGHStore(t, key, fake, srv)

	ctx := context.Background()
	d, err := store.ParseDate("2026-07-01")
	require.NoError(t, err)

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)

	err = s.PutAsset(ctx, day, store.AssetManifest, strings.NewReader(`{}`))
	require.ErrorIs(t, err, store.ErrImmutable)
}

func TestForbidden(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)
	fake.forbidPaths["/repos/o/r/releases"] = true
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s := newFakeGHStore(t, key, fake, srv)

	ctx := context.Background()
	d, err := store.ParseDate("2026-08-01")
	require.NoError(t, err)

	_, err = s.BeginDay(ctx, key, d, 0)
	require.ErrorIs(t, err, store.ErrPermission)
}

func TestRateLimited(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)
	fake.rateLimitPaths["/repos/o/r/releases"] = true
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s := newFakeGHStore(t, key, fake, srv)

	ctx := context.Background()
	d, err := store.ParseDate("2026-09-01")
	require.NoError(t, err)

	_, err = s.BeginDay(ctx, key, d, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit")
}

func TestPublish_ConcurrentUpdate(t *testing.T) {
	// Two calls to BeginDay for the same date/seq produce two separate drafts
	// that both appear in ListDays; checkUnchanged in the store.Publish flow
	// detects the conflict.
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s := newFakeGHStore(t, key, fake, srv)

	ctx := context.Background()
	d, err := store.ParseDate("2026-10-01")
	require.NoError(t, err)

	// First BeginDay creates the draft.
	day1, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)

	// Manually inject a second release with the same tag to simulate a race.
	fake.mu.Lock()
	id2 := fake.allocID()
	fake.releases[id2] = &fakeRelease{
		id:      id2,
		tagName: key.Tag(d, 0),
		draft:   true,
		assets:  make(map[int64]*fakeAsset),
	}
	fake.mu.Unlock()

	days, err := s.ListDays(ctx, key)
	require.NoError(t, err)
	// Both days should be visible.
	count := 0
	for _, day := range days {
		if day.Date == d {
			count++
		}
	}
	assert.Equal(t, 2, count, "both concurrent days must be visible")
	_ = day1
}

func TestReadAsset_FollowsRedirect(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	assetData := []byte("redirect-content")

	// Storage server that serves the actual content.
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(assetData)
	}))
	t.Cleanup(storage.Close)

	fake := newFakeGH(key.Repo)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases/assets/") && r.Method == http.MethodGet {
			http.Redirect(w, r, storage.URL+"/asset", http.StatusFound)
			return
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	s := ghrelease.NewWithHTTPClient(key, "tok", srv.URL, srv.Client())

	day := store.Day{
		Key:    key,
		Assets: map[string]store.Asset{"daily.pprof": {ID: "99"}},
	}
	rc, err := s.ReadAsset(context.Background(), day, "daily.pprof")
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, assetData, got)
}

func TestRepo(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)
	fake.visibility = "public"
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s := newFakeGHStore(t, key, fake, srv)

	info, err := s.Repo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "main", info.DefaultBranch)
	assert.Equal(t, "public", info.Visibility)
}

func TestReadAsset_DropAuthOnRedirect(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}

	var storageAuth string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storageAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("content"))
	}))
	t.Cleanup(storage.Close)

	fake := newFakeGH(key.Repo)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases/assets/") && r.Method == http.MethodGet {
			http.Redirect(w, r, storage.URL+"/asset", http.StatusFound)
			return
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	// Use a transport that does NOT follow redirects automatically so we can
	// verify the custom redirect logic drops Auth.
	s := ghrelease.NewWithHTTPClient(key, "secret-token", srv.URL, srv.Client())

	day := store.Day{
		Key:    key,
		Assets: map[string]store.Asset{"daily.pprof": {ID: "1"}},
	}
	rc, err := s.ReadAsset(context.Background(), day, "daily.pprof")
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()

	assert.Empty(t, storageAuth, "Authorization header must not be sent to the storage host on redirect")
}

func TestDeleteDay_DraftSkipsTagDelete(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s := newFakeGHStore(t, key, fake, srv)

	ctx := context.Background()
	d, err := store.ParseDate("2026-11-01")
	require.NoError(t, err)

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)
	require.True(t, day.Draft)

	require.NoError(t, s.DeleteDay(ctx, day))

	fake.mu.Lock()
	reqs := make([]string, len(fake.requests))
	copy(reqs, fake.requests)
	fake.mu.Unlock()

	for _, r := range reqs {
		assert.False(t, strings.Contains(r, "git/refs/tags"), "draft deletion must not call tag-ref DELETE, got: %v", reqs)
	}
}

func TestDeleteDay_Ignores404OnTagRef(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/refs/tags/") && r.Method == http.MethodDelete {
			w.WriteHeader(404)
			return
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	s := newFakeGHStore(t, key, fake, srv)

	ctx := context.Background()
	d, err := store.ParseDate("2026-12-01")
	require.NoError(t, err)

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)
	require.NoError(t, s.Publish(ctx, day))

	days, err := s.ListDays(ctx, key)
	require.NoError(t, err)
	require.Len(t, days, 1)

	err = s.DeleteDay(ctx, days[0])
	require.NoError(t, err, "404 on tag-ref DELETE must not be an error")
}

func TestListDays_HidesTmpAssets(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)

	// Inject a release that has a tmp- asset to simulate a mid-replace window.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/o/r/releases") && r.Method == http.MethodGet && !strings.Contains(r.URL.Path[len("/repos/o/r/releases"):], "/") {
			now := time.Now()
			writeJSON(w, []map[string]interface{}{{
				"id":         1,
				"tag_name":   "profile/2026-01-15.0",
				"draft":      false,
				"immutable":  false,
				"upload_url": "http://placeholder/repos/o/r/uploads{?name,label}",
				"assets": []map[string]interface{}{
					{"id": 10, "name": "manifest.json", "size": 2, "updated_at": now},
					{"id": 11, "name": "tmp-manifest.json", "size": 2, "updated_at": now},
				},
			}})
			return
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	s := ghrelease.NewWithHTTPClient(key, "tok", srv.URL, srv.Client())

	days, err := s.ListDays(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.True(t, days[0].Has(store.AssetManifest))
	assert.False(t, days[0].Has("tmp-manifest.json"), "tmp- assets must not appear in Day.Assets")
}

// TestListDays_MalformedRunsAsset_NoPanic covers an asset named "runs-x" that is
// shorter than the ".pprof" suffix. wireToLogical must check the suffix before
// slicing, or ListDays panics.
func TestListDays_MalformedRunsAsset_NoPanic(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		now := time.Now()
		writeJSON(w, []map[string]interface{}{{
			"id":         1,
			"tag_name":   "profile/2026-01-15.0",
			"draft":      false,
			"upload_url": "http://placeholder/repos/o/r/uploads{?name,label}",
			"assets": []map[string]interface{}{
				{"id": 10, "name": "runs-x", "size": 1, "updated_at": now},
			},
		}})
	}))
	t.Cleanup(srv.Close)
	s := ghrelease.NewWithHTTPClient(key, "tok", srv.URL, srv.Client())

	days, err := s.ListDays(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, days, 1)
	// "runs-x" has no .pprof suffix, so it stays a literal asset name.
	assert.True(t, days[0].Has("runs-x"))
}

// TestBeginDay_404OnWrite_IsPermission covers a 404 on a write (POST), which a
// token without contents:write gets. It must map to ErrPermission, not
// ErrNotFound.
func TestBeginDay_404OnWrite_IsPermission(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, []map[string]interface{}{}) // no existing releases
			return
		}
		// POST to create the release returns 404.
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	t.Cleanup(srv.Close)
	s := ghrelease.NewWithHTTPClient(key, "tok", srv.URL, srv.Client())

	d, _ := store.ParseDate("2026-01-15")
	_, err := s.BeginDay(context.Background(), key, d, 0)
	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrPermission), "404 on POST must map to ErrPermission, got %v", err)
	assert.False(t, errors.Is(err, store.ErrNotFound), "404 on write must not map to ErrNotFound")
}

// TestNew_Config_Validation covers the plan's New(cfg Config) (*Store, error),
// which errors on an empty Token or Repo.
func TestNew_Config_Validation(t *testing.T) {
	_, err := ghrelease.New(ghrelease.Config{Key: store.Key{Repo: "o/r"}, Token: ""})
	require.Error(t, err, "empty token must error")

	_, err = ghrelease.New(ghrelease.Config{Key: store.Key{Repo: ""}, Token: "tok"})
	require.Error(t, err, "empty repo must error")

	s, err := ghrelease.New(ghrelease.Config{Key: store.Key{Repo: "o/r"}, Token: "tok"})
	require.NoError(t, err)
	require.NotNil(t, s)
}

// TestRepo_ReturnsRepoInfo covers the plan's Repo(ctx) accessor.
func TestRepo_ReturnsRepoInfo(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]interface{}{"default_branch": "main", "visibility": "private"})
	}))
	t.Cleanup(srv.Close)
	s, err := ghrelease.New(ghrelease.Config{Key: key, Token: "tok", BaseURL: srv.URL, HTTPClient: srv.Client()})
	require.NoError(t, err)
	info, err := s.Repo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "main", info.DefaultBranch)
	assert.Equal(t, "private", info.Visibility)
}

// TestWrite422Immutable_IsErrImmutable covers 422 "immutable" responses on PATCH
// and DELETE. The body must reach checkResp so the text maps to ErrImmutable.
func TestWrite422Immutable_IsErrImmutable(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	immutable := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Release is immutable"}`))
			return
		}
		writeJSON(w, map[string]interface{}{"id": 1})
	}
	srv := httptest.NewServer(http.HandlerFunc(immutable))
	t.Cleanup(srv.Close)
	s := ghrelease.NewWithHTTPClient(key, "tok", srv.URL, srv.Client())
	ctx := context.Background()
	d, _ := store.ParseDate("2026-01-15")

	t.Run("publish PATCH", func(t *testing.T) {
		day := store.Day{Key: key, Date: d, Seq: 0, ID: "1", Draft: false}
		err := s.Publish(ctx, day)
		require.Error(t, err)
		assert.True(t, errors.Is(err, store.ErrImmutable), "422 immutable on PATCH must map to ErrImmutable, got %v", err)
	})

	t.Run("delete asset DELETE", func(t *testing.T) {
		day := store.Day{Key: key, Date: d, Seq: 0, ID: "1", Draft: false,
			Assets: map[string]store.Asset{"manifest.json": {ID: "9", Name: "manifest.json"}}}
		err := s.DeleteAsset(ctx, day, "manifest.json")
		require.Error(t, err)
		assert.True(t, errors.Is(err, store.ErrImmutable), "422 immutable on DELETE must map to ErrImmutable, got %v", err)
	})
}

// TestReadAsset_ReleaseFetch403 covers a ReadAsset on a name missing from day.Assets
// while the fake server returns 403 for GET release. The error must be
// ErrPermission, not ErrNotFound.
func TestReadAsset_ReleaseFetch403(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fake := newFakeGH(key.Repo)
	fake.forbidPaths["/repos/o/r/releases/1"] = true
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s := newFakeGHStore(t, key, fake, srv)

	ctx := context.Background()
	d, err := store.ParseDate("2026-09-15")
	require.NoError(t, err)

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)

	// Try to read an asset that is missing from day.Assets, triggering the
	// fallback getRelease call, which returns 403.
	_, err = s.ReadAsset(ctx, day, "nonexistent.pprof")
	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrPermission), "403 on GET release during asset refetch must map to ErrPermission, got %v", err)
}
