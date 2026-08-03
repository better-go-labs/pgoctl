package validate

import (
	"os"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

// Test-only helpers for the package-share feature. They are deliberately
// isolated in their own _test.go file (Go only compiles test-only code from
// *_test.go files), separate from the tests themselves in package_share_test.go.

// syntheticCPUProfile builds an in-memory CPU profile with the given
// leafFunction -> sampleValue (cpu nanoseconds) attribution. IDs are set
// explicitly so the profile survives a write -> parse round-trip.
func syntheticCPUProfile(t *testing.T, funcs map[string]int64) *profile.Profile {
	t.Helper()
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
	}
	var fnID, locID uint64 = 1, 1
	fns := make(map[string]*profile.Function)
	locs := make(map[string]*profile.Location)
	for name := range funcs {
		fn := &profile.Function{ID: fnID, Name: name}
		fnID++
		loc := &profile.Location{ID: locID, Line: []profile.Line{{Function: fn}}}
		locID++
		fns[name] = fn
		locs[name] = loc
		p.Function = append(p.Function, fn)
		p.Location = append(p.Location, loc)
	}
	for name, val := range funcs {
		p.Sample = append(p.Sample, &profile.Sample{
			Value:    []int64{val},
			Location: []*profile.Location{locs[name]},
		})
	}
	return p
}

// writeTempProfile serializes p to a temp file and returns its path.
func writeTempProfile(t *testing.T, p *profile.Profile) string {
	t.Helper()
	f, err := os.CreateTemp("", "gate*.pprof")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })
	require.NoError(t, p.Write(f))
	require.NoError(t, f.Close())
	return f.Name()
}

// permissiveOptions returns Options that pass on any well-formed profile, so
// package-share gates are the only thing under test.
func permissiveOptions() Options {
	return Options{
		MinSamples:         1,
		MinDurationSeconds: 0,
		MinScore:           0,
		MinStackDepth:      1.0,
		PackageShareGates:  nil,
	}
}
