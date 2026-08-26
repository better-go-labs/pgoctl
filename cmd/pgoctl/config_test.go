package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigSearchPaths(t *testing.T) {
	paths := configSearchPaths()
	assert.Greater(t, len(paths), 0)
	assert.Equal(t, ".", paths[0])
}

func TestFindConfigFile_NotFound(t *testing.T) {
	result := findConfigFile([]string{t.TempDir()})
	assert.Equal(t, "", result)
}

func TestFindConfigFile_Found(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "pgoctl.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("test"), 0o600))

	result := findConfigFile([]string{tmpDir})
	assert.Equal(t, confPath, result)
}

func TestFindConfigFile_MultipleSearchPaths(t *testing.T) {
	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()
	confPath := filepath.Join(tmpDir2, "pgoctl.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("test"), 0o600))

	result := findConfigFile([]string{tmpDir1, tmpDir2})
	assert.Equal(t, confPath, result)
}

func TestNewViper_Success(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("test-flag", "default", "test flag")
	cmd.Root()

	v, err := newViper(cmd)
	assert.NoError(t, err)
	assert.NotNil(t, v)
}

func TestNewViperWithPaths_Success(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("test-flag", "default", "test flag")
	cmd.Root()

	tmpDir := t.TempDir()
	v, err := newViperWithPaths(cmd, []string{tmpDir})
	assert.NoError(t, err)
	assert.NotNil(t, v)
}

func TestNewViperWithPaths_BadConfig(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("test-flag", "default", "test flag")
	cmd.Root()

	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "pgoctl.conf")
	// Write invalid YAML
	require.NoError(t, os.WriteFile(confPath, []byte("{ invalid yaml: ["), 0o600))

	_, err := newViperWithPaths(cmd, []string{tmpDir})
	assert.Error(t, err)
}

func TestEnvKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"min-samples", "PGOCTL_MIN_SAMPLES"},
		{"min-score", "PGOCTL_MIN_SCORE"},
		{"min-duration", "PGOCTL_MIN_DURATION"},
		{"json", "PGOCTL_JSON"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, envKey(tt.input))
	}
}

func TestValueFrom_EnvVar(t *testing.T) {
	t.Setenv("PGOCTL_TEST_FLAG", "from-env")
	cmd := &cobra.Command{}
	cmd.Flags().String("test-flag", "default", "test flag")
	cmd.Root()

	v, err := newViper(cmd)
	require.NoError(t, err)

	val, ok := valueFrom(v, "test-flag")
	assert.True(t, ok)
	assert.Equal(t, "from-env", val)
}

func TestValueFrom_NotSet(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("test-flag", "default", "test flag")
	cmd.Root()

	v, err := newViper(cmd)
	require.NoError(t, err)

	val, ok := valueFrom(v, "nonexistent-flag")
	assert.False(t, ok)
	assert.Equal(t, "", val)
}

func TestValueFrom_ConfigFile_String(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "pgoctl.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("min-samples: 5000\n"), 0o600))

	cmd := &cobra.Command{}
	cmd.Flags().Int64("min-samples", 10000, "min samples")
	cmd.Root()

	v, err := newViperWithPaths(cmd, []string{tmpDir})
	require.NoError(t, err)

	val, ok := valueFrom(v, "min-samples")
	assert.True(t, ok)
	assert.Equal(t, "5000", val)
}

func TestValueFrom_ConfigFile_List(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "pgoctl.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("min-package-share:\n  - pkg1:10\n  - pkg2:20\n"), 0o600))

	cmd := &cobra.Command{}
	cmd.Flags().StringArray("min-package-share", nil, "min package share")
	cmd.Root()

	v, err := newViperWithPaths(cmd, []string{tmpDir})
	require.NoError(t, err)

	val, ok := valueFrom(v, "min-package-share")
	assert.True(t, ok)
	assert.Equal(t, "pkg1:10,pkg2:20", val)
}

func TestResolveConfig_CLIFlag(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("test-flag", "default", "test flag")
	cmd.Root()
	require.NoError(t, cmd.Flags().Set("test-flag", "from-cli"))

	v, err := newViper(cmd)
	require.NoError(t, err)

	cfg, err := resolveConfig(cmd, v, []string{"test-flag"})
	require.NoError(t, err)
	// CLI flag is set, so it should not be in the config map
	assert.NotContains(t, cfg, "test-flag")
}

func TestResolveConfig_EnvVar(t *testing.T) {
	t.Setenv("PGOCTL_TEST_FLAG", "from-env")
	cmd := &cobra.Command{}
	cmd.Flags().String("test-flag", "default", "test flag")
	cmd.Root()

	v, err := newViper(cmd)
	require.NoError(t, err)

	cfg, err := resolveConfig(cmd, v, []string{"test-flag"})
	require.NoError(t, err)
	assert.Contains(t, cfg, "test-flag")
	assert.Equal(t, "from-env", cfg["test-flag"])
}

func TestValueFrom_ConfigFile_Int(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "pgoctl.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("min-samples: 5000\n"), 0o600))

	cmd := &cobra.Command{}
	cmd.Flags().Int64("min-samples", 10000, "min samples")
	cmd.Root()

	v, err := newViperWithPaths(cmd, []string{tmpDir})
	require.NoError(t, err)

	val, ok := valueFrom(v, "min-samples")
	assert.True(t, ok)
	assert.Equal(t, "5000", val)
}

func TestValueFrom_ConfigFile_Bool(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "pgoctl.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("json: true\n"), 0o600))

	cmd := &cobra.Command{}
	cmd.Flags().Bool("json", false, "json output")
	cmd.Root()

	v, err := newViperWithPaths(cmd, []string{tmpDir})
	require.NoError(t, err)

	val, ok := valueFrom(v, "json")
	assert.True(t, ok)
	assert.Equal(t, "true", val)
}

func TestValueFrom_EnvVarPrecedence(t *testing.T) {
	t.Setenv("PGOCTL_MIN_SAMPLES", "from-env")
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "pgoctl.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("min-samples: 5000\n"), 0o600))

	cmd := &cobra.Command{}
	cmd.Flags().Int64("min-samples", 10000, "min samples")
	cmd.Root()

	v, err := newViperWithPaths(cmd, []string{tmpDir})
	require.NoError(t, err)

	// Env should take precedence over config file
	val, ok := valueFrom(v, "min-samples")
	assert.True(t, ok)
	assert.Equal(t, "from-env", val)
}
