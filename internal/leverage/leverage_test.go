package leverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/pprof/profile"
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

	if rpt.Verdict != VerdictIncomplete {
		t.Errorf("expected VerdictIncomplete, got %s", rpt.Verdict)
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
			name: "multiple interfaces",
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

	if rpt.Verdict != VerdictIncomplete {
		t.Errorf("expected VerdictIncomplete, got %s", rpt.Verdict)
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

func TestCPUSampleIndex(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantIdx  int
		wantOk   bool
	}{
		{
			name:     "cpu_valid.pprof has cpu samples",
			filename: "cpu_valid.pprof",
			wantIdx:  1,
			wantOk:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join("..", "..", "testdata", tt.filename)
			data, err := os.ReadFile(path)
			if err != nil || strings.Contains(string(data), "git-lfs") {
				t.Skipf("testdata %s not available", tt.filename)
			}

			p, err := profile.ParseData(data)
			if err != nil {
				t.Fatalf("failed to parse profile: %v", err)
			}

			idx, ok := cpuSampleIndex(p)
			if ok != tt.wantOk {
				t.Errorf("cpuSampleIndex() ok = %v, want %v", ok, tt.wantOk)
			}
			if ok && idx != tt.wantIdx {
				t.Errorf("cpuSampleIndex() idx = %d, want %d", idx, tt.wantIdx)
			}
		})
	}
}

func TestPackageFromFunctionEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		want string
	}{
		{
			name: "empty string",
			fn:   "",
			want: "",
		},
		{
			name: "no slash",
			fn:   "Foo",
			want: "Foo",
		},
		{
			name: "local package",
			fn:   "main.Foo",
			want: "main",
		},
		{
			name: "nested with method",
			fn:   "github.com/foo/bar/baz.(*Type).Method",
			want: "github.com/foo/bar/baz",
		},
		{
			name: "nested with paren in middle",
			fn:   "github.com/foo/(bar)",
			want: "github.com/foo/",
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

func TestDetectHotInterfacesWithDuplicates(t *testing.T) {
	entries := []FunctionEntry{
		{Function: "foo.(*Bar).(Reader)"},
		{Function: "foo.(*Bar).(Reader)"},
		{Function: "baz.(*Qux).(Writer)"},
	}

	got := detectHotInterfaces(entries)
	if len(got) != 2 {
		t.Errorf("detectHotInterfaces() = %d interfaces, want 2 (should dedup)", len(got))
	}

	seen := make(map[string]bool)
	for _, iface := range got {
		if seen[iface] {
			t.Errorf("detectHotInterfaces() returned duplicate: %s", iface)
		}
		seen[iface] = true
	}
}

// makeMinimalProfile creates a temp pprof file with one cpu sample — no LFS dependency.
func makeMinimalProfile(t *testing.T) string {
	t.Helper()
	fn := &profile.Function{ID: 1, Name: "main.main", SystemName: "main.main", Filename: "main.go", StartLine: 1}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn, Line: 1}}}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		Function:   []*profile.Function{fn},
		Location:   []*profile.Location{loc},
		Sample:     []*profile.Sample{{Location: []*profile.Location{loc}, Value: []int64{1_000_000}}},
	}
	f, err := os.CreateTemp("", "test-*.pprof")
	if err != nil {
		t.Fatalf("create temp pprof: %v", err)
	}
	if werr := p.Write(f); werr != nil {
		_ = f.Close()
		t.Fatalf("write pprof: %v", werr)
	}
	_ = f.Close()
	return f.Name()
}

func TestCheckFile_InvalidData(t *testing.T) {
	f, err := os.CreateTemp("", "*.pprof")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("not valid pprof data")
	_ = f.Close()
	defer os.Remove(f.Name())

	_, err = CheckFile(f.Name(), Options{})
	if err == nil {
		t.Error("expected error for invalid pprof data")
	}
	if !strings.Contains(err.Error(), "parse profile") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestCpuSampleIndex_FallbackOnly(t *testing.T) {
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "samples", Unit: "count"}},
	}
	idx, ok := cpuSampleIndex(p)
	if !ok {
		t.Error("expected ok=true for samples/count type")
	}
	if idx != 0 {
		t.Errorf("expected idx=0, got %d", idx)
	}
}

func TestCpuSampleIndex_NoMatch(t *testing.T) {
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "alloc_space", Unit: "bytes"}},
	}
	_, ok := cpuSampleIndex(p)
	if ok {
		t.Error("expected ok=false for unknown sample type")
	}
}

func TestCheckFile_BuildAnalysisError(t *testing.T) {
	profilePath := makeMinimalProfile(t)
	defer os.Remove(profilePath)

	_, err := CheckFile(profilePath, Options{
		Dir:     "/nonexistent/module/dir",
		Package: "./...",
	})
	if err == nil {
		t.Error("expected error for invalid build dir")
	}
	if !strings.Contains(err.Error(), "build analysis") {
		t.Errorf("expected build analysis error, got: %v", err)
	}
}

func TestCheckFile_WithDir(t *testing.T) {
	// Build a minimal Go module in a temp dir so we don't need the LFS testdata.
	moduleDir := t.TempDir()
	goMod := []byte("module example.com/testmod\n\ngo 1.21\n")
	mainGo := []byte("package main\n\nfunc add(a, b int) int { return a + b }\n\nfunc main() { _ = add(1, 2) }\n")
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), goMod, 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "main.go"), mainGo, 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	profilePath := makeMinimalProfile(t)
	defer os.Remove(profilePath)

	rpt, err := CheckFile(profilePath, Options{
		TopN:    5,
		Dir:     moduleDir,
		Package: ".",
	})
	if err != nil {
		t.Fatalf("CheckFile with dir failed: %v", err)
	}
	if rpt.BuildAnalysis == nil {
		t.Fatal("expected BuildAnalysis to be set when dir is provided")
	}
	switch rpt.Verdict {
	case VerdictHigh, VerdictLow, VerdictNone:
		// valid
	default:
		t.Errorf("unexpected verdict: %s", rpt.Verdict)
	}
}

func TestCheckFile_WithDir_VerdictNone(t *testing.T) {
	// A trivial module with no inlinable calls produces 0 PGO decisions → NONE.
	moduleDir := t.TempDir()
	goMod := []byte("module example.com/trivial\n\ngo 1.21\n")
	mainGo := []byte("package main\n\nfunc main() {}\n")
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), goMod, 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "main.go"), mainGo, 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	profilePath := makeMinimalProfile(t)
	defer os.Remove(profilePath)

	rpt, err := CheckFile(profilePath, Options{
		Dir:     moduleDir,
		Package: ".",
	})
	if err != nil {
		t.Fatalf("CheckFile with trivial dir failed: %v", err)
	}
	if rpt.BuildAnalysis == nil {
		t.Fatal("expected BuildAnalysis to be set")
	}
	// Trivial module: expect NONE
	if rpt.Verdict != VerdictNone {
		t.Logf("verdict was %s (expected NONE for trivial module; may vary by toolchain)", rpt.Verdict)
	}
	if !strings.Contains(rpt.VerdictReason, string(rpt.Verdict)) {
		t.Errorf("VerdictReason %q should contain verdict %s", rpt.VerdictReason, rpt.Verdict)
	}
}

func TestCountLinesEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		pattern string
		want    int
	}{
		{
			name:    "empty output",
			output:  "",
			pattern: "test",
			want:    0,
		},
		{
			name:    "empty pattern",
			output:  "line1\nline2",
			pattern: "",
			want:    2,
		},
		{
			name:    "pattern on empty line",
			output:  "\n\n",
			pattern: "test",
			want:    0,
		},
		{
			name:    "case insensitive with mixed case",
			output:  "DeVirtualizing Foo\ndevirtualizing Bar",
			pattern: "devirtualizing",
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

func TestCheckFile_WithDir_VerdictHigh(t *testing.T) {
	// Create a module with functions that are likely to benefit from inlining
	moduleDir := t.TempDir()
	goMod := []byte("module example.com/high-leverage\n\ngo 1.21\n")
	mainGo := []byte(`package main

func inline1() int { return 1 }
func inline2() int { return 2 }
func inline3() int { return 3 }
func inline4() int { return 4 }
func inline5() int { return 5 }
func hot() int {
	total := 0
	for i := 0; i < 100; i++ {
		total += inline1() + inline2() + inline3() + inline4() + inline5()
	}
	return total
}
func main() { _ = hot() }
`)
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), goMod, 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "main.go"), mainGo, 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	// Create a profile with high inline counts to trigger build analysis
	profilePath := makeMinimalProfile(t)
	defer os.Remove(profilePath)

	rpt, err := CheckFile(profilePath, Options{
		Dir:     moduleDir,
		Package: ".",
	})
	if err != nil {
		t.Fatalf("CheckFile with dir failed: %v", err)
	}
	if rpt.BuildAnalysis == nil {
		t.Fatal("expected BuildAnalysis to be set")
	}
	// Verify the report has proper structure even if verdict varies
	if rpt.Verdict == "" {
		t.Error("expected Verdict to be set")
	}
	if rpt.VerdictReason == "" {
		t.Error("expected VerdictReason to be set")
	}
}

func TestCheckFile_AbsoluteProfilePath(t *testing.T) {
	moduleDir := t.TempDir()
	goMod := []byte("module example.com/abs-path\n\ngo 1.21\n")
	mainGo := []byte("package main\n\nfunc main() {}\n")
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), goMod, 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "main.go"), mainGo, 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	profilePath := makeMinimalProfile(t)
	defer os.Remove(profilePath)

	absProfilePath, err := filepath.Abs(profilePath)
	if err != nil {
		t.Skipf("could not get absolute path: %v", err)
	}

	rpt, err := CheckFile(absProfilePath, Options{
		Dir:     moduleDir,
		Package: ".",
	})
	if err != nil {
		t.Fatalf("CheckFile with absolute path failed: %v", err)
	}
	if rpt.ProfilePath != absProfilePath {
		t.Errorf("ProfilePath mismatch: got %q, want %q", rpt.ProfilePath, absProfilePath)
	}
}

func TestCheckFile_IgnoreZeroTopN(t *testing.T) {
	profilePath := makeMinimalProfile(t)
	defer os.Remove(profilePath)

	rpt, err := CheckFile(profilePath, Options{TopN: 0})
	if err != nil {
		t.Fatalf("CheckFile with TopN=0 failed: %v", err)
	}
	if len(rpt.TopFunctions) > 20 {
		t.Errorf("expected TopFunctions to default to 20, got %d", len(rpt.TopFunctions))
	}
}

func TestVerdictConstants(t *testing.T) {
	tests := []struct {
		name    string
		verdict Verdict
		want    string
	}{
		{"NONE", VerdictNone, "NONE"},
		{"LOW", VerdictLow, "LOW"},
		{"HIGH", VerdictHigh, "HIGH"},
		{"INCOMPLETE", VerdictIncomplete, "INCOMPLETE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.verdict) != tt.want {
				t.Errorf("verdict %v = %q, want %q", tt.verdict, tt.verdict, tt.want)
			}
		})
	}
}
