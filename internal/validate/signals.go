package validate

import (
	"math"
	"sort"
	"strings"

	"github.com/google/pprof/profile"

	"github.com/Better-Go-Labs/pgoctl/internal/pprofutil"
)

// runtimePrefixes are package prefixes whose CPU time the compiler cannot move
// through PGO: the Go runtime, GC, syscall wrappers, and I/O polling.
// net/http, sync, os etc. are deliberately excluded — PGO compiles the standard
// library with the profile, so hot stdlib paths are valid inlining targets.
var runtimePrefixes = []string{
	"runtime",
	"internal/runtime",
	"syscall",
	"internal/poll",
}

func isRuntimeFunc(name string) bool {
	for _, prefix := range runtimePrefixes {
		if name == prefix || strings.HasPrefix(name, prefix+".") || strings.HasPrefix(name, prefix+"/") {
			return true
		}
	}
	return false
}

// leafFunction returns the name of the innermost (leaf) function in the sample,
// or "" if none can be found.
func leafFunction(s *profile.Sample) string {
	for _, loc := range s.Location {
		if loc == nil || len(loc.Line) == 0 {
			continue
		}
		fn := loc.Line[0].Function
		if fn != nil && fn.Name != "" {
			return fn.Name
		}
	}
	return ""
}

// leafCounts returns a (counts, total) pair: counts maps each leaf function name
// to its aggregate sample value at idx; total is the sum of all sample values.
func leafCounts(p *profile.Profile, idx int) (map[string]int64, int64) {
	counts := make(map[string]int64)
	var total int64
	for _, s := range p.Sample {
		if idx >= len(s.Value) {
			continue
		}
		v := s.Value[idx]
		total += v
		if name := leafFunction(s); name != "" {
			counts[name] += v
		}
	}
	return counts, total
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
	counts, total := leafCounts(p, idx)
	if total == 0 {
		return 0
	}
	var user int64
	for name, v := range counts {
		pkg := pprofutil.PackageOf(name)
		if !isRuntimeFunc(pkg) {
			user += v
		}
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
	counts, total := leafCounts(p, idx)
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

// CrossPackageChainDensity returns the CPU-weighted fraction (0–1) of sampled
// call-chains that contain at least one cross-package boundary.  Chains are
// weighted by their sample value, so a stack sampled 10 000× contributes 10 000
// units, not one.  Only chains whose leaf function's flat share exceeds
// hotThresholdPct are counted; hotThresholdPct=0 includes every chain.
//
// Boundary detection scans every frame in the chain — not only hot ones — so
// any adjacent pair of frames from different packages is a boundary.  Standard-
// library frames (sort, encoding, …) count as package boundaries; this is
// intentional because PGO's cross-package inlining covers stdlib too.
// Values close to 1.0 indicate that many CPU-weighted chains cross package
// lines and PGO's cross-package inlining optimisations have many targets.
func CrossPackageChainDensity(p *profile.Profile, hotThresholdPct float64) float64 {
	idx, ok := cpuSampleIndex(p)
	if !ok {
		return 0
	}

	counts, total := leafCounts(p, idx)
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

	// Walk each sample's call-chain; accumulate CPU-weighted chain counts.
	var crossPkgWeight, totalWeight int64
	for _, s := range p.Sample {
		if idx >= len(s.Value) || s.Value[idx] == 0 {
			continue
		}
		// Skip chains whose leaf is below the hot threshold.
		if hotThresholdPct > 0 && !hotFuncs[leafFunction(s)] {
			continue
		}
		w := s.Value[idx]
		totalWeight += w

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
				pkgs = append(pkgs, pprofutil.PackageOf(line.Function.Name))
			}
		}
		// Look for at least one package boundary.
		for i := 1; i < len(pkgs); i++ {
			if pkgs[i] != pkgs[i-1] {
				crossPkgWeight += w
				break
			}
		}
		pkgs = pkgs[:0]
	}
	if totalWeight == 0 {
		return 0
	}
	density := float64(crossPkgWeight) / float64(totalWeight)
	return math.Round(density*10000) / 10000
}
