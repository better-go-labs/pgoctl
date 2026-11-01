package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	pprofprofile "github.com/google/pprof/profile"
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
	t.Setenv("GITHUB_TOKEN", "tok")
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
	t.Setenv("GITHUB_TOKEN", "tok")
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
	t.Setenv("GITHUB_TOKEN", "tok")
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

// TestStoreResolve_ArtifactPublishBranchFromDefault checks that --store artifact
// with an empty --publish-branch resolves the repo default branch, the same way
// Releases does, so publish is decided correctly.
func TestStoreResolve_ArtifactPublishBranchFromDefault(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "tok")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"default_branch":"main","visibility":"public"}`))
	}))
	t.Cleanup(srv.Close)

	stdout, _, err := executeCmd(t,
		"store", "resolve",
		"--store", "artifact",
		"--repo", "o/r",
		"--api-url", srv.URL,
		"--mode", "collect",
		"--branch", "main",
		// no --publish-branch: must resolve to the repo default (main).
	)
	require.NoError(t, err)
	assert.Contains(t, stdout, "store=artifact")
	assert.Contains(t, stdout, "publish=true")
}

// TestStorePublish_Defaults checks that publish fills CapturedAt from the
// profile, computes --samples when 0, defaults --go-version to runtime.Version(),
// and defaults --run-id/--commit/--branch from the GITHUB_* env.
func TestStorePublish_Defaults(t *testing.T) {
	mem := storetest.NewMemory()
	withStore(t, mem)

	t.Setenv("GITHUB_RUN_ID", "9900")
	t.Setenv("GITHUB_RUN_ATTEMPT", "2")
	t.Setenv("GITHUB_SHA", "deadbeefcafe")
	t.Setenv("GITHUB_REF_NAME", "main")

	captured := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	profileFile := writeTempFile(t, newProfileWithCount(t, 777, captured))

	// No --run-id, --commit, --branch, --samples, or --go-version.
	stdout, _, err := executeCmd(t,
		"--json",
		"store", "publish",
		"--repo", "o/r",
		"--profile", profileFile,
		"--today", "2026-05-01",
		"--out", t.TempDir()+"/default.pgo",
	)
	require.NoError(t, err)

	var m map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &m), "stdout: %q", stdout)

	assert.Equal(t, "9900-2", m["daily_run"], "run id must default from GITHUB_RUN_ID-GITHUB_RUN_ATTEMPT")
	assert.Equal(t, "main", m["branch"], "branch must default from GITHUB_REF_NAME")
	assert.Equal(t, runtime.Version(), m["go_version"], "go_version must default to runtime.Version()")

	runs, ok := m["runs"].([]interface{})
	require.True(t, ok && len(runs) == 1, "expected one run, got %v", m["runs"])
	run := runs[0].(map[string]interface{})
	assert.Equal(t, "9900-2", run["id"])
	assert.Equal(t, "deadbeefcafe", run["commit"], "commit must default from GITHUB_SHA")
	assert.EqualValues(t, 777, run["samples"], "samples must be computed from the profile when 0")
	assert.Equal(t, "2026-05-01T12:00:00Z", run["captured_at"], "captured_at must come from the profile TimeNanos")
}

// newProfileWithCount builds a pprof profile whose samples/count total is count
// and whose TimeNanos is at, so publish-default derivation can be asserted.
func newProfileWithCount(t *testing.T, count int64, at time.Time) []byte {
	t.Helper()
	fn := &pprofprofile.Function{ID: 1, Name: "test.work", Filename: "work.go"}
	loc := &pprofprofile.Location{ID: 1, Line: []pprofprofile.Line{{Function: fn, Line: 1}}}
	p := &pprofprofile.Profile{
		TimeNanos: at.UnixNano(),
		SampleType: []*pprofprofile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		Sample: []*pprofprofile.Sample{
			{Value: []int64{count, count * 1000}, Location: []*pprofprofile.Location{loc}},
		},
		Function: []*pprofprofile.Function{fn},
		Location: []*pprofprofile.Location{loc},
	}
	var buf bytes.Buffer
	require.NoError(t, p.Write(&buf))
	return buf.Bytes()
}

// TestStoreExclude_ActorFromEnv checks that --actor defaults from GITHUB_ACTOR.
func TestStoreExclude_ActorFromEnv(t *testing.T) {
	mem := storetest.NewMemory()
	withStore(t, mem)
	t.Setenv("GITHUB_ACTOR", "octocat")

	profileFile := writeTempFile(t, storetest.NewProfile(t, 100))
	// Two runs on one day so a default survives after excluding one.
	for _, id := range []string{"100-1", "200-1"} {
		_, _, err := executeCmd(t,
			"store", "publish",
			"--repo", "o/r",
			"--profile", profileFile,
			"--run-id", id,
			"--today", "2026-05-01",
			"--out", t.TempDir()+"/default.pgo",
		)
		require.NoError(t, err)
	}

	// Exclude without --actor; it must default from GITHUB_ACTOR.
	_, _, err := executeCmd(t,
		"store", "exclude",
		"--repo", "o/r",
		"--date", "2026-05-01",
		"--run", "100-1",
		"--reason", "noisy",
	)
	require.NoError(t, err)

	stdout, _, err := executeCmd(t,
		"--json",
		"store", "restore",
		"--repo", "o/r",
		"--out", t.TempDir()+"/restored.pgo",
	)
	require.NoError(t, err)

	var m map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &m), "stdout: %q", stdout)
	excls, ok := m["exclusions"].([]interface{})
	require.True(t, ok && len(excls) >= 1, "expected an exclusion, got %v", m["exclusions"])
	assert.Equal(t, "octocat", excls[0].(map[string]interface{})["by"], "exclusion actor must default from GITHUB_ACTOR")
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
	// A token is required to build the store; the 403 we exercise here is the
	// permission error, which must exit 1.
	t.Setenv("GITHUB_TOKEN", "tok")
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
	// A token is required to build the store; the runtime error we exercise
	// here is the server 500, which must exit 1.
	t.Setenv("GITHUB_TOKEN", "tok")
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

// TestStoreArtifact_PublishRestore exercises the artifact store through the CLI
// against a real fake-HTTP artifact backend. It publishes two runs on the same
// day (.0 then .1), restores and checks content byte-for-byte, and exercises
// the exclude path.
func TestStoreArtifact_PublishRestore(t *testing.T) {
	// RED: without the wired adapter, published artifacts are not visible in
	// ListDays, so the second publish cannot see .0 as immutable and restore
	// returns ErrNoSnapshot.
	s := newArtifactFakeStore(t, false /* not wired */)
	withStore(t, s)

	profile0 := storetest.NewProfile(t, 100)
	profile1 := storetest.NewProfile(t, 200)
	profileFile0 := writeTempFile(t, profile0)
	profileFile1 := writeTempFile(t, profile1)
	outFile := t.TempDir() + "/default.pgo"

	// First publish on 2026-10-01 — creates sequence .0.
	_, _, err := executeCmd(t,
		"store", "publish",
		"--store", "artifact",
		"--repo", "o/r",
		"--today", "2026-10-01",
		"--profile", profileFile0,
		"--run-id", "111-1",
		"--out", outFile,
	)
	require.NoError(t, err, "first publish must succeed")

	// Second publish on the same date — with a wired adapter .0 is immutable
	// so this opens .1; without adapter both publishes land as .0.
	_, _, err = executeCmd(t,
		"store", "publish",
		"--store", "artifact",
		"--repo", "o/r",
		"--today", "2026-10-01",
		"--profile", profileFile1,
		"--run-id", "111-2",
		"--out", outFile,
	)
	require.NoError(t, err, "second publish must succeed")

	// Capture what was written to the default.pgo output; restore must match it.
	wantDefault, err := os.ReadFile(outFile)
	require.NoError(t, err)

	// Restore — must succeed and match the published default.pgo byte-for-byte.
	restoreOut := t.TempDir() + "/restored.pgo"
	_, _, err = executeCmd(t,
		"store", "restore",
		"--store", "artifact",
		"--repo", "o/r",
		"--out", restoreOut,
	)
	require.NoError(t, err, "restore must succeed after two publishes")

	got, err := os.ReadFile(restoreOut)
	require.NoError(t, err)
	assert.Equal(t, wantDefault, got, "restored bytes must match the published default.pgo")

	// Exclude exercises the exclude CLI path against the artifact backend.
	_, _, err = executeCmd(t,
		"store", "exclude",
		"--store", "artifact",
		"--repo", "o/r",
		"--date", "2026-10-01",
		"--run", "999-1",
	)
	require.NoError(t, err, "exclude must succeed")
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
