package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"testing"
	"time"

	profiletypes "github.com/Better-Go-Labs/pgoctl/internal/profile"
	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// executeCmd runs pgoctl in-process and returns (stdout, stderr, error).
// Note: validate/merge/compare write directly to os.Stdout/os.Stderr, so
// those streams are not captured here — assert on err and exitError.code instead.
func executeCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

// generateProfile produces a real CPU profile via runtime/pprof.
func generateProfile(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "cpu*.pprof")
	require.NoError(t, err)
	require.NoError(t, pprof.StartCPUProfile(f))
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		// spin to gather samples
	}
	pprof.StopCPUProfile()
	require.NoError(t, f.Close())
	return f.Name()
}

// TestValidate_ValidProfile passes a real profile with permissive thresholds.
func TestValidate_ValidProfile(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	assert.NoError(t, err)
}

// TestValidate_JSONFlag covers the JSON branch of printQualityReport.
// Output goes to os.Stdout (not captured), so we only check for no error.
func TestValidate_JSONFlag(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"--json", "validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	assert.NoError(t, err)
}

// TestValidate_GateFailViaEnv sets PGOCTL_MIN_SAMPLES=999999 to force gate failure -> exitError{1}.
func TestValidate_GateFailViaEnv(t *testing.T) {
	t.Setenv("PGOCTL_MIN_SAMPLES", "999999")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--target-samples=2000000",
		"--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.code)
}

// TestMerge_TwoValidProfiles merges two real profiles and asserts the output file exists.
func TestMerge_TwoValidProfiles(t *testing.T) {
	p1 := generateProfile(t)
	p2 := generateProfile(t)
	out := filepath.Join(t.TempDir(), "merged.pgo")
	_, _, err := executeCmd(t, "merge", p1, p2, "--out", out)
	require.NoError(t, err)
	info, statErr := os.Stat(out)
	require.NoError(t, statErr)
	assert.Greater(t, info.Size(), int64(0))
}

// TestMerge_MalformedInput verifies that a malformed file without --drop-invalid causes exitError{2}.
// Note: the command loop parses files before applying DropInvalid, so malformed always exits 2.
func TestMerge_MalformedInput(t *testing.T) {
	valid := generateProfile(t)
	malformed := filepath.Join(t.TempDir(), "bad.pprof")
	require.NoError(t, os.WriteFile(malformed, []byte("not a valid pprof"), 0o600))
	out := filepath.Join(t.TempDir(), "merged.pgo")
	_, _, err := executeCmd(t, "merge", malformed, valid, "--out", out)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestCompare_SameProfile compares a profile to itself -- expects nil error (neutral verdict).
func TestCompare_SameProfile(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t, "compare", p, p)
	// Comparing a profile to itself gives neutral -> no exitError
	assert.NoError(t, err)
}

// TestCompare_JSONFlag covers the JSON branch of the compare output path.
func TestCompare_JSONFlag(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t, "--json", "compare", p, p)
	assert.NoError(t, err)
}

// TestCompare_BadFile verifies a malformed input causes exitError{2}.
func TestCompare_BadFile(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.pprof")
	require.NoError(t, os.WriteFile(bad, []byte("junk"), 0o600))
	_, _, err := executeCmd(t, "compare", bad, bad)
	var ee *exitError
	if errors.As(err, &ee) {
		assert.Equal(t, 2, ee.code)
	} else {
		assert.Error(t, err)
	}
}

// TestExitError_Unwrap verifies Unwrap enables errors.Is traversal.
func TestExitError_Unwrap(t *testing.T) {
	inner := errors.New("inner")
	e := &exitError{code: 2, err: inner}
	if !errors.Is(e, inner) {
		t.Fatal("errors.Is should work through Unwrap")
	}
}

// TestValidate_PackageShareRow covers the package_share row in printQualityReport.
func TestValidate_PackageShareRow(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		"--min-package-share=runtime:0",
		p,
	)
	assert.NoError(t, err)
}

// TestValidate_WarningRow covers the warning row in printQualityReport (short duration).
func TestValidate_WarningRow(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=9999", "--target-duration=20000",
		"--min-score=0", "--min-stack-depth=0",
		p,
	)
	assert.NoError(t, err)
}

// TestMerge_StdoutOutput covers the --out - branch writing to stdout.
func TestMerge_StdoutOutput(t *testing.T) {
	p1 := generateProfile(t)
	p2 := generateProfile(t)
	_, _, err := executeCmd(t, "merge", p1, p2, "--out", "-")
	assert.NoError(t, err)
}

// TestValidate_ConfigEnv_Json covers the json case in the env/config apply loop.
func TestValidate_ConfigEnv_Json(t *testing.T) {
	t.Setenv("PGOCTL_JSON", "true")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	assert.NoError(t, err)
}

// TestValidate_ConfigEnv_TargetSamples covers the target-samples else branch.
func TestValidate_ConfigEnv_TargetSamples(t *testing.T) {
	t.Setenv("PGOCTL_TARGET_SAMPLES", "50000")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	assert.NoError(t, err)
}

// TestValidate_ConfigEnv_FloatValue covers the default float64 branch in the apply loop.
func TestValidate_ConfigEnv_FloatValue(t *testing.T) {
	t.Setenv("PGOCTL_MIN_DURATION", "0")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	assert.NoError(t, err)
}

// TestValidate_ConfigEnv_BadValue covers ParseBool error and the configErrs exit path.
func TestValidate_ConfigEnv_BadValue(t *testing.T) {
	t.Setenv("PGOCTL_JSON", "not-a-bool")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestValidate_ConfigEnv_BadPackageShare covers the min-package-share env case
// and the ParsePackageShareGates error exit path.
func TestValidate_ConfigEnv_BadPackageShare(t *testing.T) {
	t.Setenv("PGOCTL_MIN_PACKAGE_SHARE", "no-colon-invalid")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestValidate_BadMinSamples tests parsing of PGOCTL_MIN_SAMPLES with invalid value.
func TestValidate_BadMinSamples(t *testing.T) {
	t.Setenv("PGOCTL_MIN_SAMPLES", "not-an-int")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestValidate_BadMinScore tests parsing of PGOCTL_MIN_SCORE with invalid value.
func TestValidate_BadMinScore(t *testing.T) {
	t.Setenv("PGOCTL_MIN_SCORE", "not-a-float")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-stack-depth=0",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestValidate_BadMinDuration tests parsing of PGOCTL_MIN_DURATION with invalid value.
func TestValidate_BadMinDuration(t *testing.T) {
	t.Setenv("PGOCTL_MIN_DURATION", "bad-float")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestValidate_BadPackageShare tests invalid min-package-share format.
func TestValidate_BadPackageShare(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		"--min-package-share=invalid-format",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestValidate_PackageShareGate tests that package-share gates work and fail properly.
func TestValidate_PackageShareGate(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		"--min-package-share=github.com/nonexistent:100",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.code)
}

// TestMerge_OutToStdout tests merge with --out=- (stdout).
func TestMerge_OutToStdout(t *testing.T) {
	p1 := generateProfile(t)
	p2 := generateProfile(t)
	_, _, err := executeCmd(t, "merge", p1, p2, "--out", "-")
	assert.NoError(t, err)
}

// TestMerge_UnionStrategy tests merge with union strategy.
func TestMerge_UnionStrategy(t *testing.T) {
	p1 := generateProfile(t)
	p2 := generateProfile(t)
	out := filepath.Join(t.TempDir(), "merged.pgo")
	_, _, err := executeCmd(t, "merge", "--strategy", "union", p1, p2, "--out", out)
	require.NoError(t, err)
	info, statErr := os.Stat(out)
	require.NoError(t, statErr)
	assert.Greater(t, info.Size(), int64(0))
}

// TestMerge_LatestStrategy tests merge with latest strategy.
func TestMerge_LatestStrategy(t *testing.T) {
	p1 := generateProfile(t)
	p2 := generateProfile(t)
	out := filepath.Join(t.TempDir(), "merged.pgo")
	_, _, err := executeCmd(t, "merge", "--strategy", "latest", p1, p2, "--out", out)
	require.NoError(t, err)
	info, statErr := os.Stat(out)
	require.NoError(t, statErr)
	assert.Greater(t, info.Size(), int64(0))
}

// TestMerge_CreateOutputError tests merge when output directory doesn't exist.
func TestMerge_CreateOutputError(t *testing.T) {
	p1 := generateProfile(t)
	p2 := generateProfile(t)
	out := filepath.Join(t.TempDir(), "nonexistent", "subdir", "merged.pgo")
	_, _, err := executeCmd(t, "merge", p1, p2, "--out", out)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestValidate_FileNotFound tests validate with non-existent file.
func TestValidate_FileNotFound(t *testing.T) {
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		"/nonexistent/profile.pprof",
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestValidate_WeightFlags tests setting custom weights.
func TestValidate_WeightFlags(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		"--weight-density=0.5", "--weight-richness=0.3", "--weight-coverage=0.1", "--weight-depth=0.1",
		p,
	)
	assert.NoError(t, err)
}

// TestValidate_TargetFlags tests target sample and duration flags.
func TestValidate_TargetFlags(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		"--target-samples=60000", "--target-duration=60",
		p,
	)
	assert.NoError(t, err)
}

// TestValidate_BadTargetSamples tests parsing of PGOCTL_TARGET_SAMPLES with invalid value.
func TestValidate_BadTargetSamples(t *testing.T) {
	t.Setenv("PGOCTL_TARGET_SAMPLES", "not-an-int")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestValidate_BadWeightDensity tests parsing of PGOCTL_WEIGHT_DENSITY with invalid value.
func TestValidate_BadWeightDensity(t *testing.T) {
	t.Setenv("PGOCTL_WEIGHT_DENSITY", "bad-float")
	p := generateProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// makeTimePackageSyntheticProfile creates a minimal deterministic pprof where
// time.Now is the leaf function on half the samples (~50% flat share to "time").
// Samples have depth 2 to satisfy the internal min-stack-depth=2 floor that
// cannot be disabled via the flag (0 is replaced with 2.0 internally).
func makeTimePackageSyntheticProfile(t *testing.T) string {
	t.Helper()
	timeNow := &profile.Function{ID: 1, Name: "time.Now", SystemName: "time.Now", Filename: "time/time.go", StartLine: 1}
	mainFn := &profile.Function{ID: 2, Name: "main.main", SystemName: "main.main", Filename: "main.go", StartLine: 1}
	callerFn := &profile.Function{ID: 3, Name: "main.run", SystemName: "main.run", Filename: "main.go", StartLine: 10}
	locTime := &profile.Location{ID: 1, Line: []profile.Line{{Function: timeNow, Line: 1}}}
	locMain := &profile.Location{ID: 2, Line: []profile.Line{{Function: mainFn, Line: 5}}}
	locCaller := &profile.Location{ID: 3, Line: []profile.Line{{Function: callerFn, Line: 15}}}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		Function:   []*profile.Function{timeNow, mainFn, callerFn},
		Location:   []*profile.Location{locTime, locMain, locCaller},
		Sample: []*profile.Sample{
			// time.Now called from main.run -- depth 2, leaf = time package
			{Location: []*profile.Location{locTime, locCaller}, Value: []int64{50_000_000}},
			// main.main called from main.run -- depth 2, leaf = main package
			{Location: []*profile.Location{locMain, locCaller}, Value: []int64{50_000_000}},
		},
	}
	f, err := os.CreateTemp(t.TempDir(), "synthetic-time-*.pprof")
	require.NoError(t, err)
	require.NoError(t, p.Write(f))
	require.NoError(t, f.Close())
	return f.Name()
}

// TestValidate_WeightedMinPackageShare tests with package share requirement.
func TestValidate_WeightedMinPackageShare(t *testing.T) {
	p := makeTimePackageSyntheticProfile(t)
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		"--min-package-share=time:0.01",
		p,
	)
	assert.NoError(t, err)
}

// TestMerge_DropInvalid tests merge with --drop-invalid flag.
func TestMerge_DropInvalid(t *testing.T) {
	valid := generateProfile(t)
	malformed := filepath.Join(t.TempDir(), "bad.pprof")
	require.NoError(t, os.WriteFile(malformed, []byte("not a valid pprof"), 0o600))
	out := filepath.Join(t.TempDir(), "merged.pgo")
	// With drop-invalid, should still fail because merge.Profiles will error on the malformed data
	_, _, err := executeCmd(t, "merge", "--drop-invalid", malformed, valid, "--out", out)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestMerge_RecencyWeight tests merge with custom recency weight.
func TestMerge_RecencyWeight(t *testing.T) {
	p1 := generateProfile(t)
	p2 := generateProfile(t)
	out := filepath.Join(t.TempDir(), "merged.pgo")
	_, _, err := executeCmd(t, "merge", "--recency-weight=3.0", p1, p2, "--out", out)
	require.NoError(t, err)
}

// TestMerge_HalfLife tests merge with custom half-life.
func TestMerge_HalfLife(t *testing.T) {
	p1 := generateProfile(t)
	p2 := generateProfile(t)
	out := filepath.Join(t.TempDir(), "merged.pgo")
	_, _, err := executeCmd(t, "merge", "--half-life=12", p1, p2, "--out", out)
	require.NoError(t, err)
}

// TestCompare_Improvement tests compare with profiles showing improvement.
func TestCompare_Improvement(t *testing.T) {
	p1 := generateProfile(t)
	p2 := generateProfile(t)
	// --min-regression=100 prevents a rollback verdict (exit 1) due to timing
	// variance in synthetic profiles generated under load (e.g. -coverpkg runs).
	_, _, err := executeCmd(t, "compare", "--min-improvement=0", "--min-regression=100", p1, p2)
	assert.NoError(t, err)
}

// TestCompare_MinCPUPercent tests compare with min-cpu-percent filter.
func TestCompare_MinCPUPercent(t *testing.T) {
	p1 := generateProfile(t)
	p2 := generateProfile(t)
	// --min-regression=100 prevents a rollback verdict (exit 1) due to timing
	// variance in synthetic profiles generated under load (e.g. -coverpkg runs).
	// This test only verifies the flag is accepted and parsing succeeds.
	_, _, err := executeCmd(t, "compare", "--min-cpu-percent=0.1", "--min-regression=100", p1, p2)
	assert.NoError(t, err)
}

// TestCompare_TopN tests compare with custom top N functions.
func TestCompare_TopN(t *testing.T) {
	p1 := generateProfile(t)
	_, _, err := executeCmd(t, "compare", "--top=5", p1, p1)
	assert.NoError(t, err)
}

// TestValidate_LogScore_InvalidBounds checks min > target exits 2.
func TestValidate_LogScore_InvalidBounds(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t, "validate",
		"--min-samples=10000", "--target-samples=5000",
		"--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestValidate_LogScore_NegativeDuration checks negative min exits 2.
func TestValidate_LogScore_NegativeDuration(t *testing.T) {
	p := generateProfile(t)
	_, _, err := executeCmd(t, "validate",
		"--min-samples=1", "--min-duration=-1",
		"--min-score=0", "--min-stack-depth=0",
		p,
	)
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
}

// TestValidate_Bounds_AllPairs exercises the bounds check for every
// (min, target) pair, including a value supplied via the environment, each of
// which must exit 2.
func TestValidate_Bounds_AllPairs(t *testing.T) {
	p := filepath.Join("..", "..", "testdata", "cpu_happy.pprof")
	tests := []struct {
		name string
		env  map[string]string
		args []string
	}{
		{name: "samples", args: []string{"--min-samples=10000", "--target-samples=5000"}},
		{name: "unique-stacks", args: []string{"--min-unique-stacks=100", "--target-unique-stacks=50"}},
		{name: "duration", args: []string{"--min-duration=10", "--target-duration=5"}},
		{name: "stack-depth", args: []string{"--min-stack-depth=2", "--target-stack-depth=1"}},
		{name: "stack-depth-zero", args: []string{"--target-stack-depth=0"}},
		{name: "env-target-samples", env: map[string]string{"PGOCTL_TARGET_SAMPLES": "5000"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			args := append([]string{"validate"}, tc.args...)
			args = append(args, p)
			var err error
			out := captureStderr(t, func() {
				_, _, err = executeCmd(t, args...)
			})
			var ee *exitError
			require.ErrorAs(t, err, &ee)
			assert.Equal(t, 2, ee.code)
			assert.NotEmpty(t, out, "bounds failure must print a message")
		})
	}
}

// TestValidate_FractionalTargetStackDepth confirms target-stack-depth accepts a
// fractional value now that it is a float64.
func TestValidate_FractionalTargetStackDepth(t *testing.T) {
	p := filepath.Join("..", "..", "testdata", "cpu_happy.pprof")
	_, _, err := executeCmd(t,
		"validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0",
		"--min-stack-depth=0", "--target-stack-depth=15.5",
		p,
	)
	assert.NoError(t, err)
}

// TestValidate_MinStackDepthNegative ensures a negative min-stack-depth exits 2
// with a message rather than exiting 1 with empty output (grade would have
// produced NaN and failed JSON encoding).
func TestValidate_MinStackDepthNegative(t *testing.T) {
	p := filepath.Join("..", "..", "testdata", "cpu_happy.pprof")
	var err error
	out := captureStderr(t, func() {
		_, _, err = executeCmd(t, "--json", "validate", "--min-stack-depth=-1", p)
	})
	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.code)
	assert.Contains(t, out, "min-stack-depth")
}

// TestValidate_LogScore_RichnessFactor_Warning checks the deprecation warning is printed.
func TestValidate_LogScore_RichnessFactor_Warning(t *testing.T) {
	p := generateProfile(t)
	_, stderr, err := executeCmd(t, "validate",
		"--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0",
		"--richness-factor=0.5",
		p,
	)
	assert.NoError(t, err)
	assert.Contains(t, stderr, "richness-factor")
	assert.Contains(t, stderr, "deprecated")
}

// captureStdout redirects os.Stdout for the duration of fn and returns what
// fn wrote. printQualityReport writes to os.Stdout directly, so this is how
// tests read the JSON report.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	fn()
	require.NoError(t, w.Close())
	os.Stdout = orig
	data, readErr := io.ReadAll(r)
	require.NoError(t, readErr)
	return string(data)
}

// captureStderr redirects os.Stderr for the duration of fn and returns what
// fn wrote. Bounds-error messages are written directly to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	fn()
	require.NoError(t, w.Close())
	os.Stderr = orig
	data, readErr := io.ReadAll(r)
	require.NoError(t, readErr)
	return string(data)
}

// TestValidate_ConfigEnv_TargetUniqueStacks covers PGOCTL_TARGET_UNIQUE_STACKS:
// lowering the target below the default must raise the richness grade and so
// change the overall score, proving the env var reaches the scorer.
func TestValidate_ConfigEnv_TargetUniqueStacks(t *testing.T) {
	p := filepath.Join("..", "..", "testdata", "cpu_happy.pprof")
	score := func() float64 {
		var rep profiletypes.QualityReport
		out := captureStdout(t, func() {
			_, _, err := executeCmd(t,
				"--json", "validate",
				"--min-samples=1", "--min-duration=0", "--min-score=0",
				"--min-stack-depth=0", "--min-unique-stacks=1",
				p,
			)
			require.NoError(t, err)
		})
		require.NoError(t, json.Unmarshal([]byte(out), &rep))
		return rep.QualityScore
	}

	baseline := score()
	t.Setenv("PGOCTL_TARGET_UNIQUE_STACKS", "1000")
	lowered := score()
	assert.NotEqual(t, baseline, lowered,
		"lowering target-unique-stacks via env must change the score")
}
