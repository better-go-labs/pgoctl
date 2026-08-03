package validate

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/Better-Go-Labs/pgoctl/internal/errors"
	profiletypes "github.com/Better-Go-Labs/pgoctl/internal/profile"
	"github.com/google/pprof/profile"
)

const (
	targetSamples         = 50000
	targetDurationSeconds = 30.0 // seconds
)

// PackageShareGate requires the combined flat CPU share of all functions
// under Prefix (package prefix, subpackages included) to be >= MinPercent.
type PackageShareGate struct {
	Prefix     string
	MinPercent float64
}

// ParsePackageShareGates parses --min-package-share values. Each value is
// "prefix:percent" (e.g. github.com/prometheus/prometheus/tsdb:5); the flag
// may be repeated and/or comma-separated.
func ParsePackageShareGates(values []string) ([]PackageShareGate, error) {
	var gates []PackageShareGate
	for _, v := range values {
		for _, entry := range strings.Split(v, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			prefix, percentStr, ok := strings.Cut(entry, ":")
			if !ok {
				return nil, fmt.Errorf("invalid --min-package-share %q: want <package-prefix>:<min-percent>", entry)
			}
			prefix = strings.TrimSpace(prefix)
			if prefix == "" {
				return nil, fmt.Errorf("invalid --min-package-share %q: empty package prefix", entry)
			}
			percent, err := strconv.ParseFloat(strings.TrimSpace(percentStr), 64)
			if err != nil || percent < 0 {
				return nil, fmt.Errorf("invalid --min-package-share %q: percent must be a non-negative number", entry)
			}
			gates = append(gates, PackageShareGate{Prefix: prefix, MinPercent: percent})
		}
	}
	return gates, nil
}

// packageFromFunction extracts the package path from a pprof function name.
// Examples:
//
//	github.com/prometheus/prometheus/tsdb.(*Head).Append   -> .../tsdb
//	github.com/prometheus/prometheus/tsdb/wlog.(*Watcher).run -> .../tsdb/wlog
//	github.com/prometheus/prometheus/promql.(*Engine).exec  -> .../promql
//	runtime.main                                          -> runtime
func packageFromFunction(name string) string {
	slash := strings.LastIndex(name, "/")
	start := slash + 1
	rest := name[start:]
	cut := len(rest)
	if i := strings.IndexAny(rest, "(."); i >= 0 {
		cut = i
	}
	pkg := name[:start+cut]
	if pkg == "" {
		return name
	}
	return pkg
}

// cpuSampleIndex returns the index of the CPU sample value (cpu/nanoseconds,
// falling back to samples/count) and whether one was found.
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

// ComputePackageShares returns each package's flat CPU share (percent of
// total CPU sample value), attributed to the leaf (innermost) function of
// each sample. Returns an error when the profile has no CPU sample type.
func computePackageShares(p *profile.Profile) (map[string]float64, error) {
	idx, ok := cpuSampleIndex(p)
	if !ok {
		return nil, fmt.Errorf("%w", errors.ErrNoCPUSampleType)
	}

	funcTotal := make(map[string]int64)
	var total int64
	for _, s := range p.Sample {
		if idx >= len(s.Value) {
			continue
		}
		v := s.Value[idx]
		total += v
		// Leaf function = innermost frame (last location, last line).
		for i := len(s.Location) - 1; i >= 0; i-- {
			loc := s.Location[i]
			if loc == nil || len(loc.Line) == 0 {
				continue
			}
			fn := loc.Line[len(loc.Line)-1].Function
			if fn != nil && fn.Name != "" {
				funcTotal[fn.Name] += v
				break
			}
		}
	}

	shares := make(map[string]float64)
	if total == 0 {
		return shares, nil
	}
	for fn, val := range funcTotal {
		pkg := packageFromFunction(fn)
		shares[pkg] += 100.0 * float64(val) / float64(total)
	}
	// Round to 2 decimals for stable output.
	for pkg, share := range shares {
		shares[pkg] = math.Round(share*100) / 100
	}
	return shares, nil
}

// gatePackageShare returns the combined share for a package prefix across
// all packages under it (prefix match on the package path).
func gatePackageShare(shares map[string]float64, prefix string) float64 {
	var combined float64
	for pkg, share := range shares {
		if pkg == prefix || strings.HasPrefix(pkg, prefix+"/") {
			combined += share
		}
	}
	return combined
}

type Options struct {
	MinSamples         int64
	MinDurationSeconds float64 // seconds
	MinScore           float64
	TargetSamples      int64
	TargetDuration     float64 // seconds
	MinStackDepth      float64
	WeightDensity      float64
	WeightRichness     float64
	WeightCoverage     float64
	WeightDepth        float64
	RichnessFactor     float64
	PackageShareGates  []PackageShareGate
}

func DefaultOptions() Options {
	return Options{
		MinSamples:         10000,
		MinDurationSeconds: 10.0,
		MinScore:           0.6,
		TargetSamples:      targetSamples,
		TargetDuration:     targetDurationSeconds,
		MinStackDepth:      2.0,
		WeightDensity:      0.40,
		WeightRichness:     0.30,
		WeightCoverage:     0.20,
		WeightDepth:        0.10,
		RichnessFactor:     0.02,
	}
}

func clamp(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

// ValidateFile parses path and returns a QualityReport.
// error is non-nil only on I/O or parse failure (caller should exit 2).
// report.Valid==false with error==nil means below threshold (caller exits 1).
func ValidateFile(path string, opts Options) (*profiletypes.QualityReport, error) {
	if opts.TargetSamples == 0 {
		opts.TargetSamples = targetSamples
	}
	if opts.TargetDuration == 0 {
		opts.TargetDuration = targetDurationSeconds
	}
	if opts.MinStackDepth == 0 {
		opts.MinStackDepth = 2.0
	}
	defaults := DefaultOptions()
	if opts.WeightDensity == 0 && opts.WeightRichness == 0 && opts.WeightCoverage == 0 && opts.WeightDepth == 0 {
		opts.WeightDensity = defaults.WeightDensity
		opts.WeightRichness = defaults.WeightRichness
		opts.WeightCoverage = defaults.WeightCoverage
		opts.WeightDepth = defaults.WeightDepth
	}
	if opts.RichnessFactor == 0 {
		opts.RichnessFactor = defaults.RichnessFactor
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errors.ErrReadFile, err)
	}
	p, err := profile.ParseData(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errors.ErrParseProfile, err)
	}

	hasCPU := false
	for _, st := range p.SampleType {
		if st.Type == "cpu" && st.Unit == "nanoseconds" {
			hasCPU = true
			break
		}
		if st.Type == "samples" && st.Unit == "count" {
			hasCPU = true
			break
		}
	}

	report := &profiletypes.QualityReport{}

	if !hasCPU {
		report.Errors = append(report.Errors, errors.ErrNoCPUSampleType.Error())
		return report, nil
	}

	sampleCount := int64(len(p.Sample))

	stackSet := make(map[string]struct{})
	var totalDepth int64
	for _, s := range p.Sample {
		key := ""
		for _, loc := range s.Location {
			for _, line := range loc.Line {
				if line.Function != nil {
					key += line.Function.Name + ";"
				}
			}
		}
		stackSet[key] = struct{}{}
		totalDepth += int64(len(s.Location))
	}
	uniqueStacks := int64(len(stackSet))

	var avgStackDepth float64
	if sampleCount > 0 {
		avgStackDepth = float64(totalDepth) / float64(sampleCount)
	}

	durationSec := p.DurationNanos / 1e9

	report.Samples = sampleCount
	report.UniqueStacks = uniqueStacks

	if sampleCount < opts.MinSamples {
		report.Errors = append(report.Errors, fmt.Sprintf("insufficient samples: %d < %d", sampleCount, opts.MinSamples))
	}
	if durationSec < int64(opts.MinDurationSeconds) {
		report.Warnings = append(report.Warnings, fmt.Sprintf("profile too short: %ds < %.0fs", durationSec, opts.MinDurationSeconds))
	}
	if avgStackDepth < opts.MinStackDepth {
		report.Errors = append(report.Errors, fmt.Sprintf(errors.ErrFlatProfile, opts.MinStackDepth))
	}

	// Package-share gates (coverage of specific hot paths, e.g. tsdb).
	if len(opts.PackageShareGates) > 0 {
		shares, err := computePackageShares(p)
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
		} else {
			report.PackageShares = make(map[string]float64)
			for _, gate := range opts.PackageShareGates {
				combined := gatePackageShare(shares, gate.Prefix)
				report.PackageShares[gate.Prefix] = combined
				if combined < gate.MinPercent {
					report.Errors = append(report.Errors, fmt.Sprintf(
						"package share too low: %s = %.2f%% < %.2f%%",
						gate.Prefix, combined, gate.MinPercent))
				}
			}
		}
	}

	density := clamp(float64(sampleCount) / float64(opts.TargetSamples))
	richness := clamp(float64(uniqueStacks) / (opts.RichnessFactor*float64(sampleCount) + 1))
	coverage := clamp(float64(durationSec) / opts.TargetDuration)
	var depthOK float64
	if avgStackDepth >= opts.MinStackDepth {
		depthOK = 1.0
	}
	score := opts.WeightDensity*density + opts.WeightRichness*richness + opts.WeightCoverage*coverage + opts.WeightDepth*depthOK

	report.QualityScore = math.Round(score*1000) / 1000
	report.Valid = score >= opts.MinScore && avgStackDepth >= opts.MinStackDepth && sampleCount >= opts.MinSamples && len(report.Errors) == 0

	return report, nil
}
