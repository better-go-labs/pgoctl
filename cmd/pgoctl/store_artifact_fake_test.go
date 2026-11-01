package main

import (
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
)

// artFakeAPI is a minimal GitHub Actions Artifacts API fake for CLI-level tests.
type artFakeAPI struct {
	entries []artFakeEntry
	nextID  int64
	zips    map[int64][]byte
}

type artFakeEntry struct {
	id        int64
	name      string
	createdAt time.Time
}

func newArtFakeAPI() *artFakeAPI {
	return &artFakeAPI{zips: make(map[int64][]byte), nextID: 1}
}

func (f *artFakeAPI) alloc() int64 {
	id := f.nextID
	f.nextID++
	return id
}

func (f *artFakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if r.Method == http.MethodDelete && strings.Contains(path, "/actions/artifacts/") {
		w.WriteHeader(204)
		return
	}
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
	if r.Method == http.MethodGet && strings.Contains(path, "/actions/artifacts") {
		nameFilter := r.URL.Query().Get("name")
		var out []map[string]interface{}
		for _, a := range f.entries {
			if nameFilter == "" || a.name == nameFilter {
				out = append(out, map[string]interface{}{
					"id":                   a.id,
					"name":                 a.name,
					"expired":              false,
					"created_at":           a.createdAt.Format(time.RFC3339),
					"archive_download_url": fmt.Sprintf("http://placeholder/%d/zip", a.id),
				})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"total_count": len(out),
			"artifacts":   out,
		})
		return
	}
	w.WriteHeader(404)
}

// artFakeResults handles CreateArtifact/FinalizeArtifact Twirp calls.
type artFakeResults struct {
	last      []byte
	putCount  int64
	uploadSrv *httptest.Server
}

func (f *artFakeResults) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/CreateArtifact") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":                true,
			"signed_upload_url": f.uploadSrv.URL + "/upload",
			"artifact_id":       "art-1",
		})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/FinalizeArtifact") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		return
	}
	w.WriteHeader(404)
}

func (f *artFakeResults) serveUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		atomic.AddInt64(&f.putCount, 1)
		data, _ := io.ReadAll(r.Body)
		f.last = data
		w.WriteHeader(201)
		return
	}
	w.WriteHeader(405)
}

// artMultiTransport routes requests to the appropriate fake server by host.
type artMultiTransport struct {
	apiHost     string
	resultsHost string
	uploadHost  string
	fakeAPI     http.Handler
	fakeRS      http.Handler
	uploadSrv   *httptest.Server
}

func (t *artMultiTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == t.uploadHost {
		rec := httptest.NewRecorder()
		t.uploadSrv.Config.Handler.ServeHTTP(rec, req)
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

// artContractAdapter wraps *artifact.Store and registers uploaded zips in
// fakeAPI after each Publish, so ListDays can find them.
type artContractAdapter struct {
	s       *artifact.Store
	fakeAPI *artFakeAPI
	fakeRS  *artFakeResults
}

func (a *artContractAdapter) ListDays(ctx context.Context, key store.Key) ([]store.Day, error) {
	return a.s.ListDays(ctx, key)
}
func (a *artContractAdapter) ReadAsset(ctx context.Context, day store.Day, name string) (io.ReadCloser, error) {
	return a.s.ReadAsset(ctx, day, name)
}
func (a *artContractAdapter) BeginDay(ctx context.Context, key store.Key, date store.Date, seq int) (store.Day, error) {
	return a.s.BeginDay(ctx, key, date, seq)
}
func (a *artContractAdapter) PutAsset(ctx context.Context, day store.Day, name string, r io.Reader) error {
	return a.s.PutAsset(ctx, day, name, r)
}
func (a *artContractAdapter) DeleteAsset(ctx context.Context, day store.Day, name string) error {
	return a.s.DeleteAsset(ctx, day, name)
}
func (a *artContractAdapter) Publish(ctx context.Context, day store.Day) error {
	if err := a.s.Publish(ctx, day); err != nil {
		return err
	}
	zipData := a.fakeRS.last
	if zipData != nil {
		id := a.fakeAPI.alloc()
		tag := strings.TrimPrefix(day.ID, "draft:")
		name := strings.ReplaceAll(tag, "/", "_")
		a.fakeAPI.entries = append(a.fakeAPI.entries, artFakeEntry{
			id: id, name: name, createdAt: time.Now(),
		})
		a.fakeAPI.zips[id] = zipData
	}
	return nil
}
func (a *artContractAdapter) DeleteDay(ctx context.Context, day store.Day) error {
	return a.s.DeleteDay(ctx, day)
}

func artToken(wfRun, wfJob string) string {
	payload := map[string]interface{}{
		"scp": fmt.Sprintf("Actions.Results:%s:%s", wfRun, wfJob),
	}
	data, _ := json.Marshal(payload)
	encoded := base64.RawURLEncoding.EncodeToString(data)
	return "header." + encoded + ".sig"
}

// newArtifactFakeStore sets up a fully wired artifact store backed by fake HTTP
// servers. When wired is true the artContractAdapter is used, making published
// artifacts visible via ListDays.
func newArtifactFakeStore(t *testing.T, wired bool) store.Store {
	t.Helper()
	fakeAPI := newArtFakeAPI()
	apiSrv := httptest.NewServer(fakeAPI)
	t.Cleanup(apiSrv.Close)

	fakeRS := &artFakeResults{}
	uploadSrv := httptest.NewServer(http.HandlerFunc(fakeRS.serveUpload))
	t.Cleanup(uploadSrv.Close)
	fakeRS.uploadSrv = uploadSrv
	resultsSrv := httptest.NewServer(fakeRS)
	t.Cleanup(resultsSrv.Close)

	token := artToken("wf-1", "job-1")
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	cfg := artifact.Config{
		Key:          key,
		RuntimeToken: token,
		ResultsURL:   resultsSrv.URL + "/results",
		APIURL:       apiSrv.URL,
		Token:        "gh-token",
	}
	hc := &http.Client{
		Transport: &artMultiTransport{
			apiHost:     strings.TrimPrefix(apiSrv.URL, "http://"),
			resultsHost: strings.TrimPrefix(resultsSrv.URL, "http://"),
			uploadHost:  strings.TrimPrefix(uploadSrv.URL, "http://"),
			fakeAPI:     fakeAPI,
			fakeRS:      fakeRS,
			uploadSrv:   uploadSrv,
		},
	}
	s := artifact.NewWithHTTPClient(cfg, hc)
	if wired {
		return &artContractAdapter{s: s, fakeAPI: fakeAPI, fakeRS: fakeRS}
	}
	return s
}
