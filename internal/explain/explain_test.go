package explain

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeProfile(t *testing.T, samples []struct {
	fn    string
	count int64
}) *profile.Profile {
	t.Helper()
	p := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "cpu", Unit: "nanoseconds"},
		},
		DurationNanos: 30 * 1e9,
	}
	fnMap := map[string]*profile.Function{}
	locMap := map[string]*profile.Location{}
	for _, s := range samples {
		if _, ok := fnMap[s.fn]; !ok {
			id := uint64(len(fnMap) + 1)
			fn := &profile.Function{ID: id, Name: s.fn}
			loc := &profile.Location{
				ID:   id,
				Line: []profile.Line{{Function: fn}},
			}
			fnMap[s.fn] = fn
			locMap[s.fn] = loc
			p.Function = append(p.Function, fn)
			p.Location = append(p.Location, loc)
		}
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{locMap[s.fn]},
			Value:    []int64{s.count},
		})
	}
	return p
}

func writeTmpProfile(t *testing.T, p *profile.Profile) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "cpu*.pprof")
	require.NoError(t, err)
	require.NoError(t, p.Write(f))
	require.NoError(t, f.Close())
	return f.Name()
}

func TestAnalyzeFile_TopN(t *testing.T) {
	funcs := []struct {
		fn    string
		count int64
	}{
		{"github.com/prometheus/prometheus/tsdb.(*Head).Append", 5000},
		{"github.com/prometheus/prometheus/tsdb.(*Head).gc", 3000},
		{"github.com/prometheus/prometheus/promql.(*Engine).exec", 2000},
		{"runtime.mallocgc", 1000},
		{"runtime.memmove", 500},
	}
	// Pad to pass minSamples=10000: repeat each sample to reach 11500.
	expanded := make([]struct {
		fn    string
		count int64
	}, 0, 115)
	for _, f := range funcs {
		for i := int64(0); i < f.count/100; i++ {
			expanded = append(expanded, struct {
				fn    string
				count int64
			}{f.fn, 100})
		}
	}
	p := makeProfile(t, expanded)
	path := writeTmpProfile(t, p)

	rpt, err := AnalyzeFile(path, 3)
	require.NoError(t, err)

	assert.Equal(t, 3, len(rpt.TopFunctions))
	assert.Equal(t, "github.com/prometheus/prometheus/tsdb.(*Head).Append", rpt.TopFunctions[0].Function)
	assert.Greater(t, rpt.TopFunctions[0].FlatPct, rpt.TopFunctions[1].FlatPct)

	// Package groups: tsdb should be the top group.
	require.NotEmpty(t, rpt.PackageGroups)
	assert.Equal(t, "github.com/prometheus/prometheus/tsdb", rpt.PackageGroups[0].Package)
}

func TestAnalyzeFile_Verdict(t *testing.T) {
	tests := []struct {
		name          string
		sampleCount   int
		funcCount     int
		wantVerdictIn []Verdict
	}{
		{
			name:          "not_ready_too_few_samples",
			sampleCount:   100,
			funcCount:     5,
			wantVerdictIn: []Verdict{VerdictNotReady},
		},
		{
			name:          "borderline",
			sampleCount:   15000,
			funcCount:     30,
			wantVerdictIn: []Verdict{VerdictBorderline},
		},
		{
			name:          "ready",
			sampleCount:   60000,
			funcCount:     50,
			wantVerdictIn: []Verdict{VerdictReady},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &profile.Profile{
				SampleType:    []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
				DurationNanos: 30 * 1e9,
			}
			for i := 0; i < tt.funcCount; i++ {
				fn := &profile.Function{ID: uint64(i + 1), Name: "pkg.Func" + string(rune('A'+i%26))}
				loc := &profile.Location{ID: uint64(i + 1), Line: []profile.Line{{Function: fn}}}
				p.Function = append(p.Function, fn)
				p.Location = append(p.Location, loc)
			}
			for i := 0; i < tt.sampleCount; i++ {
				loc := p.Location[i%tt.funcCount]
				p.Sample = append(p.Sample, &profile.Sample{
					Location: []*profile.Location{loc},
					Value:    []int64{1000},
				})
			}

			path := writeTmpProfile(t, p)
			rpt, err := AnalyzeFile(path, 20)
			require.NoError(t, err)
			assert.Contains(t, tt.wantVerdictIn, rpt.Verdict, "verdict=%s reason=%s", rpt.Verdict, rpt.VerdictReason)
		})
	}
}

func TestAnalyzeFile_NotFound(t *testing.T) {
	_, err := AnalyzeFile(filepath.Join(t.TempDir(), "nonexistent.pprof"), 10)
	require.Error(t, err)
}

func TestPackageFromFunction(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github.com/prometheus/prometheus/tsdb.(*Head).Append", "github.com/prometheus/prometheus/tsdb"},
		{"github.com/prometheus/prometheus/tsdb/wlog.(*Watcher).run", "github.com/prometheus/prometheus/tsdb/wlog"},
		{"runtime.mallocgc", "runtime"},
		{"main.main", "main"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, packageFromFunction(tt.input), tt.input)
	}
}

// TestPprofDataVsExplain cross-validates the explain flat-% computation by
// reading the same profile twice: once with the raw pprof package (ground truth)
// and once via AnalyzeFile. Both computations must agree to within 0.01%.
func TestPprofDataVsExplain(t *testing.T) {
	// Prometheus-style function names. nSamples is the number of pprof sample
	// entries for each function; each entry carries sampleValue ns of CPU time.
	type fnWeight struct {
		fn       string
		nSamples int
	}
	const sampleValue = int64(1000) // nanoseconds per sample entry
	fnWeights := []fnWeight{
		{"github.com/prometheus/prometheus/tsdb.(*Head).Append", 5000},
		{"github.com/prometheus/prometheus/tsdb.(*Head).gc", 3000},
		{"github.com/prometheus/prometheus/promql.(*Engine).exec", 2000},
		{"github.com/prometheus/prometheus/tsdb/wlog.(*Watcher).run", 1000},
		{"runtime.mallocgc", 500},
	}
	// Total = 11500 samples (> minSamples=10000, < targetSamples=50000 → borderline).

	var rawSamples []struct {
		fn    string
		count int64
	}
	for _, fw := range fnWeights {
		for i := 0; i < fw.nSamples; i++ {
			rawSamples = append(rawSamples, struct {
				fn    string
				count int64
			}{fw.fn, sampleValue})
		}
	}

	p := makeProfile(t, rawSamples)
	path := writeTmpProfile(t, p)

	// ── Step 1: read profile back with the pprof package and print raw data ──
	fileData, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := profile.ParseData(fileData)
	require.NoError(t, err)

	t.Logf("=== raw pprof data ===")
	t.Logf("SampleType: %v", parsed.SampleType)
	t.Logf("DurationNanos: %d (%.1fs)", parsed.DurationNanos, float64(parsed.DurationNanos)/1e9)
	t.Logf("Total sample entries: %d", len(parsed.Sample))
	t.Logf("Functions registered: %d", len(parsed.Function))
	for _, fn := range parsed.Function {
		t.Logf("  id=%d  name=%s", fn.ID, fn.Name)
	}

	// Manually compute flat% from the raw pprof samples — this is the ground truth.
	idx, ok := cpuSampleIndex(parsed)
	require.True(t, ok, "pprof profile must contain a cpu or samples value column")

	totalCPU := 0.0
	rawCounts := make(map[string]float64)
	for _, s := range parsed.Sample {
		if len(s.Location) == 0 || len(s.Value) <= idx {
			continue
		}
		v := float64(s.Value[idx])
		totalCPU += v
		loc := s.Location[0]
		if len(loc.Line) > 0 && loc.Line[0].Function != nil {
			rawCounts[loc.Line[0].Function.Name] += v
		}
	}

	t.Logf("=== manually computed flat%% from raw pprof ===")
	t.Logf("totalCPU: %.0f ns", totalCPU)
	expectedPct := make(map[string]float64, len(rawCounts))
	for fn, v := range rawCounts {
		pct := math.Round(100.0*v/totalCPU*100) / 100
		expectedPct[fn] = pct
		t.Logf("  %.2f%%  %s", pct, fn)
	}

	// ── Step 2: run explain on the same file ──
	rpt, err := AnalyzeFile(path, 20)
	require.NoError(t, err)

	t.Logf("=== explain output ===")
	t.Logf("verdict: %s — %s", rpt.Verdict, rpt.VerdictReason)
	t.Logf("top functions:")
	for _, f := range rpt.TopFunctions {
		t.Logf("  %.2f%%  [%s]  %s", f.FlatPct, f.Package, f.Function)
	}
	t.Logf("package groups:")
	for _, g := range rpt.PackageGroups {
		t.Logf("  %.2f%%  %s", g.TotalPct, g.Package)
	}

	// ── Step 3: compare explain output against raw pprof ground truth ──
	require.NotEmpty(t, rpt.TopFunctions)

	// topN=20 covers all 5 functions — every raw function must appear.
	assert.Equal(t, len(rawCounts), len(rpt.TopFunctions),
		"explain must surface all %d functions when topN exceeds unique function count", len(rawCounts))

	// Each function's flat% in the explain report must match the raw computation.
	for _, f := range rpt.TopFunctions {
		want, found := expectedPct[f.Function]
		require.True(t, found, "function %q in explain report was not found in raw pprof data", f.Function)
		assert.InDelta(t, want, f.FlatPct, 0.01,
			"flat%% mismatch for %s: raw pprof=%.2f%% explain=%.2f%%",
			f.Function, want, f.FlatPct)
	}

	// Verdict check: 11500 samples but only 5 unique functions (< minFunctions=20)
	// → not-ready on the function-diversity branch, not the sample-count branch.
	assert.Equal(t, VerdictNotReady, rpt.Verdict)
	assert.Contains(t, rpt.VerdictReason, "5 unique functions")
}

// TestAnalyzeFile_ParseError covers the profile.ParseData error path in AnalyzeFile.
func TestAnalyzeFile_ParseError(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.pprof")
	require.NoError(t, os.WriteFile(bad, []byte("not a pprof file"), 0o600))
	_, err := AnalyzeFile(bad, 10)
	require.Error(t, err)
}

// TestCPUSampleIndex_FallbackPath covers the samples/count fallback branch
// (fallback=i and return fallback,true) in cpuSampleIndex.
func TestCPUSampleIndex_FallbackPath(t *testing.T) {
	p := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
		},
	}
	idx, ok := cpuSampleIndex(p)
	assert.True(t, ok)
	assert.Equal(t, 0, idx)
}

// TestCPUSampleIndex_NotFound covers the return -1,false path when the profile
// has neither cpu/nanoseconds nor samples/count sample types.
func TestCPUSampleIndex_NotFound(t *testing.T) {
	p := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_space", Unit: "bytes"},
		},
	}
	idx, ok := cpuSampleIndex(p)
	assert.False(t, ok)
	assert.Equal(t, -1, idx)
}

// TestAnalyze_EqualPackageTotals covers the alphabetical sort fallback (line 126)
// triggered when two package groups have equal TotalPct.
func TestAnalyze_EqualPackageTotals(t *testing.T) {
	// Two functions in different packages, each with 50% of CPU time.
	// After rounding, both package totals are equal → alphabetical sort kicks in.
	samples := []struct {
		fn    string
		count int64
	}{
		{"pkg-beta.Func", 5000},
		{"pkg-alpha.Func", 5000},
	}
	p := makeProfile(t, samples)
	path := writeTmpProfile(t, p)

	rpt, err := AnalyzeFile(path, 10)
	require.NoError(t, err)
	require.Len(t, rpt.PackageGroups, 2)
	assert.Equal(t, "pkg-alpha", rpt.PackageGroups[0].Package)
	assert.Equal(t, "pkg-beta", rpt.PackageGroups[1].Package)
}

// TestAnalyze_EmptyLocationSkipped covers the len(s.Location)==0 continue
// branch in analyze.
func TestAnalyze_EmptyLocationSkipped(t *testing.T) {
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		DurationNanos: 10 * 1e9,
	}
	fn := &profile.Function{ID: 1, Name: "runtime.main"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn}}}
	p.Function = []*profile.Function{fn}
	p.Location = []*profile.Location{loc}

	p.Sample = []*profile.Sample{
		{Location: []*profile.Location{loc}, Value: []int64{1000}},
		{Location: []*profile.Location{}, Value: []int64{500}}, // empty location → skipped
	}

	rpt := analyze("test.pprof", p, 10)
	require.NotNil(t, rpt)
	assert.Equal(t, int64(2), rpt.TotalSamples)
	require.Len(t, rpt.TopFunctions, 1)
}

// TestPackageFromFunction_EmptyResult covers the pkg=="" return-name branch
// when the function name starts with a "." or "(" and has no package prefix.
func TestPackageFromFunction_EmptyResult(t *testing.T) {
	// ".init" has no slash, and IndexAny finds "." at position 0 → cut=0 → pkg=""
	result := packageFromFunction(".init")
	assert.Equal(t, ".init", result)
}
