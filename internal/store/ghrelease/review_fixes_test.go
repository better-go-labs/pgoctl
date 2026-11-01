package ghrelease_test

import (
	"context"
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
