package compare_test

import (
	"bytes"
	"math"
	"testing"

	"github.com/google/pprof/profile"

	"github.com/Better-Go-Labs/pgoctl/internal/compare"
)

// buildTwoFnProfile creates a pprof with fn1Count samples in fn1 and fn2Count in fn2.
func buildTwoFnProfile(t *testing.T, fn1, fn2 string, fn1Count, fn2Count int) []byte {
	t.Helper()
	f1 := &profile.Function{ID: 1, Name: fn1, SystemName: fn1}
	f2 := &profile.Function{ID: 2, Name: fn2, SystemName: fn2}
	loc1 := &profile.Location{ID: 1, Line: []profile.Line{{Function: f1, Line: 1}}}
	loc2 := &profile.Location{ID: 2, Line: []profile.Line{{Function: f2, Line: 1}}}
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		PeriodType:    &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:        10000000,
		DurationNanos: 30 * 1e9,
		Function:      []*profile.Function{f1, f2},
		Location:      []*profile.Location{loc1, loc2},
	}
	for i := 0; i < fn1Count; i++ {
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc1},
			Value:    []int64{10000000},
		})
	}
	for i := 0; i < fn2Count; i++ {
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc2},
			Value:    []int64{10000000},
		})
	}
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		t.Fatalf("buildTwoFnProfile: %v", err)
	}
	return buf.Bytes()
}

func TestCompare_Improvement(t *testing.T) {
	// Baseline: hotFn monopolises CPU (1000 samples, 0 in other)
	// Candidate: PGO made hotFn faster — fewer samples, rest moved to otherFn
	// hotFn: base=100%, cand=70% → relative delta = (70−100)/100×100 = −30% → Promote
	// otherFn appears only in candidate (b=0), excluded from summary.
	base := buildTwoFnProfile(t, "main.hotFn", "main.otherFn", 1000, 0)
	cand := buildTwoFnProfile(t, "main.hotFn", "main.otherFn", 700, 300)

	rpt, err := compare.Profiles(base, cand, compare.DefaultGateConfig())
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if rpt.SummaryCPUDelta >= 0 {
		t.Errorf("expected negative CPU delta (improvement), got %.2f", rpt.SummaryCPUDelta)
	}
	if rpt.Verdict != compare.Promote {
		t.Errorf("expected Promote, got %s (delta=%.2f)", rpt.Verdict, rpt.SummaryCPUDelta)
	}
}

func TestCompare_Regression(t *testing.T) {
	// Candidate is worse: hotFn grew from 70% to 100% of samples.
	// otherFn disappeared in candidate (c=0) so is excluded from the summary;
	// summary = (100−70)/70×100 = 42.86% → Rollback.
	base := buildTwoFnProfile(t, "main.hotFn", "main.otherFn", 700, 300)
	cand := buildTwoFnProfile(t, "main.hotFn", "main.otherFn", 1000, 0)

	rpt, err := compare.Profiles(base, cand, compare.DefaultGateConfig())
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if rpt.Verdict != compare.Rollback {
		t.Errorf("expected Rollback, got %s (delta=%.2f)", rpt.Verdict, rpt.SummaryCPUDelta)
	}
}

func TestCompare_Neutral(t *testing.T) {
	// Identical profiles — delta is 0, no change
	data := buildTwoFnProfile(t, "main.hotFn", "main.otherFn", 500, 500)
	rpt, err := compare.Profiles(data, data, compare.DefaultGateConfig())
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if rpt.Verdict != compare.Neutral {
		t.Errorf("expected Neutral, got %s (delta=%.2f)", rpt.Verdict, rpt.SummaryCPUDelta)
	}
}

// buildFnProfile creates a pprof with one function per entry in fnCounts,
// each sampled fnCounts[name] times.
func buildFnProfile(t *testing.T, fnCounts map[string]int) []byte {
	t.Helper()
	var funcs []*profile.Function
	var locs []*profile.Location
	var samples []*profile.Sample
	id := uint64(1)
	for name, count := range fnCounts {
		f := &profile.Function{ID: id, Name: name, SystemName: name}
		loc := &profile.Location{ID: id, Line: []profile.Line{{Function: f, Line: 1}}}
		funcs = append(funcs, f)
		locs = append(locs, loc)
		for i := 0; i < count; i++ {
			samples = append(samples, &profile.Sample{
				Location: []*profile.Location{loc},
				Value:    []int64{10000000},
			})
		}
		id++
	}
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		PeriodType:    &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:        10000000,
		DurationNanos: 30 * 1e9,
		Function:      funcs,
		Location:      locs,
		Sample:        samples,
	}
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		t.Fatalf("buildFnProfile: %v", err)
	}
	return buf.Bytes()
}

func TestCompare_MinCPUPercentFilter(t *testing.T) {
	// hotFn 60%, warmFn 30%, coldFn 10% in baseline.
	// Candidate shifts hotFn→warmFn and coldFn grows 10%→15%.
	// With MinCPUPercent=20, coldFn is below the threshold in BOTH profiles
	// and must be excluded from the comparison.
	// Summary (hotFn + warmFn only, both present in both profiles):
	//   summaryNum = (51−60) + (34−30) = −9 + 4 = −5
	//   summaryDen = 60 + 30 = 90
	//   summary = −5/90×100 ≈ −5.56 → Promote (exceeds 3% MinCPUImprovement threshold)
	base := buildFnProfile(t, map[string]int{"main.hotFn": 600, "main.warmFn": 300, "main.coldFn": 100})
	cand := buildFnProfile(t, map[string]int{"main.hotFn": 510, "main.warmFn": 340, "main.coldFn": 150})

	gate := compare.DefaultGateConfig()
	gate.MinCPUPercent = 20
	rpt, err := compare.Profiles(base, cand, gate)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if rpt.FilteredFunctions != 1 {
		t.Errorf("expected 1 filtered function, got %d", rpt.FilteredFunctions)
	}
	for _, d := range rpt.TopDeltas {
		if d.Function == "main.coldFn" {
			t.Errorf("coldFn should have been filtered out, got delta %+v", d)
		}
	}
	// summaryNum=−5, summaryDen=90 → −5/90×100 ≈ −5.56
	if math.Abs(rpt.SummaryCPUDelta+5.56) > 0.01 {
		t.Errorf("expected summary delta -5.56 with filter, got %.2f", rpt.SummaryCPUDelta)
	}
	// −5.56% exceeds the DefaultGateConfig MinCPUImprovement (3.0%) → Promote
	if rpt.Verdict != compare.Promote {
		t.Errorf("expected Promote, got %s (delta=%.2f)", rpt.Verdict, rpt.SummaryCPUDelta)
	}
}

func TestCompare_NoFilterByDefault(t *testing.T) {
	// Default GateConfig has MinCPUPercent=0 → no filtering: coldFn stays in
	// the comparison. All three functions are present in both profiles, so:
	//   summaryNum = (51−60) + (34−30) + (15−10) = −9 + 4 + 5 = 0
	//   summaryDen = 60 + 30 + 10 = 100
	//   summary = 0 (gains and regressions cancel across the full 100% share)
	base := buildFnProfile(t, map[string]int{"main.hotFn": 600, "main.warmFn": 300, "main.coldFn": 100})
	cand := buildFnProfile(t, map[string]int{"main.hotFn": 510, "main.warmFn": 340, "main.coldFn": 150})

	rpt, err := compare.Profiles(base, cand, compare.DefaultGateConfig())
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if rpt.FilteredFunctions != 0 {
		t.Errorf("default behaviour must not filter, got %d filtered", rpt.FilteredFunctions)
	}
	found := false
	for _, d := range rpt.TopDeltas {
		if d.Function == "main.coldFn" {
			found = true
		}
	}
	if !found {
		t.Error("coldFn should be present when filtering is disabled")
	}
	// All three functions present in both profiles → summaryNum = Σ(b−c) = 0
	if math.Abs(rpt.SummaryCPUDelta) > 0.01 {
		t.Errorf("expected summary delta ~0 without filter, got %.2f", rpt.SummaryCPUDelta)
	}
}

// TestCompare_IndependentRollbackThreshold verifies that the rollback gate
// uses its own MinCPURegression threshold instead of mirroring
// MinCPUImprovement, so the two can be tuned independently.
func TestCompare_IndependentRollbackThreshold(t *testing.T) {
	// hotFn regresses from 90% to 100% of CPU; otherFn disappears (c=0) and
	// is excluded from the summary.
	// summary = (100−90)/90×100 = 11.11%
	// That is above the default 10% rollback threshold → Rollback.
	base := buildTwoFnProfile(t, "main.hotFn", "main.otherFn", 900, 100)
	cand := buildTwoFnProfile(t, "main.hotFn", "main.otherFn", 1000, 0)

	def, err := compare.Profiles(base, cand, compare.DefaultGateConfig())
	if err != nil {
		t.Fatalf("compare (default): %v", err)
	}
	if math.Abs(def.SummaryCPUDelta-11.11) > 0.01 {
		t.Fatalf("expected delta 11.11, got %.2f", def.SummaryCPUDelta)
	}
	if def.Verdict != compare.Rollback {
		t.Errorf("expected Rollback at default threshold, got %s", def.Verdict)
	}

	// A lax rollback threshold (15.0) must NOT roll back, even though
	// the improvement threshold is unchanged: the gates are independent.
	gate := compare.DefaultGateConfig()
	gate.MinCPURegression = 15.0
	rpt, err := compare.Profiles(base, cand, gate)
	if err != nil {
		t.Fatalf("compare (custom rollback): %v", err)
	}
	if rpt.Verdict != compare.Neutral {
		t.Errorf("expected Neutral with MinCPURegression=15, got %s", rpt.Verdict)
	}
}
