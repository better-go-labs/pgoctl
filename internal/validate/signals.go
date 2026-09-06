package validate

import (
	"math"
	"sort"
	"strings"

	"github.com/google/pprof/profile"
)

// runtimePrefixes are Go runtime, GC, scheduler, syscall, cgo, and IO
// package prefixes that are not optimisable by PGO. Anything outside these
// prefixes counts as "user/library Go code" for the CPU-bound fraction signal.
var runtimePrefixes = []string{
	"runtime",
	"runtime/internal",
	"sync",
	"syscall",
	"internal/poll",
	"net/http",
	"os",
}

func isRuntimeFunc(name string) bool {
	// Bare runtime.* functions and their sub-packages.
	for _, prefix := range runtimePrefixes {
		if name == prefix || strings.HasPrefix(name, prefix+".") || strings.HasPrefix(name, prefix+"/") {
			return true
		}
	}
	return false
}

// CPUBoundFraction returns the fraction (0–1) of on-CPU sample weight
// attributed to non-runtime user/library Go functions.  A high fraction means
// the hot loop is in optimisable code; a low fraction means the binary is IO-
// or runtime-bound and PGO is unlikely to help.
func CPUBoundFraction(p *profile.Profile) float64 {
	idx, ok := cpuSampleIndex(p)
	if !ok {
		return 0
	}
	var user, total int64
	for _, s := range p.Sample {
		if idx >= len(s.Value) {
			continue
		}
		v := s.Value[idx]
		total += v
		// Attribute to the leaf (innermost) function.
		for _, loc := range s.Location {
			if loc == nil || len(loc.Line) == 0 {
				continue
			}
			fn := loc.Line[0].Function
			if fn != nil && fn.Name != "" {
				pkg := packageFromFunction(fn.Name)
				if !isRuntimeFunc(pkg) {
					user += v
				}
				break
			}
		}
	}
	if total == 0 {
		return 0
	}
	return math.Round(float64(user)/float64(total)*10000) / 10000
}

// ProfilePeakedness returns the cumulative flat-CPU share (0–100) of the
// topN hottest leaf functions.  A high value means a small number of functions
// dominate on-CPU time; PGO's inlining decisions over those functions have
// large leverage.
func ProfilePeakedness(p *profile.Profile, topN int) float64 {
	if topN <= 0 {
		topN = 10
	}
	idx, ok := cpuSampleIndex(p)
	if !ok {
		return 0
	}
	counts := make(map[string]int64)
	var total int64
	for _, s := range p.Sample {
		if idx >= len(s.Value) {
			continue
		}
		v := s.Value[idx]
		total += v
		for _, loc := range s.Location {
			if loc == nil || len(loc.Line) == 0 {
				continue
			}
			fn := loc.Line[0].Function
			if fn != nil && fn.Name != "" {
				counts[fn.Name] += v
				break
			}
		}
	}
	if total == 0 {
		return 0
	}
	vals := make([]int64, 0, len(counts))
	for _, v := range counts {
		vals = append(vals, v)
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] > vals[j] })

	if topN > len(vals) {
		topN = len(vals)
	}
	var sum int64
	for i := 0; i < topN; i++ {
		sum += vals[i]
	}
	pct := 100.0 * float64(sum) / float64(total)
	return math.Round(pct*100) / 100
}

// CrossPackageChainDensity returns the fraction (0–1) of sampled call-chains
// that contain at least one cross-package boundary among functions whose
// per-function flat share exceeds hotThresholdPct.  Values close to 1.0
// indicate PGO's cross-package inlining optimisations have many targets.
// hotThresholdPct = 0 means every function in a chain is a candidate.
func CrossPackageChainDensity(p *profile.Profile, hotThresholdPct float64) float64 {
	idx, ok := cpuSampleIndex(p)
	if !ok {
		return 0
	}

	// Compute per-function total to identify "hot" functions.
	counts := make(map[string]int64)
	var total int64
	for _, s := range p.Sample {
		if idx >= len(s.Value) {
			continue
		}
		v := s.Value[idx]
		total += v
		for _, loc := range s.Location {
			if loc == nil || len(loc.Line) == 0 {
				continue
			}
			fn := loc.Line[0].Function
			if fn != nil && fn.Name != "" {
				counts[fn.Name] += v
				break
			}
		}
	}
	if total == 0 {
		return 0
	}

	// Build hot-function set.
	hotFuncs := make(map[string]bool)
	for fn, v := range counts {
		if 100.0*float64(v)/float64(total) >= hotThresholdPct {
			hotFuncs[fn] = true
		}
	}

	// Walk each sample's call-chain; count cross-package boundaries.
	// A chain is included only when its leaf function meets the hot threshold.
	// We then check whether ANY two adjacent frames in the chain belong to
	// different packages (cross-package call boundary).
	var crossPkgChains, totalChains int64
	for _, s := range p.Sample {
		if idx >= len(s.Value) || s.Value[idx] == 0 {
			continue
		}

		// Determine the leaf function (innermost frame).
		var leafName string
		for _, loc := range s.Location {
			if loc == nil || len(loc.Line) == 0 {
				continue
			}
			if fn := loc.Line[0].Function; fn != nil && fn.Name != "" {
				leafName = fn.Name
				break
			}
		}
		// Skip chains whose leaf is below the hot threshold.
		if hotThresholdPct > 0 && !hotFuncs[leafName] {
			continue
		}
		totalChains++

		// Collect all frame packages in this chain.
		var pkgs []string
		for _, loc := range s.Location {
			if loc == nil {
				continue
			}
			for _, line := range loc.Line {
				if line.Function == nil {
					continue
				}
				pkgs = append(pkgs, packageFromFunction(line.Function.Name))
			}
		}
		// Look for at least one package boundary.
		hasCross := false
		for i := 1; i < len(pkgs); i++ {
			if pkgs[i] != pkgs[i-1] {
				hasCross = true
				break
			}
		}
		if hasCross {
			crossPkgChains++
		}
	}
	if totalChains == 0 {
		return 0
	}
	density := float64(crossPkgChains) / float64(totalChains)
	return math.Round(density*10000) / 10000
}

// ProfileDriftPct measures representativeness by comparing the function-share
// distributions of two non-overlapping halves of p (by sample index, not by
// time).  Returns 0–100: 0 means identical distributions, 100 means fully
// disjoint.  A high value indicates the workload changed mid-capture and the
// profile is not representative of a stable steady-state.
func ProfileDriftPct(p *profile.Profile) float64 {
	idx, ok := cpuSampleIndex(p)
	if !ok {
		return -1
	}
	n := len(p.Sample)
	if n < 2 {
		return -1
	}
	mid := n / 2

	computeShares := func(samples []*profile.Sample) map[string]float64 {
		counts := make(map[string]int64)
		var total int64
		for _, s := range samples {
			if idx >= len(s.Value) {
				continue
			}
			v := s.Value[idx]
			total += v
			for _, loc := range s.Location {
				if loc == nil || len(loc.Line) == 0 {
					continue
				}
				fn := loc.Line[0].Function
				if fn != nil && fn.Name != "" {
					counts[fn.Name] += v
					break
				}
			}
		}
		shares := make(map[string]float64, len(counts))
		if total == 0 {
			return shares
		}
		for fn, v := range counts {
			shares[fn] = float64(v) / float64(total)
		}
		return shares
	}

	early := computeShares(p.Sample[:mid])
	late := computeShares(p.Sample[mid:])

	// Total variation distance: sum of |early[fn] - late[fn]| over all fn / 2.
	// Multiply by 100 to express as percentage.
	all := make(map[string]struct{})
	for fn := range early {
		all[fn] = struct{}{}
	}
	for fn := range late {
		all[fn] = struct{}{}
	}
	var tvd float64
	for fn := range all {
		tvd += math.Abs(early[fn] - late[fn])
	}
	driftPct := 100.0 * tvd / 2.0
	return math.Round(driftPct*100) / 100
}
