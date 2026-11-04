// Package validate scores CPU pprof profiles for PGO readiness.
package validate

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/Better-Go-Labs/pgoctl/internal/pprofutil"

	"github.com/Better-Go-Labs/pgoctl/internal/errors"
	profiletypes "github.com/Better-Go-Labs/pgoctl/internal/profile"
	"github.com/google/pprof/profile"
)

const (
	targetSamples         = 500000
	targetDurationSeconds = 600.0 // seconds
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

// ComputePackageShares returns each package's flat CPU share (percent of
// total CPU sample value), attributed to the leaf (innermost) function of
// each sample. Returns an error when the profile has no CPU sample type.
func computePackageShares(p *profile.Profile) (map[string]float64, error) {
	idx, ok := pprofutil.CPUSampleIndex(p)
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
		// Leaf function = Location[0] per pprof convention (innermost frame first).
		// Location[0].Line[0] is the deepest inlined frame within that location.
		for _, loc := range s.Location {
			if loc == nil || len(loc.Line) == 0 {
				continue
			}
			fn := loc.Line[0].Function
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
		pkg := pprofutil.PackageOf(fn)
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

// Options configures the quality thresholds applied to a CPU pprof.
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
	MinUniqueStacks    int64
	TargetUniqueStacks int64
	TargetStackDepth   int64
	PackageShareGates  []PackageShareGate
}

// DefaultOptions returns Options with sensible defaults for production profiles.
func DefaultOptions() Options {
	return Options{
		MinSamples:         10000,
		MinDurationSeconds: 10.0,
		MinScore:           0,
		TargetSamples:      targetSamples,
		TargetDuration:     targetDurationSeconds,
		MinStackDepth:      2.0,
		WeightDensity:      0.40,
		WeightRichness:     0.30,
		WeightCoverage:     0.20,
		WeightDepth:        0.10,
		RichnessFactor:     0.02,
		MinUniqueStacks:    100,
		TargetUniqueStacks: 10000,
		TargetStackDepth:   20,
	}
}

// grade returns a log-scale value in [0,1] measuring how close x is to hi
// given lo as the lower bound. Returns 0 when x<=lo, 1 when x>=hi.
// When lo<=0 the caller should substitute 1.0 as lo before calling.
func grade(x, lo, hi float64) float64 {
	if x <= lo {
		return 0
	}
	if x >= hi {
		return 1
	}
	return math.Log(x/lo) / math.Log(hi/lo)
}

// ValidateFile parses path and returns a QualityReport.
// error is non-nil only on I/O or parse failure (caller should exit 2).
// report.Valid==false with error==nil means below threshold (caller exits 1).
func ValidateFile(path string, opts Options) (*profiletypes.QualityReport, error) { //nolint:revive // public API rename out of scope
	if opts.TargetSamples == 0 {
		opts.TargetSamples = targetSamples
	}
	if opts.TargetDuration == 0 {
		opts.TargetDuration = targetDurationSeconds
	}
	if opts.MinStackDepth == 0 {
		opts.MinStackDepth = 2.0
	}
	if opts.TargetUniqueStacks == 0 {
		opts.TargetUniqueStacks = 10000
	}
	if opts.TargetStackDepth == 0 {
		opts.TargetStackDepth = 20
	}
	defaults := DefaultOptions()
	if opts.WeightDensity == 0 && opts.WeightRichness == 0 && opts.WeightCoverage == 0 && opts.WeightDepth == 0 {
		opts.WeightDensity = defaults.WeightDensity
		opts.WeightRichness = defaults.WeightRichness
		opts.WeightCoverage = defaults.WeightCoverage
		opts.WeightDepth = defaults.WeightDepth
	}
	// richnessFactor is deprecated; callers should warn before this call.

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

	sampleCount := pprofutil.SampleCount(p)
	rowCount := int64(len(p.Sample))

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
	if rowCount > 0 {
		avgStackDepth = float64(totalDepth) / float64(rowCount)
	}

	durationSec := float64(p.DurationNanos) / 1e9

	report.Samples = sampleCount
	report.UniqueStacks = uniqueStacks

	if sampleCount < opts.MinSamples {
		report.Errors = append(report.Errors, fmt.Sprintf("insufficient samples: %d < %d", sampleCount, opts.MinSamples))
	}
	if durationSec < opts.MinDurationSeconds {
		report.Warnings = append(report.Warnings, fmt.Sprintf("profile too short: %.0fs < %.0fs", durationSec, opts.MinDurationSeconds))
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

	// grade lower bounds: when min==0, that hard gate is off and we use 1.0 as lo.
	densityLo := float64(opts.MinSamples)
	if densityLo <= 0 {
		densityLo = 1
	}
	richnessLo := float64(opts.MinUniqueStacks)
	if richnessLo <= 0 {
		richnessLo = 1
	}

	density := grade(float64(sampleCount), densityLo, float64(opts.TargetSamples))
	richness := grade(float64(uniqueStacks), richnessLo, float64(opts.TargetUniqueStacks))
	depth := grade(avgStackDepth, opts.MinStackDepth, float64(opts.TargetStackDepth))

	var score float64
	if p.DurationNanos == 0 {
		report.Warnings = append(report.Warnings, "profile has no duration; coverage not scored")
		totalW := opts.WeightDensity + opts.WeightRichness + opts.WeightDepth
		if totalW > 0 {
			score = (opts.WeightDensity*density + opts.WeightRichness*richness + opts.WeightDepth*depth) / totalW
		}
	} else {
		coverageLo := opts.MinDurationSeconds
		if coverageLo <= 0 {
			coverageLo = 1
		}
		coverage := grade(durationSec, coverageLo, opts.TargetDuration)
		score = opts.WeightDensity*density + opts.WeightRichness*richness + opts.WeightCoverage*coverage + opts.WeightDepth*depth
	}

	report.QualityScore = math.Round(score*1000) / 1000
	// Every other gate records why it failed; without this a low score
	// showed as "valid false" with no reason.
	if report.QualityScore < opts.MinScore {
		report.Errors = append(report.Errors, fmt.Sprintf("quality score too low: %.3f < %.3f", report.QualityScore, opts.MinScore))
	}
	report.Valid = report.QualityScore >= opts.MinScore && avgStackDepth >= opts.MinStackDepth && sampleCount >= opts.MinSamples && len(report.Errors) == 0

	return report, nil
}
