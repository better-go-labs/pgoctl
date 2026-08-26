package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExplain_TextOutput runs explain on a real profile and asserts text output is produced.
// explain uses cmd.OutOrStdout(), so output is captured by executeCmd.
func TestExplain_TextOutput(t *testing.T) {
	p := generateProfile(t)
	out, _, err := executeCmd(t, "explain", p)
	require.NoError(t, err)
	assert.NotEmpty(t, out)
	assert.True(t, strings.Contains(out, "samples") || strings.Contains(out, "verdict"),
		"expected explain output to contain profile summary fields, got: %q", out)
}

// TestExplain_JSONFormat covers the JSON branch of the explain command.
// explain writes JSON to cmd.OutOrStdout(), so output is captured.
func TestExplain_JSONFormat(t *testing.T) {
	p := generateProfile(t)
	out, _, err := executeCmd(t, "explain", "--format=json", p)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(out), "{"),
		"expected JSON output, got: %q", out)
}

// TestExplain_NonexistentFile expects exitError{2}.
func TestExplain_NonexistentFile(t *testing.T) {
	_, _, err := executeCmd(t, "explain", "/nonexistent/path/profile.pprof")
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}
