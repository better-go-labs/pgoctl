package validate

import (
	"os"
	"strings"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildProfile creates a synthetic pprof with cpu/nanoseconds sample type.
// Each entry describes a leaf function + optional call chain (leaf first, root last).
func buildProfile(t *testing.T, entries []struct {
	chain []string // leaf first; if nil, single-frame with fn
	fn    string   // leaf function name (used when chain is nil)
	value int64
}) *profile.Profile {
	t.Helper()
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		DurationNanos: 30 * 1e9,
	}
	fnCache := map[string]*profile.Function{}
	locCache := map[string]*profile.Location{}

	getFunc := func(name string) *profile.Function {
		if f, ok := fnCache[name]; ok {
			return f
		}
		f := &profile.Function{ID: uint64(len(fnCache) + 1), Name: name}
		fnCache[name] = f
		p.Function = append(p.Function, f)
		return f
	}
	getLoc := func(name string) *profile.Location {
		if l, ok := locCache[name]; ok {
			return l
		}
		l := &profile.Location{
			ID:   uint64(len(locCache) + 1),
			Line: []profile.Line{{Function: getFunc(name)}},
		}
		locCache[name] = l
		p.Location = append(p.Location, l)
		return l
	}

	for _, e := range entries {
		chain := e.chain
		if len(chain) == 0 {
			chain = []string{e.fn}
		}
		locs := make([]*profile.Location, len(chain))
		for i, name := range chain {
			locs[i] = getLoc(name)
		}
		p.Sample = append(p.Sample, &profile.Sample{
			Location: locs,
			Value:    []int64{e.value},
		})
	}
	return p
}

func writeTmpPprof(t *testing.T, p *profile.Profile) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "cpu*.pprof")
	require.NoError(t, err)
	require.NoError(t, p.Write(f))
	require.NoError(t, f.Close())
	return f.Name()
}

// ── CPUBoundFraction ────────────────────────────────────────────────────────

func TestCPUBoundFraction_AllUser(t *testing.T) {
	p := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "github.com/myapp/server.handleRequest", 600},
		{nil, "github.com/myapp/db.Query", 400},
	})
	frac := CPUBoundFraction(p)
	assert.InDelta(t, 1.0, frac, 0.001)
}

func TestCPUBoundFraction_AllRuntime(t *testing.T) {
	p := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "runtime.mallocgc", 500},
		{nil, "runtime.gcBgMarkWorker", 500},
	})
	assert.InDelta(t, 0.0, CPUBoundFraction(p), 0.001)
}

func TestCPUBoundFraction_Mixed(t *testing.T) {
	p := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "github.com/myapp/server.handleRequest", 700},
		{nil, "runtime.mallocgc", 300},
	})
	assert.InDelta(t, 0.70, CPUBoundFraction(p), 0.01)
}

func TestCPUBoundFraction_NoSamples(t *testing.T) {
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
	}
	assert.Equal(t, 0.0, CPUBoundFraction(p))
}

// ── ProfilePeakedness ───────────────────────────────────────────────────────

func TestProfilePeakedness_Concentrated(t *testing.T) {
	p := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "hot1", 800},
		{nil, "hot2", 100},
		{nil, "cold1", 50},
		{nil, "cold2", 50},
	})
	// Top-2: hot1(800)+hot2(100) = 900/1000 = 90%
	assert.InDelta(t, 90.0, ProfilePeakedness(p, 2), 0.1)
}

func TestProfilePeakedness_Uniform(t *testing.T) {
	p := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "fn1", 250},
		{nil, "fn2", 250},
		{nil, "fn3", 250},
		{nil, "fn4", 250},
	})
	assert.InDelta(t, 25.0, ProfilePeakedness(p, 1), 0.1)
}

func TestProfilePeakedness_TopNExceedsCount(t *testing.T) {
	p := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "fn1", 500},
		{nil, "fn2", 500},
	})
	assert.InDelta(t, 100.0, ProfilePeakedness(p, 100), 0.1)
}

// ── CrossPackageChainDensity ────────────────────────────────────────────────

func TestCrossPackageChainDensity_SamePackage(t *testing.T) {
	p := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{[]string{"github.com/myapp/server.handleRequest", "github.com/myapp/server.dispatch"}, "", 1},
		{[]string{"github.com/myapp/server.readBody", "github.com/myapp/server.parse"}, "", 1},
	})
	assert.InDelta(t, 0.0, CrossPackageChainDensity(p, 0), 0.001)
}

func TestCrossPackageChainDensity_CrossPackage(t *testing.T) {
	p := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		// cross-package: server → db
		{[]string{"github.com/myapp/server.handleRequest", "github.com/myapp/db.Query"}, "", 1},
		// same-package: db internal
		{[]string{"github.com/myapp/db.scan", "github.com/myapp/db.next"}, "", 1},
	})
	// 1 out of 2 chains is cross-package; equal weights → density = 0.5
	assert.InDelta(t, 0.5, CrossPackageChainDensity(p, 0), 0.001)
}

func TestCrossPackageChainDensity_HotThreshold(t *testing.T) {
	p := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		// High value, cross-package
		{[]string{"github.com/myapp/server.handleRequest", "github.com/myapp/db.Query"}, "", 900},
		// Very low value, same-package (below any reasonable hot threshold)
		{[]string{"github.com/myapp/util.helper", "github.com/myapp/util.fmt"}, "", 1},
	})
	// With threshold=50%, the second chain's leaf contributes <0.1% → below threshold
	// so only the first chain is counted; it is cross-package → density ≈ 1.0.
	density := CrossPackageChainDensity(p, 50.0)
	assert.InDelta(t, 1.0, density, 0.01)
}

// ── Integration: ValidateFile with workload signals ──────────────────────────

func TestValidateFile_WorkloadSignals_Populated(t *testing.T) {
	// Build a synthetic profile with enough samples to pass basic gates.
	// 10000 samples of [server.handleRequest→db.Query] at value=100 (cross-pkg)
	// 10000 samples of [runtime.mallocgc] at value=10
	// Total CPU = 10000*100 + 10000*10 = 1 100 000
	// CPUBoundFraction  = 1 000 000 / 1 100 000 = 0.9091
	// Peakedness(top-5) = 100.0 (top-5 covers both unique functions)
	// CrossPkgDensity   = 1 000 000 / 1 100 000 = 0.9091 (weighted; mallocgc chain has no boundary)
	entries := make([]struct {
		chain []string
		fn    string
		value int64
	}, 0, 20000)
	for i := 0; i < 10000; i++ {
		entries = append(entries, struct {
			chain []string
			fn    string
			value int64
		}{[]string{"github.com/myapp/server.handleRequest", "github.com/myapp/db.Query"}, "", 100})
		entries = append(entries, struct {
			chain []string
			fn    string
			value int64
		}{nil, "runtime.mallocgc", 10})
	}
	p := buildProfile(t, entries)
	p.DurationNanos = 30 * 1e9

	path := writeTmpPprof(t, p)

	opts := DefaultOptions()
	opts.MinSamples = 1
	opts.MinScore = 0.1
	opts.ComputeWorkloadSignals = true
	opts.PeakednessTopN = 5

	report, err := ValidateFile(path, opts)
	require.NoError(t, err)
	require.NotNil(t, report.WorkloadSignals)

	ws := report.WorkloadSignals
	assert.Equal(t, 0.9091, ws.CPUBoundFraction)
	assert.Equal(t, 100.0, ws.Peakedness)
	assert.Equal(t, 0.9091, ws.CrossPackageChainDensity)
}

func TestValidateFile_WorkloadSignals_Disabled(t *testing.T) {
	entries := []struct {
		chain []string
		fn    string
		value int64
	}{{nil, "github.com/myapp/server.Do", 100}}
	p := buildProfile(t, entries)
	path := writeTmpPprof(t, p)

	opts := DefaultOptions()
	opts.MinSamples = 1
	opts.MinScore = 0.0
	opts.ComputeWorkloadSignals = false

	report, err := ValidateFile(path, opts)
	require.NoError(t, err)
	assert.Nil(t, report.WorkloadSignals, "signals should not be computed when ComputeWorkloadSignals is false")
}

func TestValidateFile_WorkloadSignals_WarnOnThreshold(t *testing.T) {
	// Profile with all runtime → cpu_bound_fraction = 0 → should warn
	entries := make([]struct {
		chain []string
		fn    string
		value int64
	}, 10001)
	for i := range entries {
		entries[i] = struct {
			chain []string
			fn    string
			value int64
		}{nil, "runtime.mallocgc", 10}
	}
	p := buildProfile(t, entries)
	p.DurationNanos = 30 * 1e9
	path := writeTmpPprof(t, p)

	opts := DefaultOptions()
	opts.MinSamples = 1
	opts.MinScore = 0.0
	opts.ComputeWorkloadSignals = true
	opts.MinCPUBoundFraction = 0.5 // 50% threshold

	report, err := ValidateFile(path, opts)
	require.NoError(t, err)
	require.NotNil(t, report.WorkloadSignals)

	var hasCPUWarn bool
	for _, w := range report.Warnings {
		if strings.HasPrefix(w, "cpu_bound_fraction") {
			hasCPUWarn = true
		}
	}
	assert.True(t, hasCPUWarn, "expected cpu_bound_fraction warning when all-runtime profile")
}

// ── ProfileDriftPct ─────────────────────────────────────────────────────────

func TestProfileDriftPct_IdenticalProfiles(t *testing.T) {
	p := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "github.com/myapp/server.handleRequest", 600},
		{nil, "github.com/myapp/db.Query", 400},
	})
	// TVD between identical distributions is always 0.
	assert.Equal(t, 0.0, ProfileDriftPct(p, p))
}

func TestProfileDriftPct_DisjointProfiles(t *testing.T) {
	cur := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "github.com/myapp/server.handleRequest", 1000},
		{nil, "github.com/myapp/db.Query", 0},
	})
	ref := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "github.com/myapp/other.doWork", 500},
		{nil, "github.com/myapp/cache.Get", 500},
	})
	// No overlap → TVD = 1.0.
	assert.Equal(t, 1.0, ProfileDriftPct(cur, ref))
}

func TestProfileDriftPct_PartialOverlap(t *testing.T) {
	// cur: fn1=600, fn2=400 (normalized: fn1=0.6, fn2=0.4)
	// ref: fn1=400, fn3=600 (normalized: fn1=0.4, fn3=0.6)
	// TVD = 0.5 * (|0.6-0.4| + |0.4-0| + |0-0.6|) = 0.5*(0.2+0.4+0.6) = 0.6
	cur := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "fn1", 600},
		{nil, "fn2", 400},
	})
	ref := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "fn1", 400},
		{nil, "fn3", 600},
	})
	assert.InDelta(t, 0.6, ProfileDriftPct(cur, ref), 0.001)
}

func TestProfileDriftPct_SingleSample(t *testing.T) {
	p := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "fn1", 1000},
	})
	// n < 2 guard.
	assert.Equal(t, 0.0, ProfileDriftPct(p, p))
}

func TestProfileDriftPct_NoCPUSampleType(t *testing.T) {
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "alloc_space", Unit: "bytes"}},
	}
	ref := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "fn1", 1000},
		{nil, "fn2", 500},
	})
	assert.Equal(t, 0.0, ProfileDriftPct(p, ref))
}

// ── Integration: drift_pct via ValidateFile ──────────────────────────────────

func TestValidateFile_DriftPct_Populated(t *testing.T) {
	cur := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "github.com/myapp/server.handleRequest", 600},
		{nil, "github.com/myapp/db.Query", 400},
	})
	ref := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "github.com/myapp/server.handleRequest", 600},
		{nil, "github.com/myapp/db.Query", 400},
	})
	curPath := writeTmpPprof(t, cur)
	refPath := writeTmpPprof(t, ref)

	opts := DefaultOptions()
	opts.MinSamples = 1
	opts.MinScore = 0.0
	opts.ComputeWorkloadSignals = true
	opts.DriftAgainstPath = refPath

	report, err := ValidateFile(curPath, opts)
	require.NoError(t, err)
	require.NotNil(t, report.WorkloadSignals)
	// Identical distributions → drift = 0.
	assert.Equal(t, 0.0, report.WorkloadSignals.DriftPct)
}

func TestValidateFile_DriftPct_BadRefPath(t *testing.T) {
	cur := buildProfile(t, []struct {
		chain []string
		fn    string
		value int64
	}{
		{nil, "fn1", 500},
		{nil, "fn2", 500},
	})
	curPath := writeTmpPprof(t, cur)

	opts := DefaultOptions()
	opts.MinSamples = 1
	opts.MinScore = 0.0
	opts.ComputeWorkloadSignals = true
	opts.DriftAgainstPath = "/nonexistent/profile.pb.gz"

	report, err := ValidateFile(curPath, opts)
	require.NoError(t, err)
	require.NotNil(t, report.WorkloadSignals)
	// drift_pct skipped on I/O error → 0, and a warning is emitted.
	assert.Equal(t, 0.0, report.WorkloadSignals.DriftPct)
	var hasWarn bool
	for _, w := range report.Warnings {
		if strings.HasPrefix(w, "drift_pct skipped") {
			hasWarn = true
		}
	}
	assert.True(t, hasWarn, "expected drift_pct skipped warning for missing reference")
}
