package ghrelease_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/ghrelease"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListDays_MalformedRunsAsset_NoPanic covers an asset named "runs-x" that is
// shorter than the ".pprof" suffix. wireToLogical must check the suffix before
// slicing, or ListDays panics.
func TestListDays_MalformedRunsAsset_NoPanic(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
