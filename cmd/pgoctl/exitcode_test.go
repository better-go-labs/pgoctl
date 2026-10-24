package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExitCodes verifies the 0/1/2 convention using a compiled binary.
// 0 = success, 1 = gate/verdict failure, 2 = bad usage / input error.
func TestExitCodes(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pgoctl-test")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	require.NoError(t, err, "build failed: %s", string(out))

	run := func(args ...string) int {
		cmd := exec.Command(bin, args...)
		err := cmd.Run()
		if err == nil {
			return 0
		}
		if ex, ok := err.(*exec.ExitError); ok {
			return ex.ExitCode()
		}
		t.Fatalf("unexpected error: %v", err)
		return -1
	}

	// Version flag exits 0.
	assert.Equal(t, 0, run("--version"), "--version should exit 0")

	// Unknown command exits 2.
	assert.Equal(t, 2, run("unknowncmd"), "unknown command should exit 2")

	// Unknown flag exits 2.
	assert.Equal(t, 2, run("validate", "--not-a-real-flag"), "unknown flag should exit 2")

	// Missing required arg exits 2.
	assert.Equal(t, 2, run("validate"), "missing arg should exit 2")

	// Non-existent file exits 2.
	assert.Equal(t, 2, run("validate", filepath.Join(t.TempDir(), "no.pprof")), "file-not-found should exit 2")

	// Check that a missing arg for merge also exits 2.
	assert.Equal(t, 2, run("merge"), "merge missing arg should exit 2")

	// Check that a missing arg for compare also exits 2.
	assert.Equal(t, 2, run("compare"), "compare missing arg should exit 2")
	assert.Equal(t, 2, run("compare", "a.pprof"), "compare one arg should exit 2")

	// Errors that do not go through exitError must still be printed.
	stderrOf := func(args ...string) string {
		cmd := exec.Command(bin, args...)
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	assert.Contains(t, stderrOf("validate", "--not-a-real-flag"), "unknown flag", "flag errors must be printed")
	assert.Contains(t, stderrOf("collect"), "--source is required", "collect errors must be printed")
	assert.Contains(t, stderrOf("collect", "--source=pprof", "--url=http://127.0.0.1:1/debug/pprof/profile?seconds=1"),
		"connection refused", "network errors must be printed")
	assert.Contains(t, stderrOf("collect", "--json"), `{"error":"--source is required"}`, "JSON mode prints a JSON error")

	// Check that compare with bad files exits 2.
	tmp := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmp, "bad.pprof"), []byte("not a pprof"), 0600)
	assert.Equal(t, 2, run("compare", filepath.Join(tmp, "bad.pprof"), filepath.Join(tmp, "bad.pprof")), "compare bad pprof should exit 2")
}
