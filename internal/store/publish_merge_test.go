package store_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/pprofutil"
	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

// cpuProfileFn builds a single-sample CPU profile whose only function is fnName
// and whose cpu/nanoseconds total is cpu. Distinct function names let a test
// read each day's share of a merged default.pgo. The profile carries no
// TimeNanos, so merge weighting uses the publish request's capture time.
func cpuProfileFn(t *testing.T, fnName string, cpu int64) []byte {
	t.Helper()
	fn := &profile.Function{ID: 1, Name: fnName, Filename: "x.go"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn, Line: 1}}}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		Sample:   []*profile.Sample{{Value: []int64{1, cpu}, Location: []*profile.Location{loc}}},
		Function: []*profile.Function{fn},
		Location: []*profile.Location{loc},
	}
	var buf bytes.Buffer
	require.NoError(t, p.Write(&buf))
	return buf.Bytes()
}

// publishProfile publishes raw profile bytes for date with the given run id and
// capture time, for tests that need to control both the profile shape and the
// merge weighting.
func publishProfile(t *testing.T, s store.Store, date, runID string, prof []byte, captured time.Time) *store.PublishResult {
	t.Helper()
	d := mustDate(t, date)
	res, err := store.Publish(context.Background(), s, store.PublishRequest{
		Key:          testKey,
		Today:        d,
		Branch:       "main",
		Run:          store.RunInfo{ID: runID, QualityScore: 0.9, CapturedAt: captured},
		Profile:      prof,
		LookbackDays: 14,
		MaxProfiles:  7,
		GoVersion:    "go1.25",
	})
	require.NoError(t, err)
	return res
}

// functionCPUShares parses a merged default.pgo and returns each leaf function's
// fraction of the total CPU samples.
func functionCPUShares(t *testing.T, data []byte) map[string]float64 {
	t.Helper()
	p, err := profile.ParseData(data)
	require.NoError(t, err)
	idx, ok := pprofutil.CPUSampleIndex(p)
	require.True(t, ok)

	byFn := make(map[string]int64)
	var total int64
	for _, s := range p.Sample {
		if idx >= len(s.Value) {
			continue
		}
		total += s.Value[idx]
		if len(s.Location) > 0 && len(s.Location[0].Line) > 0 {
			byFn[s.Location[0].Line[0].Function.Name] += s.Value[idx]
		}
	}
	shares := make(map[string]float64, len(byFn))
	for fn, v := range byFn {
		shares[fn] = float64(v) / float64(total)
	}
	return shares
}
