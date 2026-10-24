package main

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stderr
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

// printError is what keeps plain errors from exiting silently under
// SilenceErrors; the exit-code test covers it only through a subprocess.
func TestPrintError(t *testing.T) {
	old := jsonOutput
	t.Cleanup(func() { jsonOutput = old })

	jsonOutput = false
	assert.Equal(t, "error: --source is required\n",
		captureStderr(t, func() { printError(errors.New("--source is required")) }))

	jsonOutput = true
	assert.JSONEq(t, `{"error":"--source is required"}`,
		captureStderr(t, func() { printError(errors.New("--source is required")) }))
}
