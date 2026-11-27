package store

import (
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeCPUProfile builds a minimal CPU profile whose cpu/nanoseconds total is
// cpu. It mirrors the storetest synthetic profile but lives in the internal test
// so the package-private normalize and cpuTotal can be driven directly without an
// import cycle through storetest.
func makeCPUProfile(cpu int64) *profile.Profile {
	fn := &profile.Function{ID: 1, Name: "work", Filename: "work.go"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn, Line: 1}}}
	return &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		Sample: []*profile.Sample{
			{Value: []int64{1, cpu}, Location: []*profile.Location{loc}},
		},
		Function: []*profile.Function{fn},
		Location: []*profile.Location{loc},
	}
}

// makeNoCPUProfile builds a profile with no cpu sample type, so cpuTotal reports
// zero for it.
func makeNoCPUProfile() *profile.Profile {
	fn := &profile.Function{ID: 1, Name: "work", Filename: "work.go"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn, Line: 1}}}
	return &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "alloc_space", Unit: "bytes"}},
		Sample: []*profile.Sample{
			{Value: []int64{42}, Location: []*profile.Location{loc}},
		},
		Function: []*profile.Function{fn},
		Location: []*profile.Location{loc},
	}
}

func TestNormalize_SingleProfile(t *testing.T) {
	p := makeCPUProfile(1000)
	normalize([]*profile.Profile{p})
	assert.Equal(t, int64(1000), cpuTotal(p), "a single profile is already its own maximum and must not be scaled")
}

func TestNormalize_EqualTotals(t *testing.T) {
	a := makeCPUProfile(1000)
	b := makeCPUProfile(1000)
	normalize([]*profile.Profile{a, b})
	assert.Equal(t, int64(1000), cpuTotal(a))
	assert.Equal(t, int64(1000), cpuTotal(b), "profiles with equal totals stay unchanged")
}

func TestNormalize_ScalesToMax(t *testing.T) {
	small := makeCPUProfile(100)
	large := makeCPUProfile(1000)
	normalize([]*profile.Profile{small, large})
	assert.Equal(t, int64(1000), cpuTotal(large), "the largest profile is the target and stays put")
	assert.Equal(t, int64(1000), cpuTotal(small), "the smaller profile is scaled up to the maximum total")
}

func TestNormalize_AllZeroIsNoop(t *testing.T) {
	// A profile carrying no CPU samples has a zero total; with every total zero,
	// normalize has no maximum to scale toward and must leave the inputs alone.
	p := makeNoCPUProfile()
	require.Equal(t, int64(0), cpuTotal(p))
	normalize([]*profile.Profile{p})
	assert.Equal(t, int64(0), cpuTotal(p))
}
