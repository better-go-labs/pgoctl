package validate_test

import (
	"fmt"
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
				expected := float64(r.Samples) / 50000
				assert.InDelta(t, expected, r.QualityScore, 0.001, "score should equal density term")
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
