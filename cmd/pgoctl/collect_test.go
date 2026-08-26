package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCollect_PprofSource_Success starts a mock pprof HTTP server and verifies
// collect writes a non-empty file to disk.
func TestCollect_PprofSource_Success(t *testing.T) {
	fakeProfile := []byte("FAKE_PPROF_DATA")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fakeProfile)
	}))
	defer srv.Close()

	outFile := filepath.Join(t.TempDir(), "cpu.pprof")
	_, _, err := executeCmd(t,
		"collect",
		"--source=pprof",
		"--url="+srv.URL+"/debug/pprof/profile",
		"--window=100ms",
		"--timeout=5s",
		"--out="+outFile,
	)
	require.NoError(t, err)
	data, readErr := os.ReadFile(outFile)
	require.NoError(t, readErr)
	assert.Equal(t, fakeProfile, data)
}

// TestCollect_PprofSource_ServerError verifies that a 500 from the pprof endpoint returns an error.
func TestCollect_PprofSource_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	outFile := filepath.Join(t.TempDir(), "cpu.pprof")
	_, _, err := executeCmd(t,
		"collect",
		"--source=pprof",
		"--url="+srv.URL+"/debug/pprof/profile",
		"--timeout=5s",
		"--out="+outFile,
	)
	assert.Error(t, err)
}

// TestCollect_ParcaSource_Success starts a mock Parca REST server and verifies
// collect writes a non-empty file to disk.
func TestCollect_ParcaSource_Success(t *testing.T) {
	fakeProfile := []byte("FAKE_PPROF_DATA")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "MODE_MERGE", r.URL.Query().Get("mode"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"pprof": base64.StdEncoding.EncodeToString(fakeProfile),
		})
	}))
	defer srv.Close()

	outFile := filepath.Join(t.TempDir(), "cpu.pprof")
	_, _, err := executeCmd(t,
		"collect",
		"--source=parca",
		"--parca-addr="+srv.URL,
		"--query=process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		"--window="+time.Minute.String(),
		"--timeout=5s",
		"--out="+outFile,
	)
	require.NoError(t, err)
	data, readErr := os.ReadFile(outFile)
	require.NoError(t, readErr)
	assert.Equal(t, fakeProfile, data)
}

// TestCollect_MissingSource verifies that omitting --source returns an error.
func TestCollect_MissingSource(t *testing.T) {
	_, _, err := executeCmd(t, "collect", "--out=/tmp/nope.pprof")
	assert.Error(t, err)
}

// TestCollect_UnsupportedSource verifies an unsupported source value returns an error.
func TestCollect_UnsupportedSource(t *testing.T) {
	_, _, err := executeCmd(t, "collect", "--source=unknown", "--out=/tmp/nope.pprof")
	assert.Error(t, err)
}

// TestCollect_PprofMissingURL verifies --source=pprof without --url returns an error.
func TestCollect_PprofMissingURL(t *testing.T) {
	_, _, err := executeCmd(t, "collect", "--source=pprof", "--out=/tmp/nope.pprof")
	assert.Error(t, err)
}
