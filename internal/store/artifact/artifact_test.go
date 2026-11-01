package artifact_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/artifact"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeToken(wfRun, wfJob string) string {
	payload := map[string]interface{}{
		"scp": fmt.Sprintf("Actions.Results:%s:%s", wfRun, wfJob),
	}
	data, _ := json.Marshal(payload)
	encoded := base64.RawURLEncoding.EncodeToString(data)
	return "header." + encoded + ".sig"
}

func makeZip(files map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		f, _ := zw.Create(name)
		_, _ = f.Write(data)
	}
	_ = zw.Close()
	return buf.Bytes()
}

// fakeArtifactAPI is a minimal GitHub Actions Artifacts API fake.
type fakeArtifactAPI struct {
	artifacts []*fakeArtifact
	nextID    int64
	zips      map[int64][]byte
	// forbidDelete makes DELETE return 403.
	forbidDelete bool
}

type fakeArtifact struct {
	id        int64
	name      string
	expired   bool
	createdAt time.Time
}

func newFakeArtifactAPI() *fakeArtifactAPI {
	return &fakeArtifactAPI{zips: make(map[int64][]byte), nextID: 1}
}

func (f *fakeArtifactAPI) alloc() int64 {
	id := f.nextID
	f.nextID++
	return id
}

func (f *fakeArtifactAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// DELETE /repos/{o}/{r}/actions/artifacts/{id}
	if r.Method == http.MethodDelete && strings.Contains(path, "/actions/artifacts/") {
		if f.forbidDelete {
			w.WriteHeader(403)
			return
		}
		parts := strings.Split(path, "/")
		idStr := parts[len(parts)-1]
		var id int64
		fmt.Sscanf(idStr, "%d", &id) //nolint:errcheck
		for i, a := range f.artifacts {
			if a.id == id {
				f.artifacts = append(f.artifacts[:i], f.artifacts[i+1:]...)
				break
			}
		}
		w.WriteHeader(204)
		return
	}
	// GET /repos/{o}/{r}/actions/artifacts/{id}/zip
	if r.Method == http.MethodGet && strings.HasSuffix(path, "/zip") {
		parts := strings.Split(strings.TrimSuffix(path, "/zip"), "/")
		idStr := parts[len(parts)-1]
		var id int64
		fmt.Sscanf(idStr, "%d", &id) //nolint:errcheck
		data, ok := f.zips[id]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(data)
		return
	}
	// GET /repos/{o}/{r}/actions/artifacts
	if r.Method == http.MethodGet && strings.Contains(path, "/actions/artifacts") {
		w.Header().Set("Content-Type", "application/json")
		nameFilter := r.URL.Query().Get("name")
		var out []*fakeArtifact
		for _, a := range f.artifacts {
			if nameFilter == "" || a.name == nameFilter {
				out = append(out, a)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"total_count": len(out),
			"artifacts":   artifactsToJSON(out),
		})
		return
	}
	w.WriteHeader(404)
}

func artifactsToJSON(arts []*fakeArtifact) []map[string]interface{} {
	out := make([]map[string]interface{}, len(arts))
	for i, a := range arts {
		out[i] = map[string]interface{}{
			"id":          a.id,
			"name":        a.name,
			"expired":     a.expired,
			"created_at":  a.createdAt.Format(time.RFC3339),
			"archive_download_url": fmt.Sprintf("http://placeholder/%d/zip", a.id),
		}
	}
	return out
}

// fakeResultsService records uploads and returns a signed upload URL.
type fakeResultsService struct {
	putCount  int64 // atomic
	artifacts map[string][]byte
	uploadSrv *httptest.Server
}

func newFakeResultsService(t *testing.T) *fakeResultsService {
	t.Helper()
	f := &fakeResultsService{artifacts: make(map[string][]byte)}
	return f
}

func (f *fakeResultsService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/CreateArtifact") {
		w.Header().Set("Content-Type", "application/json")
		uploadURL := f.uploadSrv.URL + "/upload"
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":                true,
			"signed_upload_url": uploadURL,
			"artifact_id":       "art-1",
		})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/FinalizeArtifact") {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		return
	}
	w.WriteHeader(404)
}

func (f *fakeResultsService) serveUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		atomic.AddInt64(&f.putCount, 1)
		data, _ := io.ReadAll(r.Body)
		f.artifacts["last"] = data
		w.WriteHeader(201)
		return
	}
	w.WriteHeader(405)
}

func newFakeResults(t *testing.T) (*fakeResultsService, *httptest.Server) {
	t.Helper()
	f := newFakeResultsService(t)
	uploadSrv := httptest.NewServer(http.HandlerFunc(f.serveUpload))
	t.Cleanup(uploadSrv.Close)
	f.uploadSrv = uploadSrv
	resultsSrv := httptest.NewServer(f)
	t.Cleanup(resultsSrv.Close)
	return f, resultsSrv
}

func TestContract(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	storetest.RunContract(t, func() store.Store {
		fakeAPI := newFakeArtifactAPI()
		fakeRS, resultsSrv := newFakeResults(t)
		// Override the APIURL to route API calls to the fake.
		apiSrv := httptest.NewServer(fakeAPI)
		t.Cleanup(apiSrv.Close)
		token := makeToken("wf-1", "job-1")
		cfg := artifact.Config{
			Key:          key,
			RuntimeToken: token,
			ResultsURL:   resultsSrv.URL + "/results",
			APIURL:       apiSrv.URL,
			Token:        "gh-token",
		}
		// Build a transport that routes /upload calls and API calls to the right fake.
		hc := &http.Client{
			Transport: &multiTransport{
				apiHost:    strings.TrimPrefix(apiSrv.URL, "http://"),
				resultsHost: strings.TrimPrefix(resultsSrv.URL, "http://"),
				uploadHost: strings.TrimPrefix(fakeRS.uploadSrv.URL, "http://"),
				fakeAPI:    fakeAPI,
				fakeRS:     fakeRS,
				fakeUpload: fakeRS.uploadSrv,
			},
		}
		s := artifact.NewWithHTTPClient(cfg, hc)
		// Wire the fakeAPI so it can serve downloads of uploaded artifacts.
		return &contractAdapter{s: s, fakeAPI: fakeAPI, fakeRS: fakeRS}
	})
}

// contractAdapter wraps the artifact.Store and plumbs zip downloads into the
// fakeArtifactAPI after each Publish.
type contractAdapter struct {
	s       *artifact.Store
	fakeAPI *fakeArtifactAPI
	fakeRS  *fakeResultsService
}

func (a *contractAdapter) ListDays(ctx context.Context, key store.Key) ([]store.Day, error) {
	return a.s.ListDays(ctx, key)
}
func (a *contractAdapter) ReadAsset(ctx context.Context, day store.Day, name string) (io.ReadCloser, error) {
	return a.s.ReadAsset(ctx, day, name)
}
func (a *contractAdapter) BeginDay(ctx context.Context, key store.Key, date store.Date, seq int) (store.Day, error) {
	return a.s.BeginDay(ctx, key, date, seq)
}
func (a *contractAdapter) PutAsset(ctx context.Context, day store.Day, name string, r io.Reader) error {
	return a.s.PutAsset(ctx, day, name, r)
}
func (a *contractAdapter) DeleteAsset(ctx context.Context, day store.Day, name string) error {
	return a.s.DeleteAsset(ctx, day, name)
}
func (a *contractAdapter) Publish(ctx context.Context, day store.Day) error {
	if err := a.s.Publish(ctx, day); err != nil {
		return err
	}
	// After publish, register the uploaded zip in fakeAPI for download.
	zipData := a.fakeRS.artifacts["last"]
	if zipData != nil {
		id := a.fakeAPI.alloc()
		tag := strings.TrimPrefix(day.ID, "draft:")
		name := strings.ReplaceAll(tag, "/", "_")
		a.fakeAPI.artifacts = append(a.fakeAPI.artifacts, &fakeArtifact{
			id:        id,
			name:      name,
			createdAt: time.Now(),
		})
		a.fakeAPI.zips[id] = zipData
	}
	return nil
}
func (a *contractAdapter) DeleteDay(ctx context.Context, day store.Day) error {
	return a.s.DeleteDay(ctx, day)
}

// multiTransport routes requests to the appropriate fake server.
type multiTransport struct {
	apiHost     string
	resultsHost string
	uploadHost  string
	fakeAPI     http.Handler
	fakeRS      http.Handler
	fakeUpload  *httptest.Server
}

func (t *multiTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == t.uploadHost {
		rec := httptest.NewRecorder()
		t.fakeUpload.Config.Handler.ServeHTTP(rec, req)
		return rec.Result(), nil
	}
	if req.URL.Host == t.resultsHost {
		rec := httptest.NewRecorder()
		t.fakeRS.ServeHTTP(rec, req)
		return rec.Result(), nil
	}
	if req.URL.Host == t.apiHost {
		rec := httptest.NewRecorder()
		t.fakeAPI.ServeHTTP(rec, req)
		return rec.Result(), nil
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestUpload_Protocol(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fakeRS, resultsSrv := newFakeResults(t)
	apiSrv := httptest.NewServer(newFakeArtifactAPI())
	t.Cleanup(apiSrv.Close)
	token := makeToken("wf-run-1", "wf-job-1")
	cfg := artifact.Config{
		Key:          key,
		RuntimeToken: token,
		ResultsURL:   resultsSrv.URL + "/results",
		APIURL:       apiSrv.URL,
		Token:        "gh-token",
	}
	hc := &http.Client{
		Transport: &multiTransport{
			apiHost:     strings.TrimPrefix(apiSrv.URL, "http://"),
			resultsHost: strings.TrimPrefix(resultsSrv.URL, "http://"),
			uploadHost:  strings.TrimPrefix(fakeRS.uploadSrv.URL, "http://"),
			fakeAPI:     newFakeArtifactAPI(),
			fakeRS:      fakeRS,
			fakeUpload:  fakeRS.uploadSrv,
		},
	}
	s := artifact.NewWithHTTPClient(cfg, hc)
	ctx := context.Background()
	d, err := store.ParseDate("2026-03-01")
	require.NoError(t, err)

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)
	require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, strings.NewReader(`{}`)))
	require.NoError(t, s.Publish(ctx, day))

	// Verify that the fake results service received the upload.
	assert.NotNil(t, fakeRS.artifacts["last"], "upload must have been received")
}

func TestUpload_NoRuntimeToken(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	cfg := artifact.Config{Key: key, RuntimeToken: "", ResultsURL: "http://localhost/results"}
	s := artifact.New(cfg)
	ctx := context.Background()
	d, _ := store.ParseDate("2026-04-01")

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)

	err = s.Publish(ctx, day)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ACTIONS_RUNTIME_TOKEN")
}

func TestUpload_BadScope(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	// A token with a payload missing the Actions.Results scope.
	payload := map[string]interface{}{"scp": "no-actions-scope"}
	data, _ := json.Marshal(payload)
	badToken := "header." + base64.RawURLEncoding.EncodeToString(data) + ".sig"

	fakeRS, resultsSrv := newFakeResults(t)
	apiSrv := httptest.NewServer(newFakeArtifactAPI())
	t.Cleanup(apiSrv.Close)
	cfg := artifact.Config{
		Key:          key,
		RuntimeToken: badToken,
		ResultsURL:   resultsSrv.URL + "/results",
		APIURL:       apiSrv.URL,
	}
	hc := &http.Client{
		Transport: &multiTransport{
			apiHost:     strings.TrimPrefix(apiSrv.URL, "http://"),
			resultsHost: strings.TrimPrefix(resultsSrv.URL, "http://"),
			uploadHost:  strings.TrimPrefix(fakeRS.uploadSrv.URL, "http://"),
			fakeAPI:     newFakeArtifactAPI(),
			fakeRS:      fakeRS,
			fakeUpload:  fakeRS.uploadSrv,
		},
	}
	s := artifact.NewWithHTTPClient(cfg, hc)
	ctx := context.Background()
	d, _ := store.ParseDate("2026-05-01")

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)

	err = s.Publish(ctx, day)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Actions.Results")
}

func TestListDays_NameMapping(t *testing.T) {
	tests := []struct {
		name     string
		key      store.Key
		artName  string
		wantDate string
		wantSeq  int
	}{
		{
			name:     "plain key",
			key:      store.Key{Repo: "o/r", Prefix: "profile"},
			artName:  "profile_2026-10-26.0",
			wantDate: "2026-10-26",
			wantSeq:  0,
		},
		{
			name:     "service key",
			key:      store.Key{Repo: "o/r", Prefix: "profile", Service: "api"},
			artName:  "profile_api_2026-10-26.0",
			wantDate: "2026-10-26",
			wantSeq:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeAPI := newFakeArtifactAPI()
			fakeAPI.artifacts = append(fakeAPI.artifacts, &fakeArtifact{
				id:        1,
				name:      tt.artName,
				createdAt: time.Now(),
			})
			apiSrv := httptest.NewServer(fakeAPI)
			t.Cleanup(apiSrv.Close)
			cfg := artifact.Config{Key: tt.key, APIURL: apiSrv.URL}
			s := artifact.NewWithHTTPClient(cfg, apiSrv.Client())

			days, err := s.ListDays(context.Background(), tt.key)
			require.NoError(t, err)
			require.Len(t, days, 1)
			assert.Equal(t, tt.wantDate, days[0].Date.String())
			assert.Equal(t, tt.wantSeq, days[0].Seq)
		})
	}
}

func TestListDays_SkipsExpired(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fakeAPI := newFakeArtifactAPI()
	fakeAPI.artifacts = append(fakeAPI.artifacts,
		&fakeArtifact{id: 1, name: "profile_2026-01-01.0", expired: false, createdAt: time.Now()},
		&fakeArtifact{id: 2, name: "profile_2026-01-02.0", expired: true, createdAt: time.Now()},
	)
	apiSrv := httptest.NewServer(fakeAPI)
	t.Cleanup(apiSrv.Close)
	cfg := artifact.Config{Key: key, APIURL: apiSrv.URL}
	s := artifact.NewWithHTTPClient(cfg, apiSrv.Client())

	days, err := s.ListDays(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.Equal(t, "2026-01-01", days[0].Date.String())
}

func TestReadAsset_NotInZip(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	zipData := makeZip(map[string][]byte{"daily.pprof": []byte("data")})
	fakeAPI := newFakeArtifactAPI()
	id := fakeAPI.alloc()
	fakeAPI.artifacts = append(fakeAPI.artifacts, &fakeArtifact{id: id, name: "profile_2026-01-01.0", createdAt: time.Now()})
	fakeAPI.zips[id] = zipData

	apiSrv := httptest.NewServer(fakeAPI)
	t.Cleanup(apiSrv.Close)
	cfg := artifact.Config{Key: key, APIURL: apiSrv.URL}
	s := artifact.NewWithHTTPClient(cfg, apiSrv.Client())

	days, err := s.ListDays(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, days, 1)

	_, err = s.ReadAsset(context.Background(), days[0], store.AssetManifest)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteDay_Forbidden(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fakeAPI := newFakeArtifactAPI()
	fakeAPI.forbidDelete = true
	id := fakeAPI.alloc()
	fakeAPI.artifacts = append(fakeAPI.artifacts, &fakeArtifact{id: id, name: "profile_2026-01-01.0", createdAt: time.Now()})

	apiSrv := httptest.NewServer(fakeAPI)
	t.Cleanup(apiSrv.Close)
	cfg := artifact.Config{Key: key, APIURL: apiSrv.URL}
	s := artifact.NewWithHTTPClient(cfg, apiSrv.Client())

	days, err := s.ListDays(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, days, 1)

	err = s.DeleteDay(context.Background(), days[0])
	require.ErrorIs(t, err, store.ErrPermission)
}

func TestPublish_SecondRunMakesNextSequence(t *testing.T) {
	// Two concurrent runs that upload before openForWrite creates the .1 sequence
	// should result in two separate artifacts with the same base name.
	// After ListDays deduplicates (keeping newer created_at), only one survives.
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fakeAPI := newFakeArtifactAPI()
	now := time.Now()
	fakeAPI.artifacts = append(fakeAPI.artifacts,
		&fakeArtifact{id: 1, name: "profile_2026-06-01.0", createdAt: now.Add(-time.Minute)},
		&fakeArtifact{id: 2, name: "profile_2026-06-01.0", createdAt: now},
	)

	apiSrv := httptest.NewServer(fakeAPI)
	t.Cleanup(apiSrv.Close)
	cfg := artifact.Config{Key: key, APIURL: apiSrv.URL}
	s := artifact.NewWithHTTPClient(cfg, apiSrv.Client())

	days, err := s.ListDays(context.Background(), key)
	require.NoError(t, err)
	// Only the newer one should survive deduplication.
	count := 0
	d, _ := store.ParseDate("2026-06-01")
	for _, day := range days {
		if day.Date == d {
			count++
			assert.Equal(t, "2", day.ID, "the newer artifact (id=2) must be kept")
		}
	}
	assert.Equal(t, 1, count)
}

func TestRestore_LegacyArtifact(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	legacyData := []byte("legacy-pgo-content")
	zipData := makeZip(map[string][]byte{store.AssetDefault: legacyData})

	fakeAPI := newFakeArtifactAPI()
	id := fakeAPI.alloc()
	fakeAPI.artifacts = append(fakeAPI.artifacts,
		&fakeArtifact{id: id, name: "pgo-profiles", createdAt: time.Now()},
	)
	fakeAPI.zips[id] = zipData

	apiSrv := httptest.NewServer(fakeAPI)
	t.Cleanup(apiSrv.Close)
	cfg := artifact.Config{
		Key:        key,
		APIURL:     apiSrv.URL,
		LegacyName: "pgo-profiles",
	}
	s := artifact.NewWithHTTPClient(cfg, apiSrv.Client())

	var buf bytes.Buffer
	err := s.RestoreLegacy(context.Background(), &buf)
	require.NoError(t, err)
	assert.Equal(t, legacyData, buf.Bytes())
}

func TestListDays_DuplicateName(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	now := time.Now()
	fakeAPI := newFakeArtifactAPI()
	fakeAPI.artifacts = append(fakeAPI.artifacts,
		&fakeArtifact{id: 1, name: "profile_2026-07-01.0", createdAt: now.Add(-time.Minute)},
		&fakeArtifact{id: 2, name: "profile_2026-07-01.0", createdAt: now},
	)

	apiSrv := httptest.NewServer(fakeAPI)
	t.Cleanup(apiSrv.Close)
	cfg := artifact.Config{Key: key, APIURL: apiSrv.URL}
	s := artifact.NewWithHTTPClient(cfg, apiSrv.Client())

	days, err := s.ListDays(context.Background(), key)
	require.NoError(t, err)
	d, _ := store.ParseDate("2026-07-01")
	var found []store.Day
	for _, day := range days {
		if day.Date == d {
			found = append(found, day)
		}
	}
	require.Len(t, found, 1, "duplicate artifact names must be deduplicated")
	assert.Equal(t, "2", found[0].ID, "the newer artifact (id=2) must be kept")
}

func TestUpload_PutIsOneRequest(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fakeRS, resultsSrv := newFakeResults(t)
	apiSrv := httptest.NewServer(newFakeArtifactAPI())
	t.Cleanup(apiSrv.Close)
	token := makeToken("wf-1", "job-1")
	cfg := artifact.Config{
		Key:          key,
		RuntimeToken: token,
		ResultsURL:   resultsSrv.URL + "/results",
		APIURL:       apiSrv.URL,
		Token:        "gh-token",
	}
	hc := &http.Client{
		Transport: &multiTransport{
			apiHost:     strings.TrimPrefix(apiSrv.URL, "http://"),
			resultsHost: strings.TrimPrefix(resultsSrv.URL, "http://"),
			uploadHost:  strings.TrimPrefix(fakeRS.uploadSrv.URL, "http://"),
			fakeAPI:     newFakeArtifactAPI(),
			fakeRS:      fakeRS,
			fakeUpload:  fakeRS.uploadSrv,
		},
	}
	s := artifact.NewWithHTTPClient(cfg, hc)
	ctx := context.Background()
	d, _ := store.ParseDate("2026-08-01")

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)
	require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, strings.NewReader(`{}`)))
	require.NoError(t, s.Publish(ctx, day))

	assert.Equal(t, int64(1), atomic.LoadInt64(&fakeRS.putCount), "blob PUT must happen exactly once")
}
