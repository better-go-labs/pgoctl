package leverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFile_ProfileOnly(t *testing.T) {
	profilePath := filepath.Join("..", "..", "testdata", "cpu_valid.pprof")
	data, err := os.ReadFile(profilePath)
	if err != nil || strings.Contains(string(data), "git-lfs") {
		t.Skipf("testdata file not found or is LFS pointer")
	}

	opts := Options{
		TopN:    20,
		Package: "./...",
	}

	rpt, err := CheckFile(profilePath, opts)
	if err != nil {
		t.Fatalf("CheckFile failed: %v", err)
	}

	if rpt.Verdict != VerdictProfileOnly {
		t.Errorf("expected VerdictProfileOnly, got %s", rpt.Verdict)
	}

	if len(rpt.TopFunctions) == 0 {
		t.Errorf("expected TopFunctions to be populated")
	}

	if rpt.BuildAnalysis != nil {
		t.Errorf("expected BuildAnalysis to be nil for profile-only run")
	}

	if rpt.TotalSamples <= 0 {
		t.Errorf("expected TotalSamples > 0, got %d", rpt.TotalSamples)
	}
}

func TestCheckFile_InvalidPath(t *testing.T) {
	_, err := CheckFile("/nonexistent/path.pprof", Options{})
	if err == nil {
		t.Errorf("expected error for nonexistent profile")
	}
}

func TestCountLines(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		pattern string
		want    int
	}{
		{
			name:    "no matches",
			output:  "line 1\nline 2\nline 3",
			pattern: "notfound",
			want:    0,
		},
		{
			name:    "single match",
			output:  "line 1\ninlining call\nline 3",
			pattern: "inlining call",
			want:    1,
		},
		{
			name:    "multiple matches",
			output:  "inlining call to foo\ninlining call to bar\nsome other line",
			pattern: "inlining call",
			want:    2,
		},
		{
			name:    "case insensitive",
			output:  "Inlining Call to foo\nInlining call to bar",
			pattern: "inlining call",
			want:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countLines(tt.output, tt.pattern)
			if got != tt.want {
				t.Errorf("countLines(%q, %q) = %d, want %d", tt.output, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestDetectHotInterfaces(t *testing.T) {
	tests := []struct {
		name    string
		entries []FunctionEntry
		want    int
	}{
		{
			name:    "no interfaces",
			entries: []FunctionEntry{{Function: "foo.Bar"}},
			want:    0,
		},
		{
			name:    "one interface",
			entries: []FunctionEntry{{Function: "foo.(*Bar).(Reader)"}},
			want:    1,
		},
		{
			name:    "multiple interfaces",
			entries: []FunctionEntry{
				{Function: "foo.(*Bar).(Reader)"},
				{Function: "baz.(*Qux).(Writer)"},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectHotInterfaces(tt.entries)
			if len(got) != tt.want {
				t.Errorf("detectHotInterfaces() = %d interfaces, want %d", len(got), tt.want)
			}
		})
	}
}

func TestPackageFromFunction(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		want string
	}{
		{
			name: "simple function",
			fn:   "github.com/foo/bar.Baz",
			want: "github.com/foo/bar",
		},
		{
			name: "method",
			fn:   "github.com/foo/bar.(*Type).Method",
			want: "github.com/foo/bar",
		},
		{
			name: "interface method",
			fn:   "github.com/foo/bar.(*Type).(Reader).Read",
			want: "github.com/foo/bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := packageFromFunction(tt.fn)
			if got != tt.want {
				t.Errorf("packageFromFunction(%q) = %q, want %q", tt.fn, got, tt.want)
			}
		})
	}
}

func TestCheckFileDefaultOptions(t *testing.T) {
	profilePath := filepath.Join("..", "..", "testdata", "cpu_valid.pprof")
	data, err := os.ReadFile(profilePath)
	if err != nil || strings.Contains(string(data), "git-lfs") {
		t.Skipf("testdata file not found or is LFS pointer")
	}

	rpt, err := CheckFile(profilePath, Options{})
	if err != nil {
		t.Fatalf("CheckFile with default options failed: %v", err)
	}

	if rpt.Verdict != VerdictProfileOnly {
		t.Errorf("expected VerdictProfileOnly, got %s", rpt.Verdict)
	}
}

func TestCheckFileDefaultTopN(t *testing.T) {
	profilePath := filepath.Join("..", "..", "testdata", "cpu_valid.pprof")
	data, err := os.ReadFile(profilePath)
	if err != nil || strings.Contains(string(data), "git-lfs") {
		t.Skipf("testdata file not found or is LFS pointer")
	}

	rpt, err := CheckFile(profilePath, Options{TopN: 0})
	if err != nil {
		t.Fatalf("CheckFile with TopN=0 failed: %v", err)
	}

	if len(rpt.TopFunctions) > 20 {
		t.Errorf("expected TopFunctions to be clamped at 20, got %d", len(rpt.TopFunctions))
	}
}

func TestReadProfileFileAbsolute(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "cpu_valid.pprof"))
	if err != nil {
		t.Skipf("could not get absolute path: %v", err)
	}

	data, err := os.ReadFile(abs)
	if err != nil || strings.Contains(string(data), "git-lfs") {
		t.Skipf("testdata file not found or is LFS pointer")
	}

	rpt, err := CheckFile(abs, Options{TopN: 5})
	if err != nil {
		t.Fatalf("CheckFile with absolute path failed: %v", err)
	}

	if rpt.TotalSamples <= 0 {
		t.Errorf("expected TotalSamples > 0")
	}
}
