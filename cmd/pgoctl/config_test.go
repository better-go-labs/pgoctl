package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCommand builds a command with the validate-like flags used by the
// config tests.
func testCommand(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "validate <path>"}
	cmd.Flags().Int64("min-samples", 10000, "")
	cmd.Flags().Float64("min-score", 0.6, "")
	cmd.Flags().StringArray("min-package-share", nil, "")
	return cmd
}

// writeConfig writes a pgoctl.conf into dir and returns the dir.
func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pgoctl.conf"), []byte(content), 0o644))
	return dir
}

func TestValueFrom_EnvOverConfig(t *testing.T) {
	dir := writeConfig(t, t.TempDir(), "min-score: 0.9\n")
	cmd := testCommand(t)
	v, err := newViperWithPaths(cmd, []string{dir})
	require.NoError(t, err)

	t.Setenv("PGOCTL_MIN_SCORE", "0.2")
	val, ok := valueFrom(v, "min-score")
	require.True(t, ok)
	assert.Equal(t, "0.2", val, "env must win over config file")
}

func TestValueFrom_ConfigWhenNoEnv(t *testing.T) {
	dir := writeConfig(t, t.TempDir(), "min-samples: 1234\n")
	cmd := testCommand(t)
	v, err := newViperWithPaths(cmd, []string{dir})
	require.NoError(t, err)

	val, ok := valueFrom(v, "min-samples")
	require.True(t, ok)
	assert.Equal(t, "1234", val, "config value must apply when env absent")
}

func TestValueFrom_ListFromConfig(t *testing.T) {
	dir := writeConfig(t, t.TempDir(),
		"min-package-share:\n  - github.com/prometheus/prometheus/tsdb:5.0\n  - github.com/prometheus/prometheus/promql:1.5\n")
	cmd := testCommand(t)
	v, err := newViperWithPaths(cmd, []string{dir})
	require.NoError(t, err)

	val, ok := valueFrom(v, "min-package-share")
	require.True(t, ok)
	assert.Equal(t, "github.com/prometheus/prometheus/tsdb:5.0,github.com/prometheus/prometheus/promql:1.5", val)
}

func TestValueFrom_EnvList(t *testing.T) {
	cmd := testCommand(t)
	v, err := newViperWithPaths(cmd, []string{t.TempDir()})
	require.NoError(t, err)

	t.Setenv("PGOCTL_MIN_PACKAGE_SHARE", "a/b:1.0,c/d:2.0")
	val, ok := valueFrom(v, "min-package-share")
	require.True(t, ok)
	assert.Equal(t, "a/b:1.0,c/d:2.0", val)
}

func TestResolveConfig_CLIWinsOverFile(t *testing.T) {
	// min-score is set explicitly on the CLI (wins, omitted from cfg);
	// min-samples comes from the config file.
	dir := writeConfig(t, t.TempDir(), "min-score: 0.9\nmin-samples: 1234\n")
	cmd := testCommand(t)
	require.NoError(t, cmd.Flags().Set("min-score", "0.1")) // explicit CLI flag
	v, err := newViperWithPaths(cmd, []string{dir})
	require.NoError(t, err)

	cfg, err := resolveConfig(cmd, v, []string{"min-score", "min-samples"})
	require.NoError(t, err)
	assert.NotContains(t, cfg, "min-score", "explicit CLI flag must win; not overridden")
	assert.Equal(t, "1234", cfg["min-samples"], "unset flag takes config value")
}

func TestResolveConfig_FileAppliesWhenFlagAbsent(t *testing.T) {
	dir := writeConfig(t, t.TempDir(), "min-samples: 4321\nmin-score: 0.75\n")
	cmd := testCommand(t)
	v, err := newViperWithPaths(cmd, []string{dir})
	require.NoError(t, err)

	cfg, err := resolveConfig(cmd, v, []string{"min-samples", "min-score"})
	require.NoError(t, err)
	assert.Equal(t, "4321", cfg["min-samples"])
	assert.Equal(t, "0.75", cfg["min-score"])
}

func TestResolveConfig_NothingSet(t *testing.T) {
	cmd := testCommand(t)
	v, err := newViperWithPaths(cmd, []string{t.TempDir()})
	require.NoError(t, err)

	cfg, err := resolveConfig(cmd, v, []string{"min-samples", "min-score"})
	require.NoError(t, err)
	assert.Empty(t, cfg, "no CLI/env/config values -> nothing resolved")
}

func TestNewViper_MalformedConfigError(t *testing.T) {
	dir := writeConfig(t, t.TempDir(), "min-score: [unclosed\n")
	cmd := testCommand(t)
	_, err := newViperWithPaths(cmd, []string{dir})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "config", "error must mention config")
}

func TestNewViper_MissingConfigNotAnError(t *testing.T) {
	cmd := testCommand(t)
	v, err := newViperWithPaths(cmd, []string{t.TempDir()})
	require.NoError(t, err)
	assert.False(t, v.InConfig("min-score"))
}

func TestEnvKey(t *testing.T) {
	assert.Equal(t, "PGOCTL_MIN_SAMPLES", envKey("min-samples"))
	assert.Equal(t, "PGOCTL_MIN_PACKAGE_SHARE", envKey("min-package-share"))
	assert.Equal(t, "PGOCTL_JSON", envKey("json"))
}

// TestConfigSearchPaths verifies the returned slice is non-empty and contains
// a home-directory-relative path (the standard ~/.config/pgoctl location).
func TestConfigSearchPaths(t *testing.T) {
	paths := configSearchPaths()
	require.NotEmpty(t, paths, "configSearchPaths must return at least one path")

	// First entry is always "." (cwd).
	assert.Equal(t, ".", paths[0])

	// At least one path should contain ".config/pgoctl" (home dir entry).
	found := false
	for _, p := range paths {
		if strings.Contains(p, filepath.Join(".config", "pgoctl")) {
			found = true
			break
		}
	}
	assert.True(t, found, "expected a ~/.config/pgoctl entry in %v", paths)
}

// TestNewViper_ReturnsNonNil verifies newViper succeeds and returns a usable instance.
func TestNewViper_ReturnsNonNil(t *testing.T) {
	cmd := testCommand(t)
	// Wire the command into a root so PersistentFlags are accessible.
	root := newRootCmd()
	root.AddCommand(cmd)

	v, err := newViper(cmd)
	require.NoError(t, err)
	require.NotNil(t, v)
}

// TestNewViper_BindsEnvPrefix verifies that env-var overrides are picked up by
// valueFrom after newViper wires up the viper instance. The codebase translates
// hyphens to underscores itself (PGOCTL_MIN_SAMPLES); this test uses valueFrom
// (the real consumer) not v.GetInt64 directly.
func TestNewViper_BindsEnvPrefix(t *testing.T) {
	t.Setenv("PGOCTL_MIN_SAMPLES", "12345")
	cmd := testCommand(t)
	root := newRootCmd()
	root.AddCommand(cmd)

	v, err := newViper(cmd)
	require.NoError(t, err)
	require.NotNil(t, v)

	// valueFrom does the hyphen→underscore conversion and os.LookupEnv.
	val, ok := valueFrom(v, "min-samples")
	require.True(t, ok, "expected PGOCTL_MIN_SAMPLES to be visible via valueFrom")
	assert.Equal(t, "12345", val)
}
