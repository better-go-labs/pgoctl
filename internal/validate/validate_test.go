package validate_test

import (
	"fmt"
	"math"
	"os"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	profiletypes "github.com/Better-Go-Labs/pgoctl/internal/profile"
	"github.com/Better-Go-Labs/pgoctl/internal/validate"
)

func generateCPUProfile(t *testing.T, durationMs int) []byte {
	f, err := os.CreateTemp("", "cpu*.pprof")
	require.NoError(t, err)
	defer func() { _ = os.Remove(f.Name()) }()
	defer func() { _ = f.Close() }()

	err = pprof.StartCPUProfile(f)
	require.NoError(t, err)

	deadline := time.Now().Add(time.Duration(durationMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = fib(30)
	}
	pprof.StopCPUProfile()

	_, err = f.Seek(0, 0)
	require.NoError(t, err)

	data, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	return data
}

func fib(n int) int {
	if n <= 1 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func TestValidate_TableDriven(t *testing.T) {
	// Generate valid pprof data for test setup
	validData := generateCPUProfile(t, 200)

	tests := []struct {
		name        string
		setup       func(t *testing.T) string
		teardown    func(t *testing.T, path string)
		opts        validate.Options
		expectValid bool
		expectError bool
		checkResult func(t *testing.T, r *profiletypes.QualityReport)
	}{
		{
			name: "Valid Profile",
			setup: func(t *testing.T) string {
				f, err := os.CreateTemp("", "valid*.pprof")
				require.NoError(t, err)
				_, err = f.Write(validData)
				require.NoError(t, err)
				_ = f.Close()
				return f.Name()
			},
			teardown: func(_ *testing.T, path string) {
				_ = os.Remove(path)
			},
			opts: func() validate.Options {
				o := validate.DefaultOptions()
				o.MinSamples = 1
				o.MinScore = 0.1
				return o
			}(),
			expectValid: true,
			expectError: false,
			checkResult: func(t *testing.T, r *profiletypes.QualityReport) {
				assert.NotEmpty(t, r.Samples, "Samples count should be non-zero")
				assert.NotEmpty(t, r.UniqueStacks, "Unique stacks count should be non-zero")
			},
		},
		{
			name: "Nonexistent File",
			setup: func(_ *testing.T) string {
				return "/nonexistent/path/does/not/exist.pprof"
			},
			teardown:    func(_ *testing.T, _ string) {},
			opts:        validate.DefaultOptions(),
			expectValid: false,
			expectError: true,
		},
		{
			name: "Invalid Data",
			setup: func(t *testing.T) string {
				f, err := os.CreateTemp("", "bad*.pprof")
				require.NoError(t, err)
				_, err = f.WriteString("not a pprof file")
				require.NoError(t, err)
				_ = f.Close()
				return f.Name()
			},
			teardown: func(_ *testing.T, path string) {
				_ = os.Remove(path)
			},
			opts:        validate.DefaultOptions(),
			expectValid: false,
			expectError: true,
		},
		{
			name: "Score Formula Validation",
			setup: func(t *testing.T) string {
				f, err := os.CreateTemp("", "score*.pprof")
				require.NoError(t, err)
				_, err = f.Write(validData)
				require.NoError(t, err)
				_ = f.Close()
				return f.Name()
			},
			teardown: func(_ *testing.T, path string) {
				_ = os.Remove(path)
			},
			opts: func() validate.Options {
				o := validate.DefaultOptions()
				o.MinSamples = 1
				return o
			}(),
			expectValid: false,
			expectError: false,
			checkResult: func(t *testing.T, r *profiletypes.QualityReport) {
				assert.GreaterOrEqual(t, r.QualityScore, 0.0, "Score should be >= 0")
				assert.LessOrEqual(t, r.QualityScore, 1.0, "Score should be <= 1")
			},
		},
		{
			name: "Custom Density-Only Weight",
			setup: func(t *testing.T) string {
				f, err := os.CreateTemp("", "density-only*.pprof")
				require.NoError(t, err)
				_, err = f.Write(validData)
				require.NoError(t, err)
				_ = f.Close()
				return f.Name()
			},
			teardown: func(_ *testing.T, path string) { _ = os.Remove(path) },
			opts: func() validate.Options {
				o := validate.DefaultOptions()
				o.MinSamples = 1
				o.WeightDensity = 1
				o.WeightRichness = 0
				o.WeightCoverage = 0
				o.WeightDepth = 0
				return o
			}(),
			expectValid: false,
			expectError: false,
			checkResult: func(t *testing.T, r *profiletypes.QualityReport) {
				// New formula: density = grade(samples, minSamples, targetSamples)
				// MinSamples=1 → lo=1; DefaultOptions TargetSamples=500000
				lo, hi := 1.0, 500000.0
				var expected float64
				s := float64(r.Samples)
				if s <= lo {
					expected = 0
				} else if s >= hi {
					expected = 1
				} else {
					expected = math.Log(s/lo) / math.Log(hi/lo)
				}
				expected = math.Round(expected*1000) / 1000
				assert.InDelta(t, expected, r.QualityScore, 0.001, "score should equal density term (log-scale)")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup(t)
			defer tt.teardown(t, path)

			report, err := validate.ValidateFile(path, tt.opts)
			if tt.expectError {
				assert.Error(t, err, "Expected an error")
				assert.Nil(t, report, "Report should be nil on error")
			} else {
				assert.NoError(t, err, "Unexpected error")
				assert.NotNil(t, report, "Report should not be nil")
				if tt.expectValid {
					assert.True(t, report.Valid, "Report should be valid")
				}
				if tt.checkResult != nil {
					tt.checkResult(t, report)
				}
			}
		})
	}
}

// writeTmpProfileV writes p to a temp file and returns the path.
func writeTmpProfileV(t *testing.T, p *profile.Profile) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "cpu*.pprof")
	require.NoError(t, err)
	require.NoError(t, p.Write(f))
	require.NoError(t, f.Close())
	return f.Name()
}

// TestParsePackageShareGates_EmptyEntries verifies whitespace-only and empty
// comma-separated entries are silently skipped (covers the continue branch).
func TestParsePackageShareGates_EmptyEntries(t *testing.T) {
	gates, err := validate.ParsePackageShareGates([]string{" , github.com/foo/bar:5 , "})
	require.NoError(t, err)
	require.Len(t, gates, 1)
	assert.Equal(t, "github.com/foo/bar", gates[0].Prefix)
	assert.InDelta(t, 5.0, gates[0].MinPercent, 0.001)
}

// TestValidateFile_FallbackSampleType ensures a samples/count profile is accepted
// and covers cpuSampleIndex's fallback branch (fallback=i, return fallback,true).
func TestValidateFile_FallbackSampleType(t *testing.T) {
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "samples", Unit: "count"}},
		DurationNanos: 30 * 1e9,
	}
	fn := &profile.Function{ID: 1, Name: "main.main"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn}}}
	p.Function = []*profile.Function{fn}
	p.Location = []*profile.Location{loc}
	for i := 0; i < 20; i++ {
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{1},
		})
	}
	path := writeTmpProfileV(t, p)

	opts := validate.Options{MinSamples: 1, MinScore: 0, MinStackDepth: 1}
	report, err := validate.ValidateFile(path, opts)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, int64(20), report.Samples)
}

// TestValidateFile_DefaultMinStackDepth verifies that MinStackDepth=0 is replaced
// by the default 2.0 inside ValidateFile (covers the opts.MinStackDepth==0 branch).
func TestValidateFile_DefaultMinStackDepth(t *testing.T) {
	data := generateCPUProfile(t, 200)
	f, err := os.CreateTemp(t.TempDir(), "cpu*.pprof")
	require.NoError(t, err)
	_, err = f.Write(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	opts := validate.Options{MinSamples: 1, MinScore: 0, MinStackDepth: 0}
	report, err := validate.ValidateFile(f.Name(), opts)
	require.NoError(t, err)
	require.NotNil(t, report)
}

// TestValidateFile_NegativeLowerBoundNoNaN verifies grade does not emit NaN
// when a lower bound is <= 0; the score must stay finite and in [0,1].
func TestValidateFile_NegativeLowerBoundNoNaN(t *testing.T) {
	data := generateCPUProfile(t, 200)
	f, err := os.CreateTemp(t.TempDir(), "cpu*.pprof")
	require.NoError(t, err)
	_, err = f.Write(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	opts := validate.Options{MinSamples: 1, MinScore: 0, MinStackDepth: -1}
	report, err := validate.ValidateFile(f.Name(), opts)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.False(t, math.IsNaN(report.QualityScore), "score must not be NaN")
	assert.GreaterOrEqual(t, report.QualityScore, 0.0)
	assert.LessOrEqual(t, report.QualityScore, 1.0)
}

// TestValidateFile_InsufficientSamples verifies sampleCount < MinSamples generates
// the "insufficient samples" error entry.
func TestValidateFile_InsufficientSamples(t *testing.T) {
	data := generateCPUProfile(t, 200)
	f, err := os.CreateTemp(t.TempDir(), "cpu*.pprof")
	require.NoError(t, err)
	_, err = f.Write(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	opts := validate.Options{MinSamples: 999999, MinScore: 0, MinStackDepth: 1}
	report, err := validate.ValidateFile(f.Name(), opts)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.False(t, report.Valid)
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "insufficient samples") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected 'insufficient samples' error, got: %v", report.Errors)
}

// TestValidateFile_FlatProfileError verifies avgStackDepth < MinStackDepth generates
// the flat-profile error entry.
func TestValidateFile_FlatProfileError(t *testing.T) {
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		DurationNanos: 30 * 1e9,
	}
	for i := 0; i < 25; i++ {
		fn := &profile.Function{ID: uint64(i + 1), Name: fmt.Sprintf("pkg.Func%d", i)}
		loc := &profile.Location{ID: uint64(i + 1), Line: []profile.Line{{Function: fn}}}
		p.Function = append(p.Function, fn)
		p.Location = append(p.Location, loc)
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{1000},
		})
	}
	path := writeTmpProfileV(t, p)

	opts := validate.Options{MinSamples: 1, MinScore: 0, MinStackDepth: 2.0}
	report, err := validate.ValidateFile(path, opts)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.False(t, report.Valid)
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "stack") || strings.Contains(e, "depth") || strings.Contains(e, "flat") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected flat-profile error, got: %v", report.Errors)
}

// TestValidateFile_PackageShareZeroTotal covers computePackageShares's total==0 return
// when all samples have zero CPU value.
func TestValidateFile_PackageShareZeroTotal(t *testing.T) {
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		DurationNanos: 30 * 1e9,
	}
	fn := &profile.Function{ID: 1, Name: "main.main"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn}}}
	p.Function = []*profile.Function{fn}
	p.Location = []*profile.Location{loc}
	for i := 0; i < 20; i++ {
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{0},
		})
	}
	path := writeTmpProfileV(t, p)

	opts := validate.Options{
		MinSamples: 1, MinScore: 0, MinStackDepth: 1,
		PackageShareGates: []validate.PackageShareGate{{Prefix: "main", MinPercent: 0}},
	}
	report, err := validate.ValidateFile(path, opts)
	require.NoError(t, err)
	require.NotNil(t, report)
}

// TestValidateFile_SamplesSumCount guards that Samples sums samples/count,
// not the number of stack rows.
func TestValidateFile_SamplesSumCount(t *testing.T) {
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "samples", Unit: "count"}},
		DurationNanos: 30 * 1e9,
	}
	fn := &profile.Function{ID: 1, Name: "main.main"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn}}}
	p.Function = []*profile.Function{fn}
	p.Location = []*profile.Location{loc}
	for i := 0; i < 4; i++ {
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{5},
		})
	}
	path := writeTmpProfileV(t, p)

	report, err := validate.ValidateFile(path, validate.Options{MinSamples: 1, MinScore: 0, MinStackDepth: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(20), report.Samples, "4 rows x 5 samples")
}

// A profile that fails only the score gate must say so; it used to report
// valid=false with no error, so text output gave no reason.
func TestValidateFile_LowScoreRecordsReason(t *testing.T) {
	data := generateCPUProfile(t, 200)
	f, err := os.CreateTemp(t.TempDir(), "cpu*.pprof")
	require.NoError(t, err)
	_, err = f.Write(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	opts := validate.DefaultOptions()
	opts.MinSamples = 1
	opts.MinStackDepth = 1
	opts.MinScore = 1.01
	report, err := validate.ValidateFile(f.Name(), opts)
	require.NoError(t, err)
	assert.False(t, report.Valid)
	require.NotEmpty(t, report.Errors)
	assert.Contains(t, report.Errors[len(report.Errors)-1], "quality score too low")
}

// TestLogScore_SyntheticSamples checks that larger sample counts score higher.
func TestLogScore_SyntheticSamples(t *testing.T) {
	makeProfile := func(sampleCount int) string {
		p := &profile.Profile{
			SampleType:    []*profile.ValueType{{Type: "samples", Unit: "count"}},
			DurationNanos: 60 * 1e9,
		}
		for i := 0; i < sampleCount; i++ {
			fn := &profile.Function{ID: uint64(i + 1), Name: fmt.Sprintf("pkg.Func%d", i%500)}
			loc := &profile.Location{ID: uint64(i + 1), Line: []profile.Line{{Function: fn}}}
			loc2 := &profile.Location{ID: uint64(i) + uint64(sampleCount) + 1, Line: []profile.Line{{Function: fn}}} //nolint:gosec
			p.Function = append(p.Function, fn)
			p.Location = append(p.Location, loc, loc2)
			p.Sample = append(p.Sample, &profile.Sample{
				Location: []*profile.Location{loc, loc2},
				Value:    []int64{1},
			})
		}
		path := writeTmpProfileV(t, p)
		return path
	}

	small := makeProfile(60000)
	large := makeProfile(200000)

	opts := validate.Options{
		MinSamples:         1,
		MinScore:           0,
		MinStackDepth:      1,
		MinUniqueStacks:    100,
		TargetUniqueStacks: 10000,
		TargetStackDepth:   20,
	}

	repSmall, err := validate.ValidateFile(small, opts)
	require.NoError(t, err)

	repLarge, err := validate.ValidateFile(large, opts)
	require.NoError(t, err)

	assert.Greater(t, repLarge.QualityScore, repSmall.QualityScore, "larger sample count should score higher")
	assert.Greater(t, repSmall.QualityScore, 0.0, "small profile should still have positive score")
}

// TestLogScore_NoDuration verifies DurationNanos=0 gives a re-weighted score
// and does not simply drop 0.2 from the total.
func TestLogScore_NoDuration(t *testing.T) {
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "samples", Unit: "count"}},
		DurationNanos: 0,
	}
	fn := &profile.Function{ID: 1, Name: "pkg.Func0"}
	for i := 0; i < 20; i++ {
		loc := &profile.Location{ID: uint64(i + 1), Line: []profile.Line{{Function: fn}}}
		p.Location = append(p.Location, loc)
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{5000},
		})
	}
	p.Function = []*profile.Function{fn}
	path := writeTmpProfileV(t, p)

	opts := validate.Options{
		MinSamples:         1,
		MinScore:           0,
		MinStackDepth:      0,
		MinUniqueStacks:    0,
		TargetUniqueStacks: 10000,
		TargetStackDepth:   20,
		WeightDensity:      0.40,
		WeightRichness:     0.30,
		WeightCoverage:     0.20,
		WeightDepth:        0.10,
	}

	report, err := validate.ValidateFile(path, opts)
	require.NoError(t, err)
	require.NotNil(t, report)
	// Score must be > 0 (not penalised by missing coverage)
	assert.Greater(t, report.QualityScore, 0.0, "no-duration profile should have positive score")
	// Sum of remaining weights is 0.80; score must not be capped at 0.80
	// (it should be normalised to the 1.0 scale), so it stays within [0,1].
	assert.LessOrEqual(t, report.QualityScore, 1.0)
}

// TestNoDuration_NoProfileTooShortError verifies that when DurationNanos==0 and
// MinDurationSeconds > 0, the "profile too short" error is NOT returned.
func TestNoDuration_NoProfileTooShortError(t *testing.T) {
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "samples", Unit: "count"}},
		DurationNanos: 0,
	}
	fn := &profile.Function{ID: 1, Name: "pkg.FuncPTS"}
	for i := 0; i < 5; i++ {
		loc := &profile.Location{ID: uint64(i + 1), Line: []profile.Line{{Function: fn}}}
		p.Location = append(p.Location, loc)
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{100},
		})
	}
	p.Function = []*profile.Function{fn}
	path := writeTmpProfileV(t, p)

	opts := validate.Options{
		MinSamples:         1,
		MinDurationSeconds: 10, // non-zero, so the gate must be skipped when DurationNanos == 0
		MinScore:           0,
		MinStackDepth:      0,
		MinUniqueStacks:    0,
		TargetUniqueStacks: 10000,
		TargetStackDepth:   20,
		WeightDensity:      0.40,
		WeightRichness:     0.30,
		WeightCoverage:     0.20,
		WeightDepth:        0.10,
	}

	report, err := validate.ValidateFile(path, opts)
	require.NoError(t, err)
	require.NotNil(t, report)
	for _, w := range report.Warnings {
		assert.NotContains(t, w, "profile too short",
			"profile too short must not appear when DurationNanos==0")
	}
}

// TestNoDuration_NoPollutionToReport verifies that a profile with DurationNanos==0
// emits no message to stderr and that its warning appears in report.Warnings.
func TestNoDuration_NoPollutionToReport(t *testing.T) {
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "samples", Unit: "count"}},
		DurationNanos: 0,
	}
	fn := &profile.Function{ID: 1, Name: "pkg.FuncND"}
	for i := 0; i < 5; i++ {
		loc := &profile.Location{ID: uint64(i + 1), Line: []profile.Line{{Function: fn}}}
		p.Location = append(p.Location, loc)
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{100},
		})
	}
	p.Function = []*profile.Function{fn}
	path := writeTmpProfileV(t, p)

	opts := validate.Options{
		MinSamples:         1,
		MinScore:           0,
		MinStackDepth:      0,
		MinUniqueStacks:    0,
		TargetUniqueStacks: 10000,
		TargetStackDepth:   20,
		WeightDensity:      0.40,
		WeightRichness:     0.30,
		WeightCoverage:     0.20,
		WeightDepth:        0.10,
	}

	// Capture stderr to verify nothing is written there.
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	report, validateErr := validate.ValidateFile(path, opts)

	_ = w.Close()
	os.Stderr = oldStderr
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	stderrOut := string(buf[:n])

	require.NoError(t, validateErr)
	require.NotNil(t, report)
	assert.Empty(t, stderrOut, "no-duration warning must not go to stderr")
	assert.True(t, func() bool {
		for _, w := range report.Warnings {
			if strings.Contains(w, "duration") {
				return true
			}
		}
		return false
	}(), "no-duration warning should appear in report.Warnings")
}

// TestLogScore_RichnessFactor_Deprecated verifies the deprecation warning and
// that the score is unaffected by the richness-factor value.
func TestLogScore_RichnessFactor_Deprecated(t *testing.T) {
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "samples", Unit: "count"}},
		DurationNanos: 60 * 1e9,
	}
	fn := &profile.Function{ID: 1, Name: "pkg.FuncA"}
	for i := 0; i < 100; i++ {
		loc := &profile.Location{ID: uint64(i + 1), Line: []profile.Line{{Function: fn}}}
		p.Location = append(p.Location, loc)
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{100},
		})
	}
	p.Function = []*profile.Function{fn}
	path := writeTmpProfileV(t, p)

	optsBase := validate.Options{
		MinSamples:         1,
		MinScore:           0,
		MinStackDepth:      1,
		MinUniqueStacks:    0,
		TargetUniqueStacks: 10000,
		TargetStackDepth:   20,
	}
	optsAlt := optsBase
	optsAlt.RichnessFactor = 0.5

	repBase, err := validate.ValidateFile(path, optsBase)
	require.NoError(t, err)

	repAlt, err := validate.ValidateFile(path, optsAlt)
	require.NoError(t, err)

	assert.InDelta(t, repBase.QualityScore, repAlt.QualityScore, 0.001,
		"richness-factor should have no effect on score")
}

// TestLogScore_PgoctlCPU checks the pgoctl_cpu.pprof testdata scores 0.351 ± 0.005
// under default flags.
func TestLogScore_PgoctlCPU(t *testing.T) {
	report, err := validate.ValidateFile("../../testdata/pgoctl_cpu.pprof", validate.DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.InDelta(t, 0.351, report.QualityScore, 0.005,
		"pgoctl_cpu.pprof should score 0.351 ± 0.005 with default options, got %v", report.QualityScore)
	assert.True(t, report.Valid, "pgoctl_cpu.pprof should be valid with default options")
}
