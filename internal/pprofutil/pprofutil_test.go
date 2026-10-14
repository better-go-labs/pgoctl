package pprofutil

import "testing"

func TestPackageOf(t *testing.T) {
	tests := []struct {
		fn   string
		want string
	}{
		// Existing behavior, carried over from validate/explain/leverage tests.
		{"github.com/prometheus/prometheus/tsdb.(*Head).Append", "github.com/prometheus/prometheus/tsdb"},
		{"github.com/prometheus/prometheus/tsdb/wlog.(*Watcher).run", "github.com/prometheus/prometheus/tsdb/wlog"},
		{"github.com/foo/bar.Baz", "github.com/foo/bar"},
		{"github.com/foo/bar.(*Type).(Reader).Read", "github.com/foo/bar"},
		{"runtime.mallocgc", "runtime"},
		{"main.main", "main"},
		{"Foo", "Foo"},
		{"", ""},
		{".init", ".init"},
		{"github.com/foo/(bar)", "github.com/foo/"},
		// Versioned last path element (finding 9).
		{"gopkg.in/yaml.v3.(*decoder).unmarshal", "gopkg.in/yaml.v3"},
		{"gopkg.in/yaml.v3.Unmarshal", "gopkg.in/yaml.v3"},
		{"github.com/foo/bar.v2.Fn", "github.com/foo/bar.v2"},
		{"gopkg.in/yaml.v3", "gopkg.in/yaml.v3"},
		// A method that merely starts with v is not a version suffix.
		{"github.com/foo/bar.validate", "github.com/foo/bar"},
	}
	for _, tt := range tests {
		if got := PackageOf(tt.fn); got != tt.want {
			t.Errorf("PackageOf(%q) = %q, want %q", tt.fn, got, tt.want)
		}
	}
}
