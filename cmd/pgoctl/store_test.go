package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withStore sets the global storeOverride for the duration of the test.
func withStore(t *testing.T, s store.Store) {
	t.Helper()
	storeOverride = s
	t.Cleanup(func() { storeOverride = nil })
}

// TestStoreResolve_OtherBranch checks that a non-publish branch outputs publish=false.
func TestStoreResolve_OtherBranch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"default_branch":"main","visibility":"private"}`))
	}))
	t.Cleanup(srv.Close)

	stdout, stderr, err := executeCmd(t,
		"store", "resolve",
		"--repo", "o/r",
		"--api-url", srv.URL,
		"--mode", "collect",
		"--branch", "feature/x",
		"--publish-branch", "main",
	)
	require.NoError(t, err)
	assert.Contains(t, stdout, "publish=false")
	assert.Contains(t, stderr, "::notice::")
}

// TestStoreResolve_DefaultBranch checks that the publish branch outputs publish=true.
func TestStoreResolve_DefaultBranch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"default_branch":"main","visibility":"private"}`))
	}))
	t.Cleanup(srv.Close)

	stdout, _, err := executeCmd(t,
		"store", "resolve",
		"--repo", "o/r",
		"--api-url", srv.URL,
		"--mode", "collect",
		"--branch", "main",
		"--publish-branch", "main",
	)
	require.NoError(t, err)
	assert.Contains(t, stdout, "publish=true")
}

// TestStoreResolve_PublicRepo checks that a public repo routes to artifact
// unless --allow-public-profiles is set.
func TestStoreResolve_PublicRepo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"default_branch":"main","visibility":"public"}`))
	}))
	t.Cleanup(srv.Close)

	stdout, stderr, err := executeCmd(t,
		"store", "resolve",
		"--repo", "o/r",
		"--api-url", srv.URL,
		"--mode", "collect",
		"--branch", "main",
		"--publish-branch", "main",
	)
	require.NoError(t, err)
	assert.Contains(t, stdout, "store=artifact")
	assert.Contains(t, stderr, "::warning::")
}

// TestStoreResolve_ArtifactStoreSkipsVisibility checks that --store artifact
// never calls the repo API.
func TestStoreResolve_ArtifactStoreSkipsVisibility(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(500)
	}))
	t.Cleanup(srv.Close)

	stdout, _, err := executeCmd(t,
		"store", "resolve",
		"--store", "artifact",
		"--repo", "o/r",
		"--api-url", srv.URL,
		"--mode", "collect",
		"--branch", "main",
		"--publish-branch", "main",
	)
	require.NoError(t, err)
	assert.False(t, called, "--store artifact must not call the GitHub API")
	assert.Contains(t, stdout, "store=artifact")
}

// TestStorePublishRestore_RoundTrip exercises publish then restore through the CLI.
func TestStorePublishRestore_RoundTrip(t *testing.T) {
	mem := storetest.NewMemory()
	withStore(t, mem)

	profile := storetest.NewProfile(t, 100)
	profileFile := writeTempFile(t, profile)
	outFile := t.TempDir() + "/default.pgo"

	// Publish.
	_, _, err := executeCmd(t,
		"store", "publish",
		"--repo", "o/r",
		"--profile", profileFile,
		"--run-id", "123-1",
		"--branch", "main",
		"--out", outFile,
	)
	require.NoError(t, err)

	// Restore.
	restoreOut := t.TempDir() + "/restored.pgo"
	_, _, err = executeCmd(t,
		"store", "restore",
		"--repo", "o/r",
		"--out", restoreOut,
	)
	require.NoError(t, err)
}

// TestStoreRestore_Empty checks that restore with no snapshot exits with code 3.
func TestStoreRestore_Empty(t *testing.T) {
	withStore(t, storetest.NewMemory())

	_, _, err := executeCmd(t,
		"store", "restore",
		"--repo", "o/r",
		"--out", t.TempDir()+"/out.pgo",
	)
	require.Error(t, err)
	code, ok := isExitError(err)
	require.True(t, ok)
	assert.Equal(t, 3, code)
}

// TestStorePublish_BadFlags checks that bad flags return exit code 2.
func TestStorePublish_BadFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "missing profile",
			args: []string{"store", "publish", "--repo", "o/r", "--run-id", "1-1"},
		},
		{
			name: "bad lookback",
			args: []string{"store", "publish", "--repo", "o/r", "--profile", "/dev/null", "--run-id", "1-1", "--lookback-days", "0"},
		},
		{
			name: "bad run-id",
			args: []string{"store", "publish", "--repo", "o/r", "--profile", "/dev/null", "--run-id", "notvalid"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withStore(t, storetest.NewMemory())
			_, _, err := executeCmd(t, tt.args...)
			require.Error(t, err)
			code, ok := isExitError(err)
			require.True(t, ok, "expected exitError, got %T: %v", err, err)
			assert.Equal(t, 2, code)
		})
	}
}

// TestStorePublish_Permission checks that ErrPermission exits with code 1.
func TestStorePublish_Permission(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible"}`))
	}))
	t.Cleanup(srv.Close)

	profile := storetest.NewProfile(t, 100)
	profileFile := writeTempFile(t, profile)

	_, _, err := executeCmd(t,
		"store", "publish",
		"--repo", "o/r",
		"--api-url", srv.URL,
		"--profile", profileFile,
		"--run-id", "1-1",
	)
	require.Error(t, err)
	code, ok := isExitError(err)
	require.True(t, ok)
	assert.Equal(t, 1, code)
}

// TestStorePublish_RuntimeErrorIsOne checks that generic runtime errors exit 1.
func TestStorePublish_RuntimeErrorIsOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"message":"Internal Server Error"}`))
	}))
	t.Cleanup(srv.Close)

	profile := storetest.NewProfile(t, 100)
	profileFile := writeTempFile(t, profile)

	_, _, err := executeCmd(t,
		"store", "publish",
		"--repo", "o/r",
		"--api-url", srv.URL,
		"--profile", profileFile,
		"--run-id", "1-1",
	)
	require.Error(t, err)
	code, ok := isExitError(err)
	require.True(t, ok)
	assert.Equal(t, 1, code)
}

// TestStoreExclude_Pairs checks --pairs flag parsing.
func TestStoreExclude_Pairs(t *testing.T) {
	mem := storetest.NewMemory()
	withStore(t, mem)

	profile := storetest.NewProfile(t, 100)
	profileFile := writeTempFile(t, profile)

	// Publish a day first.
	_, _, err := executeCmd(t,
		"store", "publish",
		"--repo", "o/r",
		"--profile", profileFile,
		"--run-id", "123-1",
		"--today", "2026-05-01",
		"--out", t.TempDir()+"/default.pgo",
	)
	require.NoError(t, err)

	// Exclude via --pairs.
	stdout, _, err := executeCmd(t,
		"store", "exclude",
		"--repo", "o/r",
		"--pairs", "2026-05-01:123-1",
		"--reason", "test",
	)
	require.NoError(t, err)
	assert.Contains(t, stdout, "123-1")
}

// TestStoreArtifact_PublishRestore exercises the artifact store through the CLI.
func TestStoreArtifact_PublishRestore(t *testing.T) {
	// Use a memory store configured like an artifact store (immutable + hide run assets).
	mem := storetest.NewMemory(storetest.HideRunAssets(), storetest.Immutable())
	withStore(t, mem)

	profile := storetest.NewProfile(t, 100)
	profileFile := writeTempFile(t, profile)
	outFile := t.TempDir() + "/default.pgo"

	// Publish via the CLI.
	_, _, err := executeCmd(t,
		"store", "publish",
		"--store", "artifact",
		"--repo", "o/r",
		"--profile", profileFile,
		"--run-id", "456-1",
		"--branch", "main",
		"--out", outFile,
	)
	require.NoError(t, err)

	// Restore via the CLI.
	restoreOut := t.TempDir() + "/restored.pgo"
	_, _, err = executeCmd(t,
		"store", "restore",
		"--store", "artifact",
		"--repo", "o/r",
		"--out", restoreOut,
	)
	require.NoError(t, err)
}

// TestStorePublish_JSON checks that --json writes valid JSON with tag and schema_version.
func TestStorePublish_JSON(t *testing.T) {
	mem := storetest.NewMemory()
	withStore(t, mem)

	profile := storetest.NewProfile(t, 100)
	profileFile := writeTempFile(t, profile)

	// Use executeCmd but capture stdout.
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{
		"--json",
		"store", "publish",
		"--repo", "o/r",
		"--profile", profileFile,
		"--run-id", "789-1",
		"--out", t.TempDir() + "/default.pgo",
	})
	err := root.Execute()
	require.NoError(t, err)

	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &m), "stdout: %q", stdout.String())
	assert.Contains(t, m, "tag", "JSON output must contain 'tag'")
	assert.EqualValues(t, 1, m["schema_version"], "JSON output must contain schema_version: 1")
}

// TestStoreRestore_LegacyArtifactCLI confirms exit 3 when no snapshot and no legacy artifact.
func TestStoreRestore_LegacyArtifactCLI(t *testing.T) {
	withStore(t, storetest.NewMemory())

	_, _, err := executeCmd(t,
		"store", "restore",
		"--store", "artifact",
		"--repo", "o/r",
		"--out", t.TempDir()+"/out.pgo",
	)
	require.Error(t, err)
	code, ok := isExitError(err)
	require.True(t, ok)
	assert.Equal(t, 3, code)
}

// TestStoreExclude_SkippedDayWritesNotice checks that excluding a missing day
// writes a ::notice:: and exits 0.
func TestStoreExclude_SkippedDayWritesNotice(t *testing.T) {
	withStore(t, storetest.NewMemory())

	stdout, _, err := executeCmd(t,
		"store", "exclude",
		"--repo", "o/r",
		"--date", "2026-09-01",
		"--run", "123-1",
	)
	require.NoError(t, err)
	assert.Contains(t, stdout, "::notice::")
}

// writeTempFile writes data to a temp file and returns its path.
func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "profile-*.pprof")
	require.NoError(t, err)
	_, err = f.Write(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

// Ensure unused imports are consumed.
var _ = io.Discard
