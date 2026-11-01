package artifact_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
		if _, scanErr := fmt.Sscanf(idStr, "%d", &id); scanErr != nil {
			http.Error(w, "bad artifact id", http.StatusBadRequest)
			return
		}
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
		if _, scanErr := fmt.Sscanf(idStr, "%d", &id); scanErr != nil {
			http.Error(w, "bad artifact id", http.StatusBadRequest)
			return
		}
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
			"id":                   a.id,
			"name":                 a.name,
			"expired":              a.expired,
			"created_at":           a.createdAt.Format(time.RFC3339),
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
				apiHost:     strings.TrimPrefix(apiSrv.URL, "http://"),
				resultsHost: strings.TrimPrefix(resultsSrv.URL, "http://"),
				uploadHost:  strings.TrimPrefix(fakeRS.uploadSrv.URL, "http://"),
				fakeAPI:     fakeAPI,
				fakeRS:      fakeRS,
				fakeUpload:  fakeRS.uploadSrv,
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

// TestPutAsset_RunsSubdirCreated verifies that PutAsset succeeds for asset
// names that include a subdirectory such as "runs/<id>.pprof", even though
// the freshly-created staging directory contains no pre-existing subdirs.
func TestPutAsset_RunsSubdirCreated(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	fakeAPI := newFakeArtifactAPI()
	apiSrv := httptest.NewServer(fakeAPI)
	t.Cleanup(apiSrv.Close)
	cfg := artifact.Config{Key: key, APIURL: apiSrv.URL}
	s := artifact.NewWithHTTPClient(cfg, apiSrv.Client())

	d, _ := store.ParseDate("2026-10-01")
	day, err := s.BeginDay(context.Background(), key, d, 0)
	require.NoError(t, err)

	err = s.PutAsset(context.Background(), day, "runs/abc123.pprof", strings.NewReader("pprof-data"))
	require.NoError(t, err, "PutAsset must create the runs/ subdir before writing")
}

func sha256HexStr(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// TestRestore_PublishedArtifactAssets verifies that store.Restore can use a
// published artifact day. Before the fix, ListDays returned an empty Assets
// map for published days, causing usable() to return false so Restore always
// returned ErrNoSnapshot.
func TestRestore_PublishedArtifactAssets(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}

	pgoData := []byte("fake-pgo-data")
	pgoSum := sha256HexStr(pgoData)

	mf := store.Manifest{
		SchemaVersion: store.SchemaVersion,
		Date:          "2026-10-01",
		Tag:           "profile/2026-10-01.0",
		Assets:        map[string]string{store.AssetDefault: pgoSum},
	}
	manifestData, _ := json.Marshal(mf)

	zipData := makeZip(map[string][]byte{
		store.AssetDefault:  pgoData,
		store.AssetManifest: manifestData,
	})

	fakeAPI := newFakeArtifactAPI()
	id := fakeAPI.alloc()
	fakeAPI.artifacts = append(fakeAPI.artifacts, &fakeArtifact{
		id: id, name: "profile_2026-10-01.0", createdAt: time.Now(),
	})
	fakeAPI.zips[id] = zipData

	apiSrv := httptest.NewServer(fakeAPI)
	t.Cleanup(apiSrv.Close)
	cfg := artifact.Config{Key: key, APIURL: apiSrv.URL}
	s := artifact.NewWithHTTPClient(cfg, apiSrv.Client())

	var buf bytes.Buffer
	_, err := store.Restore(context.Background(), s, key, &buf)
	require.NoError(t, err, "Restore must succeed when a published artifact day exists")
	assert.Equal(t, pgoData, buf.Bytes())
}

// TestListArtifacts_403_IsErrPermission checks that a 403 from the list endpoint
// wraps ErrPermission.
func TestListArtifacts_403_IsErrPermission(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	s := artifact.NewWithHTTPClient(artifact.Config{Key: key, APIURL: srv.URL}, srv.Client())
	_, err := s.ListDays(context.Background(), key)
	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrPermission), "403 on list must wrap ErrPermission, got: %v", err)
}

// TestListArtifacts_404_IsErrNotFound checks that a 404 from the list endpoint
// wraps ErrNotFound.
func TestListArtifacts_404_IsErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	s := artifact.NewWithHTTPClient(artifact.Config{Key: key, APIURL: srv.URL}, srv.Client())
	_, err := s.ListDays(context.Background(), key)
	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrNotFound), "404 on list must wrap ErrNotFound, got: %v", err)
}

// TestDownloadArtifact_404_IsErrNotFound checks that a 404 from the zip download
// endpoint wraps ErrNotFound when ReadAsset is called on a published day.
func TestDownloadArtifact_404_IsErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/actions/artifacts") && r.Method == http.MethodGet && !strings.HasSuffix(r.URL.Path, "/zip") {
			// List returns one artifact.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"total_count": 1,
				"artifacts": []map[string]interface{}{{
					"id":                   42,
					"name":                 "profile_2026-01-01.0",
					"expired":              false,
					"created_at":           time.Now().Format(time.RFC3339),
					"archive_download_url": "http://placeholder/42/zip",
				}},
			})
			return
		}
		// Zip download returns 404.
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	s := artifact.NewWithHTTPClient(artifact.Config{Key: key, APIURL: srv.URL}, srv.Client())
	d, _ := store.ParseDate("2026-01-01")
	day := store.Day{Key: key, Date: d, Seq: 0, ID: "42"}
	_, err := s.ReadAsset(context.Background(), day, "default.pgo")
	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrNotFound), "404 on zip download must wrap ErrNotFound, got: %v", err)
}

// TestDeleteDay_404_IsErrNotFound checks that a 404 from the delete endpoint
// wraps ErrNotFound.
func TestDeleteDay_404_IsErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	s := artifact.NewWithHTTPClient(artifact.Config{Key: key, APIURL: srv.URL}, srv.Client())
	d, _ := store.ParseDate("2026-01-01")
	day := store.Day{Key: key, Date: d, Seq: 0, ID: "99"}
	err := s.DeleteDay(context.Background(), day)
	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrNotFound), "404 on delete must wrap ErrNotFound, got: %v", err)
}

// TestRestoreLegacy_DecompressionLimitExceeded verifies that RestoreLegacy
// rejects a zip entry whose uncompressed size exceeds the per-entry limit
// (G110: decompression bomb guard).
func TestRestoreLegacy_DecompressionLimitExceeded(t *testing.T) {
	// Lower the limit so the test does not allocate 512 MiB.
	orig := *artifact.MaxZipEntrySize
	*artifact.MaxZipEntrySize = 4096
	t.Cleanup(func() { *artifact.MaxZipEntrySize = orig })

	// Build a zip where default.pgo is twice the test limit.
	oversized := bytes.Repeat([]byte("X"), 8192)
	zipData := makeZip(map[string][]byte{store.AssetDefault: oversized})

	fakeAPI := newFakeArtifactAPI()
	id := fakeAPI.alloc()
	fakeAPI.artifacts = append(fakeAPI.artifacts,
		&fakeArtifact{id: id, name: "old-profiles", createdAt: time.Now()},
	)
	fakeAPI.zips[id] = zipData

	apiSrv := httptest.NewServer(fakeAPI)
	t.Cleanup(apiSrv.Close)
	cfg := artifact.Config{
		Key:        store.Key{Repo: "o/r", Prefix: "profile"},
		APIURL:     apiSrv.URL,
		LegacyName: "old-profiles",
	}
	s := artifact.NewWithHTTPClient(cfg, apiSrv.Client())

	var buf bytes.Buffer
	err := s.RestoreLegacy(context.Background(), &buf)
	require.Error(t, err, "oversized entry must be rejected as a possible decompression bomb")
	assert.ErrorContains(t, err, "decompression limit")
}

// makeZip64WithHugeSize builds a minimal ZIP64 archive containing one file
// named name with a declared (but not actual) uncompressed size of hugeSize.
// The actual file data is empty; only the central-directory metadata is
// populated with the large value. This is used to test G115 overflow handling
// without allocating huge buffers.
func makeZip64WithHugeSize(name string, hugeSize uint64) ([]byte, error) {
	fname := []byte(name)
	if len(fname) > math.MaxUint16 {
		return nil, fmt.Errorf("name length %d exceeds ZIP uint16 limit", len(fname))
	}
	fnlen := uint16(len(fname)) //nolint:gosec // G115: checked above

	makeLocalExtra := func(uncomp, comp uint64) []byte {
		b := make([]byte, 20)
		binary.LittleEndian.PutUint16(b[0:], 0x0001)
		binary.LittleEndian.PutUint16(b[2:], 16)
		binary.LittleEndian.PutUint64(b[4:], uncomp)
		binary.LittleEndian.PutUint64(b[12:], comp)
		return b
	}
	makeCDExtra := func(uncomp, comp, offset uint64) []byte {
		b := make([]byte, 28)
		binary.LittleEndian.PutUint16(b[0:], 0x0001)
		binary.LittleEndian.PutUint16(b[2:], 24)
		binary.LittleEndian.PutUint64(b[4:], uncomp)
		binary.LittleEndian.PutUint64(b[12:], comp)
		binary.LittleEndian.PutUint64(b[20:], offset)
		return b
	}

	var buf bytes.Buffer

	localExtra := makeLocalExtra(hugeSize, 0)
	var lfh [30]byte
	binary.LittleEndian.PutUint32(lfh[0:], 0x04034b50)  // local file header sig
	binary.LittleEndian.PutUint16(lfh[4:], 45)          // version needed: 4.5 (ZIP64)
	binary.LittleEndian.PutUint32(lfh[18:], 0xFFFFFFFF) // compressed size: ZIP64 marker
	binary.LittleEndian.PutUint32(lfh[22:], 0xFFFFFFFF) // uncompressed size: ZIP64 marker
	binary.LittleEndian.PutUint16(lfh[26:], fnlen)
	binary.LittleEndian.PutUint16(lfh[28:], uint16(len(localExtra))) //nolint:gosec // G115: fixed-size struct, len always fits uint16
	buf.Write(lfh[:])
	buf.Write(fname)
	buf.Write(localExtra)

	cdStart := uint64(buf.Len()) //nolint:gosec // G115: buf.Len() is non-negative; test zip is tiny

	cdExtra := makeCDExtra(hugeSize, 0, 0)
	var cdh [46]byte
	binary.LittleEndian.PutUint32(cdh[0:], 0x02014b50) // central directory header sig
	binary.LittleEndian.PutUint16(cdh[4:], 45)
	binary.LittleEndian.PutUint16(cdh[6:], 45)
	binary.LittleEndian.PutUint32(cdh[20:], 0xFFFFFFFF) // compressed size: ZIP64 marker
	binary.LittleEndian.PutUint32(cdh[24:], 0xFFFFFFFF) // uncompressed size: ZIP64 marker
	binary.LittleEndian.PutUint16(cdh[28:], fnlen)
	binary.LittleEndian.PutUint16(cdh[30:], uint16(len(cdExtra))) //nolint:gosec // G115: fixed-size struct, len always fits uint16
	binary.LittleEndian.PutUint16(cdh[34:], 0xFFFF)               // disk number start: ZIP64 marker
	binary.LittleEndian.PutUint32(cdh[42:], 0xFFFFFFFF)           // local header offset: ZIP64 marker
	buf.Write(cdh[:])
	buf.Write(fname)
	buf.Write(cdExtra)

	cdEnd := uint64(buf.Len()) //nolint:gosec // G115: buf.Len() is non-negative; test zip is tiny
	cdSize := cdEnd - cdStart
	eocd64Offset := uint64(buf.Len()) //nolint:gosec // G115: buf.Len() is non-negative; test zip is tiny

	var eocd64 [56]byte
	binary.LittleEndian.PutUint32(eocd64[0:], 0x06064b50)
	binary.LittleEndian.PutUint64(eocd64[4:], 44) // size of remaining record
	binary.LittleEndian.PutUint16(eocd64[12:], 45)
	binary.LittleEndian.PutUint16(eocd64[14:], 45)
	binary.LittleEndian.PutUint64(eocd64[24:], 1)
	binary.LittleEndian.PutUint64(eocd64[32:], 1)
	binary.LittleEndian.PutUint64(eocd64[40:], cdSize)
	binary.LittleEndian.PutUint64(eocd64[48:], cdStart)
	buf.Write(eocd64[:])

	var locator [20]byte
	binary.LittleEndian.PutUint32(locator[0:], 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:], eocd64Offset)
	binary.LittleEndian.PutUint32(locator[16:], 1)
	buf.Write(locator[:])

	var eocd [22]byte
	binary.LittleEndian.PutUint32(eocd[0:], 0x06054b50)
	binary.LittleEndian.PutUint16(eocd[4:], 0xFFFF)
	binary.LittleEndian.PutUint16(eocd[6:], 0xFFFF)
	binary.LittleEndian.PutUint16(eocd[8:], 0xFFFF)
	binary.LittleEndian.PutUint16(eocd[10:], 0xFFFF)
	binary.LittleEndian.PutUint32(eocd[12:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(eocd[16:], 0xFFFFFFFF)
	buf.Write(eocd[:])

	return buf.Bytes(), nil
}

// TestListDays_UncompressedSizeOverflow verifies that a zip entry whose
// declared uncompressed size overflows int64 does not produce a negative
// asset size (G115: integer overflow guard).
func TestListDays_UncompressedSizeOverflow(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}

	overflowSize := uint64(math.MaxInt64) + 1
	zipData, err := makeZip64WithHugeSize("daily.pprof", overflowSize)
	require.NoError(t, err, "test helper must build valid ZIP64 archive")

	fakeAPI := newFakeArtifactAPI()
	id := fakeAPI.alloc()
	fakeAPI.artifacts = append(fakeAPI.artifacts,
		&fakeArtifact{id: id, name: "profile_2026-01-01.0", createdAt: time.Now()},
	)
	fakeAPI.zips[id] = zipData

	apiSrv := httptest.NewServer(fakeAPI)
	t.Cleanup(apiSrv.Close)

	s := artifact.NewWithHTTPClient(artifact.Config{Key: key, APIURL: apiSrv.URL}, apiSrv.Client())
	days, listErr := s.ListDays(context.Background(), key)
	require.NoError(t, listErr)
	require.Len(t, days, 1)
	for _, a := range days[0].Assets {
		assert.GreaterOrEqual(t, a.Size, int64(0),
			"asset size from an overflowed uint64 must not be negative")
	}
}

// TestListDays_LinkHeaderPagination verifies that artifact pagination uses the
// absolute URL from the Link header's "next" relation without prepending APIURL.
// Regression test for the doubled-host bug that broke restore on repos with >100 artifacts.
func TestListDays_LinkHeaderPagination(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	var requestedURLs []string
	var requestMu sync.Mutex

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMu.Lock()
		requestedURLs = append(requestedURLs, r.URL.String())
		count := len(requestedURLs)
		requestMu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		switch count {
		case 1:
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/repos/o/r/actions/artifacts?per_page=100&page=2>; rel="next"`, r.Host))
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"total_count": 2,
				"artifacts": []map[string]interface{}{
					{
						"id":                   1,
						"name":                 "profile_2026-01-01.0",
						"expired":              false,
						"created_at":           time.Now().Format(time.RFC3339),
						"archive_download_url": "http://placeholder/1/zip",
					},
				},
			})
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"total_count": 2,
				"artifacts": []map[string]interface{}{
					{
						"id":                   2,
						"name":                 "profile_2026-01-02.0",
						"expired":              false,
						"created_at":           time.Now().Format(time.RFC3339),
						"archive_download_url": "http://placeholder/2/zip",
					},
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"total_count": 2,
				"artifacts":   []map[string]interface{}{},
			})
		}
	}))
	srvURL = srv.URL
	t.Cleanup(srv.Close)

	s := artifact.NewWithHTTPClient(artifact.Config{Key: key, APIURL: srvURL}, srv.Client())
	days, err := s.ListDays(context.Background(), key)
	require.NoError(t, err)
	assert.Len(t, days, 2)

	requestMu.Lock()
	defer requestMu.Unlock()

	var listRequests []string
	for _, u := range requestedURLs {
		if !strings.Contains(u, "/zip") {
			listRequests = append(listRequests, u)
		}
	}
	require.Len(t, listRequests, 2, "must make exactly 2 paginated list requests")

	page1URL := listRequests[0]
	page2URL := listRequests[1]
	assert.NotContains(t, page1URL, "page=2", "page 1 must not request page 2")
	assert.Contains(t, page2URL, "page=2", "page 2 must contain page=2 param")
	doubledHost := srvURL + srvURL
	assert.NotContains(t, page2URL, doubledHost, "page 2 URL must not have doubled host")
}

// TestPublish_RunsSubdirInZip verifies that published artifacts include files from
// subdirectories like runs/. Before the fix, zipDir only included files directly in
// the staging directory and skipped subdirectories.
func TestPublish_RunsSubdirInZip(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	payload := []byte("pprof-data-in-subdir")
	payloadSum := sha256HexStr(payload)

	fakeRS, resultsSrv := newFakeResults(t)
	fakeAPI := newFakeArtifactAPI()
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
	hc := &http.Client{
		Transport: &multiTransport{
			apiHost:     strings.TrimPrefix(apiSrv.URL, "http://"),
			resultsHost: strings.TrimPrefix(resultsSrv.URL, "http://"),
			uploadHost:  strings.TrimPrefix(fakeRS.uploadSrv.URL, "http://"),
			fakeAPI:     fakeAPI,
			fakeRS:      fakeRS,
			fakeUpload:  fakeRS.uploadSrv,
		},
	}
	s := artifact.NewWithHTTPClient(cfg, hc)
	ctx := context.Background()
	d, _ := store.ParseDate("2026-08-01")

	day, err := s.BeginDay(ctx, key, d, 0)
	require.NoError(t, err)
	require.NoError(t, s.PutAsset(ctx, day, "runs/1-1.pprof", strings.NewReader("pprof-data-in-subdir")))
	mf := store.Manifest{
		SchemaVersion: store.SchemaVersion,
		Date:          "2026-08-01",
		Tag:           "profile/2026-08-01.0",
		Assets:        map[string]string{"runs/1-1.pprof": payloadSum},
	}
	manifestData, _ := json.Marshal(mf)
	require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, strings.NewReader(string(manifestData))))
	require.NoError(t, s.Publish(ctx, day))

	// After publish, register the uploaded zip in fakeAPI for download.
	zipData := fakeRS.artifacts["last"]
	require.NotNil(t, zipData, "artifact must have been published")
	id := fakeAPI.alloc()
	fakeAPI.artifacts = append(fakeAPI.artifacts, &fakeArtifact{
		id: id, name: "profile_2026-08-01.0", createdAt: time.Now(),
	})
	fakeAPI.zips[id] = zipData

	days, err := s.ListDays(ctx, key)
	require.NoError(t, err)
	require.Len(t, days, 1)

	published := days[0]
	rc, err := s.ReadAsset(ctx, published, "runs/1-1.pprof")
	require.NoError(t, err)
	defer func() { require.NoError(t, rc.Close()) }()

	data, _ := io.ReadAll(rc)
	assert.Equal(t, payload, data, "reads runs subdir file from published artifact")
}
