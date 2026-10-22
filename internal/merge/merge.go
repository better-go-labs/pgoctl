// Package merge implements profile merging strategies for pgoctl.
package merge

import (
	"fmt"
	"io"
	"math"
	"time"

	"github.com/google/pprof/profile"
)

// Strategy selects how profiles are merged.
type Strategy string

// Merge strategy constants.
const (
	Weighted Strategy = "weighted"
	Latest   Strategy = "latest"
	Union    Strategy = "union"
)

// Options configures the merge operation.
type Options struct {
	Strategy      Strategy
	RecencyWeight float64
	HalfLife      time.Duration
	DropInvalid   bool
}

// DefaultOptions returns Options with sensible defaults.
func DefaultOptions() Options {
	return Options{
		Strategy:      Weighted,
		RecencyWeight: 2.0,
		HalfLife:      24 * time.Hour,
	}
}

// Input is a single pprof profile. CapturedAt is only used as a fallback when
// the profile itself carries no timestamp (p.TimeNanos == 0).
type Input struct {
	Data       []byte
	CapturedAt time.Time
}

// captureTime returns the profile's own capture time when present, falling
// back to the caller-provided timestamp otherwise.
func captureTime(p *profile.Profile, fallback time.Time) time.Time {
	if p.TimeNanos != 0 {
		return time.Unix(0, p.TimeNanos)
	}
	return fallback
}

// Profiles merges the given pprof inputs according to opts and writes the
// resulting gzipped pprof to w. The output is a valid default.pgo input.
func Profiles(inputs []Input, opts Options, w io.Writer) error {
	if len(inputs) == 0 {
		return fmt.Errorf("at least one profile is required")
	}

	profiles := make([]*profile.Profile, 0, len(inputs))
	times := make([]time.Time, 0, len(inputs))
	for i, inp := range inputs {
		p, err := profile.ParseData(inp.Data)
		if err != nil {
			if opts.DropInvalid {
				continue
			}
			return fmt.Errorf("input %d: parse: %w", i, err)
		}
		profiles = append(profiles, p)
		times = append(times, captureTime(p, inp.CapturedAt))
	}
	if len(profiles) == 0 {
		return fmt.Errorf("no valid profiles after parsing")
	}

	switch opts.Strategy {
	case Latest:
		newest := 0
		for i := 1; i < len(times); i++ {
			if times[i].After(times[newest]) {
				newest = i
			}
		}
		profiles = []*profile.Profile{profiles[newest]}

	case Union:
		// Equal weight for every profile: merge all at their native scale.
		for _, p := range profiles {
			p.Scale(1.0)
		}

	case Weighted:
		// Exponential decay (weight = RecencyWeight * 0.5^(age/HalfLife)) so
		// that recent profiles dominate while older ones still contribute.
		applyWeights(profiles, times, opts)

	default:
		return fmt.Errorf("unknown strategy %q", opts.Strategy)
	}

	merged, err := profile.Merge(profiles)
	if err != nil {
		return fmt.Errorf("profile.Merge: %w", err)
	}
	return merged.Write(w)
}

// applyWeights scales each profile by a normalised exponential recency weight.
//
// Raw weight = RecencyWeight * 0.5^(age/HalfLife). Dividing every raw weight
// by the newest profile's raw weight (= RecencyWeight, since age=0 there)
// normalises the scale so the newest profile always receives factor 1.0. This
// means a single-input merge returns unchanged sample totals, and relative
// recency ordering is preserved across multiple inputs.
//
// RecencyWeight cancels out after normalisation and has no effect on output; it
// is retained in Options for interface stability and potential future use as a
// decay-shape parameter.
func applyWeights(profiles []*profile.Profile, times []time.Time, opts Options) {
	newest := times[0]
	for _, t := range times[1:] {
		if t.After(newest) {
			newest = t
		}
	}
	halfLifeH := opts.HalfLife.Hours()
	if halfLifeH <= 0 {
		halfLifeH = 24.0
	}
	// newestW is the raw weight for age=0; dividing by it normalises newest→1.0.
	newestW := opts.RecencyWeight
	if newestW <= 0 {
		newestW = 1.0
	}
	for i, p := range profiles {
		ageH := newest.Sub(times[i]).Hours()
		w := opts.RecencyWeight * math.Pow(0.5, ageH/halfLifeH) / newestW
		// Floor keeps very-old profiles from contributing negligibly; expressed
		// relative to newestW so the ratio is consistent with the original 0.25
		// floor before normalisation.
		w = math.Max(0.25/newestW, w)
		p.Scale(w)
	}
}
