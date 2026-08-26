package compare

import (
	"testing"

	"github.com/google/pprof/profile"
)

// TestFlatPercents_EmptyAndZeroSamples tests flatPercents with empty and zero-value samples.
// Covers the continue blocks for empty location/value and early return for zero total.
func TestFlatPercents_EmptyAndZeroSamples(t *testing.T) {
	fn := &profile.Function{ID: 1, Name: "main.f"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn}}}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		Function:   []*profile.Function{fn},
		Location:   []*profile.Location{loc},
		Sample: []*profile.Sample{
			{Location: nil, Value: nil},                             // hits empty-location/value continue
			{Location: []*profile.Location{loc}, Value: []int64{0}}, // total stays 0 → early return
		},
	}
	rpt := compareProfiles(p, p, DefaultGateConfig())
	if rpt == nil {
		t.Fatal("expected non-nil report")
	}
}
