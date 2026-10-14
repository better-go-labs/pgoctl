// Package explain analyzes pprof files and emits PGO readiness verdicts.
package explain

import (
	"fmt"
	"github.com/Better-Go-Labs/pgoctl/internal/pprofutil"
	"math"
	"os"
	"sort"

	internerrors "github.com/Better-Go-Labs/pgoctl/internal/errors"
	"github.com/google/pprof/profile"
)

// FunctionEntry is one function's flat CPU attribution.
type FunctionEntry struct {
	Function string  `json:"function"`
	Package  string  `json:"package"`
	FlatPct  float64 `json:"flat_pct"`
}

// PackageGroup is the roll-up of all top functions in one package.
type PackageGroup struct {
	Package   string          `json:"package"`
	TotalPct  float64         `json:"total_pct"`
	Functions []FunctionEntry `json:"functions"`
}

// Verdict is the plain-English PGO readiness assessment.
type Verdict string

// Verdict values for PGO readiness assessment.
const (
	VerdictReady      Verdict = "ready"
	VerdictBorderline Verdict = "borderline"
	VerdictNotReady   Verdict = "not-ready"
)

// Report is the full output of pgoctl explain.
type Report struct {
	ProfilePath   string          `json:"profile_path"`
	TotalSamples  int64           `json:"total_samples"`
	DurationSec   float64         `json:"duration_sec"`
	TopFunctions  []FunctionEntry `json:"top_functions"`
	PackageGroups []PackageGroup  `json:"package_groups"`
	Verdict       Verdict         `json:"verdict"`
	VerdictReason string          `json:"verdict_reason"`
}

// AnalyzeFile loads a pprof file and returns an explain report.
func AnalyzeFile(path string, topN int) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", internerrors.ErrReadFile, err)
	}
	p, err := profile.ParseData(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", internerrors.ErrParseProfile, err)
	}
	return analyze(path, p, topN), nil
}

func analyze(path string, p *profile.Profile, topN int) *Report {
	idx, ok := cpuSampleIndex(p)
	total := 0.0
	counts := make(map[string]float64)

	if ok {
		for _, s := range p.Sample {
			if len(s.Location) == 0 || len(s.Value) <= idx {
				continue
			}
			v := float64(s.Value[idx])
			total += v
			loc := s.Location[0]
			if len(loc.Line) > 0 && loc.Line[0].Function != nil {
				counts[loc.Line[0].Function.Name] += v
			}
		}
	}

	// Build sorted function entries.
	entries := make([]FunctionEntry, 0, len(counts))
	for fn, c := range counts {
		pct := 0.0
		if total > 0 {
			pct = math.Round(100.0*c/total*100) / 100
		}
		entries = append(entries, FunctionEntry{
			Function: fn,
			Package:  pprofutil.PackageOf(fn),
			FlatPct:  pct,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].FlatPct != entries[j].FlatPct {
			return entries[i].FlatPct > entries[j].FlatPct
		}
		return entries[i].Function < entries[j].Function
	})

	top := topN
	if top <= 0 || top > len(entries) {
		top = len(entries)
	}
	topEntries := entries[:top]

	// Group by package (over the top-N set only).
	pkgMap := make(map[string]*PackageGroup)
	pkgOrder := []string{}
	for _, e := range topEntries {
		if _, exists := pkgMap[e.Package]; !exists {
			pkgMap[e.Package] = &PackageGroup{Package: e.Package}
			pkgOrder = append(pkgOrder, e.Package)
		}
		g := pkgMap[e.Package]
		g.TotalPct = math.Round((g.TotalPct+e.FlatPct)*100) / 100
		g.Functions = append(g.Functions, e)
	}
	// Sort package groups by descending total.
	sort.Slice(pkgOrder, func(i, j int) bool {
		pi, pj := pkgMap[pkgOrder[i]].TotalPct, pkgMap[pkgOrder[j]].TotalPct
		if pi != pj {
			return pi > pj
		}
		return pkgOrder[i] < pkgOrder[j]
	})
	groups := make([]PackageGroup, 0, len(pkgOrder))
	for _, pkg := range pkgOrder {
		groups = append(groups, *pkgMap[pkg])
	}

	verdict, reason := pgoVerdict(p, int64(total), len(entries))

	return &Report{
		ProfilePath:   path,
		TotalSamples:  int64(len(p.Sample)),
		DurationSec:   math.Round(float64(p.DurationNanos)/1e9*10) / 10,
		TopFunctions:  topEntries,
		PackageGroups: groups,
		Verdict:       verdict,
		VerdictReason: reason,
	}
}

// pgoVerdict gives a simple plain-English readiness verdict based on the
// same signals used by validate: sample count and function diversity.
func pgoVerdict(p *profile.Profile, _ int64, uniqueFunctions int) (Verdict, string) {
	samples := int64(len(p.Sample))

	const minSamples = 10000
	const targetSamples = 50000
	const minFunctions = 20

	switch {
	case samples < minSamples:
		return VerdictNotReady, fmt.Sprintf(
			"too few samples (%d < %d); collect a longer or higher-load profile",
			samples, minSamples)
	case uniqueFunctions < minFunctions:
		return VerdictNotReady, fmt.Sprintf(
			"profile covers only %d unique functions; a richer workload is needed for PGO to be effective",
			uniqueFunctions)
	case samples < targetSamples:
		return VerdictBorderline, fmt.Sprintf(
			"sample count %d is below the recommended %d; PGO will work but a denser profile improves inlining decisions",
			samples, targetSamples)
	default:
		return VerdictReady, fmt.Sprintf(
			"%d samples across %d functions; profile is a good PGO baseline",
			samples, uniqueFunctions)
	}
}

// cpuSampleIndex returns the index of the CPU value in s.Value (cpu/nanoseconds
// preferred, samples/count as fallback).
func cpuSampleIndex(p *profile.Profile) (int, bool) {
	preferred, fallback := -1, -1
	for i, st := range p.SampleType {
		switch {
		case st.Type == "cpu" && st.Unit == "nanoseconds":
			preferred = i
		case st.Type == "samples" && st.Unit == "count":
			fallback = i
		}
	}
	if preferred >= 0 {
		return preferred, true
	}
	if fallback >= 0 {
		return fallback, true
	}
	return -1, false
}
