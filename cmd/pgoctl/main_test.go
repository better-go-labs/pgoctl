package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// executeCmd runs pgoctl in-process and returns (stdout, stderr, error).
// Note: validate/merge/compare write directly to os.Stdout/os.Stderr, so
// those streams are not captured here — assert on err and exitError.code instead.
func executeCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

// generateProfile produces a real CPU profile via runtime/pprof.
func generateProfile(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "cpu*.pprof")
	require.NoError(t, err)
	require.NoError(t, pprof.StartCPUProfile(f))
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		// spin to gather samples
	}
	pprof.StopCPUProfile()
	require.NoError(t, f.Close())
	return f.Name()
}

// TestValidate_ValidProfile passes a real profile with permissive thresholds.
func TestValidate_ValidProfile(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	assert.NoError(t, err)
}

// TestValidate_JSONFlag covers the JSON branch of printQualityReport.
// Output goes to os.Stdout (not captured), so we only check for no error.
func TestValidate_JSONFlag(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"--json", "validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	assert.NoError(t, err)
}

// TestValidate_GateFailViaEnv sets PGOCTL_MIN_SAMPLES=999999 to force gate failure → exitError{1}.
func TestValidate_GateFailViaEnv(t *testing.T) {
	t.Setenv("PGOCTL_MIN_SAMPLES", "999999")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.code)
}

// TestMerge_TwoValidProfiles merges two real profiles and asserts the output file exists.
func TestMerge_TwoValidProfiles(t *testing.T) {
	p1 := generateProfile(t)
	p2 := generateProfile(t)
	out := filepath.Join(t.TempDir(), "merged.pgo")
	_, _, err := executeCmd(t, "merge", p1, p2, "--out", out)
	require.NoError(t, err)
	info, statErr := os.Stat(out)
	require.NoError(t, statErr)
	assert.Greater(t, info.Size(), int64(0))
}

// TestMerge_MalformedInput verifies that a malformed file without --drop-invalid causes exitError{2}.
// Note: the command loop parses files before applying DropInvalid, so malformed always exits 2.
func TestMerge_MalformedInput(t *testing.T) {
	valid := generateProfile(t)
	malformed := filepath.Join(t.TempDir(), "bad.pprof")
	require.NoError(t, os.WriteFile(malformed, []byte("not a valid pprof"), 0o600))
	out := filepath.Join(t.TempDir(), "merged.pgo")
	_, _, err := executeCmd(t, "merge", malformed, valid, "--out", out)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestCompare_SameProfile compares a profile to itself — expects nil error (neutral verdict).
func TestCompare_SameProfile(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t, "compare", p, p)
	// Comparing a profile to itself gives neutral → no exitError
	assert.NoError(t, err)
}

// TestCompare_JSONFlag covers the JSON branch of the compare output path.
func TestCompare_JSONFlag(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t, "--json", "compare", p, p)
	assert.NoError(t, err)
}

// TestCompare_BadFile verifies a malformed input causes exitError{2}.
func TestCompare_BadFile(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.pprof")
	require.NoError(t, os.WriteFile(bad, []byte("junk"), 0o600))
	_, _, err := executeCmd(t, "compare", bad, bad)
	var ee *exitError
	if errors.As(err, &ee) {
		assert.Equal(t, 2, ee.code)
	} else {
		assert.Error(t, err)
	}
}
