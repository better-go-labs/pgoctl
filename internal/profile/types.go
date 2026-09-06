// Package profile defines shared types for pprof profile metadata and quality reports.
package profile

import "time"

// ProfileMeta holds metadata describing a captured pprof file.
//
//nolint:revive // profile.ProfileMeta is the established name; renaming is out of scope
type ProfileMeta struct {
	Path         string        `json:"path"`
	Service      string        `json:"service"`
	CapturedAt   time.Time     `json:"captured_at"`
	Duration     time.Duration `json:"duration"`
	SampleCount  int64         `json:"samples"`
	UniqueStacks int64         `json:"unique_stacks"`
	SampleTypes  []string      `json:"sample_types"`
	SHA256       string        `json:"sha256"`
}

// WorkloadSignals holds the four workload-shape signals computed beyond the
// existing devirt/inline-fire pre-check. Each field is present in JSON output
// when the corresponding analysis was requested.
type WorkloadSignals struct {
	// CPUBoundFraction is the share (0–1) of on-CPU time attributed to
	// user/library Go functions vs runtime, GC, scheduler, syscall, cgo, and IO.
	// Higher values mean more optimisable code is on-CPU, making PGO more likely
	// to move the needle.
	CPUBoundFraction float64 `json:"cpu_bound_fraction"`

	// Peakedness is the cumulative flat-CPU share of the top-N hottest
	// functions (N configured by Options.PeakednessTopN, default 10).
	// Higher values indicate a concentrated hot-path where inlining decisions
	// have large leverage.
	Peakedness float64 `json:"peakedness"`

	// CrossPackageChainDensity is the fraction of sampled call-chains (stacks)
	// that contain at least one cross-package call boundary among the hot
	// top-K% of functions. Higher values mean PGO's cross-package inlining
	// optimisations have more targets.
	CrossPackageChainDensity float64 `json:"cross_package_chain_density"`

	// DriftPct is the profile representativeness signal when two profiles are
	// compared: the percentage of total CPU share that has shifted between the
	// early and late halves of the profile's time range (or between two supplied
	// windows). 0 = identical, 100 = completely different.  A high value
	// indicates the workload changed during capture, reducing PGO reliability.
	// Set to -1 when only one profile is provided (drift cannot be measured).
	DriftPct float64 `json:"drift_pct"`
}

// QualityReport is the output of a pgoctl validate run.
type QualityReport struct {
	Valid           bool               `json:"valid"`
	QualityScore    float64            `json:"quality_score"`
	Samples         int64              `json:"samples"`
	UniqueStacks    int64              `json:"unique_stacks"`
	PackageShares   map[string]float64 `json:"package_shares,omitempty"`
	WorkloadSignals *WorkloadSignals   `json:"workload_signals,omitempty"`
	Errors          []string           `json:"errors,omitempty"`
	Warnings        []string           `json:"warnings,omitempty"`
}
