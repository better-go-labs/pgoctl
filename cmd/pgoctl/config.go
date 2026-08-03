package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Config loading for pgoctl commands.
//
// Precedence (highest first):
//
//	1. explicit CLI flag
//	2. environment variable PGOCTL_<FLAG> (e.g. PGOCTL_MIN_SAMPLES)
//	3. config file pgoctl.conf (YAML) in the current directory, then
//	   ~/.config/pgoctl/
//	4. flag default
//
// The config file is optional; a missing file is not an error. Commands
// opt in by calling newViper + resolveConfig in their RunE (see
// newValidateCmd).

const envPrefix = "PGOCTL"

// configSearchPaths returns the config file search paths: cwd first, then
// ~/.config/pgoctl.
func configSearchPaths() []string {
	paths := []string{"."}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "pgoctl"))
	}
	return paths
}

// newViper builds a viper instance for cmd with the standard search paths.
func newViper(cmd *cobra.Command) (*viper.Viper, error) {
	return newViperWithPaths(cmd, configSearchPaths())
}

// findConfigFile returns the first existing pgoctl.conf among paths, or ""
// when none is present.
func findConfigFile(paths []string) string {
	for _, dir := range paths {
		p := filepath.Join(dir, "pgoctl.conf")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// newViperWithPaths builds a viper instance for cmd reading pgoctl.conf from
// the given paths (first match wins). A missing config file is not an error;
// any other read or parse failure is. The file is pinned explicitly because
// viper does not auto-discover the .conf extension.
func newViperWithPaths(cmd *cobra.Command, paths []string) (*viper.Viper, error) {
	v := viper.New()
	v.SetEnvPrefix(envPrefix)
	v.AutomaticEnv()

	if err := v.BindPFlags(cmd.Flags()); err != nil {
		return nil, fmt.Errorf("bind flags: %w", err)
	}
	if err := v.BindPFlags(cmd.Root().PersistentFlags()); err != nil {
		return nil, fmt.Errorf("bind persistent flags: %w", err)
	}

	if path := findConfigFile(paths); path != "" {
		v.SetConfigType("yaml")
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}
	return v, nil
}

// envKey returns the environment variable name for a flag, e.g.
// min-samples -> PGOCTL_MIN_SAMPLES.
func envKey(name string) string {
	return envPrefix + "_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// valueFrom returns the effective value for a flag that was not set
// explicitly on the command line: environment variable first, then config
// file. ok is false when neither source has a value. List values (e.g.
// min-package-share in YAML) are joined with commas so the caller can reuse
// the same comma-separated parsing as the CLI flag.
func valueFrom(v *viper.Viper, name string) (string, bool) {
	if s, ok := os.LookupEnv(envKey(name)); ok {
		return s, true
	}
	if v.InConfig(name) {
		switch val := v.Get(name).(type) {
		case string:
			return val, true
		case []interface{}:
			parts := make([]string, 0, len(val))
			for _, item := range val {
				parts = append(parts, fmt.Sprint(item))
			}
			return strings.Join(parts, ","), true
		case []string:
			return strings.Join(val, ","), true
		default:
			return fmt.Sprint(val), true
		}
	}
	return "", false
}

// resolveConfig returns effective values for the named flags, honoring
// CLI > env > config. Flags explicitly set on the command line are omitted
// (the caller keeps its CLI value). Values are returned as strings; the
// caller parses them into typed flag variables.
func resolveConfig(cmd *cobra.Command, v *viper.Viper, names []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, name := range names {
		if cmd.Flags().Changed(name) || cmd.Root().PersistentFlags().Changed(name) {
			continue // explicit CLI flag wins
		}
		if val, ok := valueFrom(v, name); ok {
			out[name] = val
		}
	}
	return out, nil
}
