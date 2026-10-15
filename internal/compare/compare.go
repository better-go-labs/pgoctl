// Package compare provides CPU profile comparison and gate verdict computation.
package compare

import (
	"fmt"
	"math"
	"os"
	"sort"

	"github.com/Better-Go-Labs/pgoctl/internal/pprofutil"

	"github.com/google/pprof/profile"
)

// Verdict is the gate decision.
type Verdict string

// Verdict values returned by the gate evaluation.
const (
	Promote  Verdict = "promote"
	Rollback Verdict = "rollback"
	Neutral  Verdict = "neutral"
)

// FunctionDelta captures the per-function CPU change between two profiles.
type FunctionDelta struct {
	Function string  `json:"function"`
	BasePct  float64 `json:"base_pct"`
	CandPct  float64 `json:"cand_pct"`
	DeltaPct float64 `json:"delta_pct"` // relative % change: (cand-base)/base×100; negative = candidate uses less CPU; 0 when base=0
}

// Report is the full output of pgoctl compare.
type Report struct {
	BaselineSamples   int64           `json:"baseline_samples"`
	CandidateSamples  int64           `json:"candidate_samples"`
	TopDeltas         []FunctionDelta `json:"top_deltas"`
	SummaryCPUDelta   float64         `json:"summary_cpu_delta_pct"` // positive = improvement; weighted avg relative change over functions present in both profiles
	FilteredFunctions int64           `json:"filtered_functions"`    // dropped by MinCPUPercent filter
	Verdict           Verdict         `json:"verdict"`
}

// GateConfig holds the thresholds used to decide the verdict.
type GateConfig struct {
	MinCPUImprovement float64 // relative % improvement required for Promote
	MinCPURegression  float64 // relative % regression required for Rollback (independent of MinCPUImprovement)
	MinCPUPercent     float64 // drop functions below this CPU %% share in BOTH profiles (<= 0 disables)
	TopN              int     // number of function deltas to include in output
}

// DefaultGateConfig returns a GateConfig with sensible default thresholds.
func DefaultGateConfig() GateConfig {
	return GateConfig{MinCPUImprovement: 10.0, MinCPURegression: 10.0, TopN: 10}
}

// ProfileFiles loads two pprof files and compares CPU function attribution.
func ProfileFiles(basePath, candPath string, gate GateConfig) (*Report, error) {
	baseData, err := os.ReadFile(basePath)
	if err != nil {
		return nil, fmt.Errorf("read baseline %s: %w", basePath, err)
	}
	candData, err := os.ReadFile(candPath)
	if err != nil {
		return nil, fmt.Errorf("read candidate %s: %w", candPath, err)
	}
	return Profiles(baseData, candData, gate)
}

// Profiles compares two raw pprof byte slices.
func Profiles(baseData, candData []byte, gate GateConfig) (*Report, error) {
	base, err := profile.ParseData(baseData)
	if err != nil {
		return nil, fmt.Errorf("parse baseline: %w", err)
	}
	cand, err := profile.ParseData(candData)
	if err != nil {
		return nil, fmt.Errorf("parse candidate: %w", err)
	}
	return compareProfiles(base, cand, gate), nil
}

func compareProfiles(base, cand *profile.Profile, gate GateConfig) *Report {
	basePct := flatPercents(base)
	candPct := flatPercents(cand)

	// union of all function names across both profiles
	seen := make(map[string]struct{}, len(basePct)+len(candPct))
	for k := range basePct {
		seen[k] = struct{}{}
	}
	for k := range candPct {
		seen[k] = struct{}{}
	}

	deltas := make([]FunctionDelta, 0, len(seen))
	// Summary: weighted average of relative changes, restricted to functions
	// present in both profiles (b>0 AND c>0). Excluding single-profile
	// functions prevents the always-zero cancellation that arises because
	// percentages in a complete profile always sum to 100 on both sides.
	var summaryNum, summaryDen float64
	var filtered int64

	for fn := range seen {
		b := basePct[fn]
		c := candPct[fn]
		// Optional noise filter: skip functions whose CPU share is below
		// MinCPUPercent in BOTH profiles. A function that is hot in either
		// profile is kept, so large shifts (hot → cold / cold → hot) still
		// surface. Default (MinCPUPercent <= 0) disables filtering.
		if gate.MinCPUPercent > 0 && b < gate.MinCPUPercent && c < gate.MinCPUPercent {
			filtered++
			continue
		}
		var d float64
		if b > 0 {
			d = (c - b) / b * 100
		}
		deltas = append(deltas, FunctionDelta{
			Function: fn,
			BasePct:  math.Round(b*100) / 100,
			CandPct:  math.Round(c*100) / 100,
			DeltaPct: math.Round(d*100) / 100,
		})
		if b > 0 && c > 0 {
			summaryNum += c - b
			summaryDen += b
		}
	}

	sort.Slice(deltas, func(i, j int) bool {
		return math.Abs(deltas[i].DeltaPct) > math.Abs(deltas[j].DeltaPct)
	})

	top := gate.TopN
	if top <= 0 || top > len(deltas) {
		top = len(deltas)
	}

	// SummaryCPUDelta is signed: negative = the candidate spends LESS CPU
	// overall (improvement), positive = it spends MORE (regression). The
	// rollback gate uses its own dedicated threshold (MinCPURegression) so
	// the two gates can be tuned independently.
	var summary float64
	if summaryDen > 0 {
		summary = summaryNum / summaryDen * 100
	}

	v := Neutral
	switch {
	case summary <= -gate.MinCPUImprovement:
		v = Promote
	case summary >= gate.MinCPURegression:
		v = Rollback
	}

	return &Report{
		BaselineSamples:   pprofutil.SampleCount(base),
		CandidateSamples:  pprofutil.SampleCount(cand),
		TopDeltas:         deltas[:top],
		SummaryCPUDelta:   math.Round(summary*100) / 100,
		FilteredFunctions: filtered,
		Verdict:           v,
	}
}

// flatPercents returns per-function CPU percentage attribution (top frame only).
func flatPercents(p *profile.Profile) map[string]float64 {
	total := 0.0
	counts := make(map[string]float64)
	for _, s := range p.Sample {
		if len(s.Location) == 0 || len(s.Value) == 0 {
			continue
		}
		v := float64(s.Value[0])
		total += v
		loc := s.Location[0]
		if len(loc.Line) > 0 && loc.Line[0].Function != nil {
			counts[loc.Line[0].Function.Name] += v
		}
	}
	if total == 0 {
		return counts
	}
	result := make(map[string]float64, len(counts))
	for fn, c := range counts {
		result[fn] = 100.0 * c / total
	}
	return result
}
