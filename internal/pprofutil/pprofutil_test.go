package pprofutil

import (
	"testing"

	"github.com/google/pprof/profile"
)

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

func TestSampleCount(t *testing.T) {
	both := []*profile.ValueType{
		{Type: "samples", Unit: "count"},
		{Type: "cpu", Unit: "nanoseconds"},
	}
	cpuOnly := []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}

	tests := []struct {
		name string
		p    *profile.Profile
		want int64
	}{
		{
			name: "sums samples/count values",
			p: &profile.Profile{SampleType: both, Sample: []*profile.Sample{
				{Value: []int64{73, 730000000}},
				{Value: []int64{162, 1620000000}},
			}},
			want: 235,
		},
		{
			name: "falls back to row count without samples/count",
			p: &profile.Profile{SampleType: cpuOnly, Sample: []*profile.Sample{
				{Value: []int64{10000000}},
				{Value: []int64{20000000}},
				{Value: []int64{30000000}},
			}},
			want: 3,
		},
		{
			name: "skips a sample with a short Value slice",
			p: &profile.Profile{SampleType: both, Sample: []*profile.Sample{
				{Value: []int64{5, 50000000}},
				{Value: []int64{}},
			}},
			want: 5,
		},
		{
			name: "empty profile",
			p:    &profile.Profile{SampleType: both},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SampleCount(tt.p); got != tt.want {
				t.Errorf("SampleCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResolver_PackageOf(t *testing.T) {
	r := NewResolver([]string{
		"github.com/satori/go.uuid",
		"github.com/nats-io/nats.go",
		"example.com/m",
		"example.com/m/sub",
		"fmt",
		"",
	})
	tests := []struct {
		fn   string
		want string
	}{
		{"github.com/satori/go.uuid.NewV4", "github.com/satori/go.uuid"},
		{"github.com/nats-io/nats.go.(*Conn).Publish", "github.com/nats-io/nats.go"},
		{"example.com/m/sub.Fn", "example.com/m/sub"}, // longest known prefix wins
		{"example.com/m/sub.Map[go.shape.int]", "example.com/m/sub"},
		{"example.com/m.Fn", "example.com/m"},
		{"example.com/m/sub2.Fn", "example.com/m/sub2"}, // sibling name, not a prefix match
		{"fmt.Sprintf", "fmt"},
		{"example.com/m/unknown.Fn", "example.com/m/unknown"},     // not listed: heuristic
		{"example.com/other/go.uuid.New", "example.com/other/go"}, // not listed: heuristic truncates
		{"main.main", "main"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := r.PackageOf(tt.fn); got != tt.want {
			t.Errorf("Resolver.PackageOf(%q) = %q, want %q", tt.fn, got, tt.want)
		}
	}
}

func TestResolver_NilFallsBack(t *testing.T) {
	var r *Resolver
	if got := r.PackageOf("gopkg.in/yaml.v3.Unmarshal"); got != "gopkg.in/yaml.v3" {
		t.Errorf("nil Resolver.PackageOf = %q, want heuristic result", got)
	}
}

func TestResolver_LongestOfTwoDottedCandidates(t *testing.T) {
	r := NewResolver([]string{"example.com/x/go", "example.com/x/go.uuid"})
	if got := r.PackageOf("example.com/x/go.uuid.New"); got != "example.com/x/go.uuid" {
		t.Errorf("PackageOf = %q, want example.com/x/go.uuid (longest known prefix)", got)
	}
}
