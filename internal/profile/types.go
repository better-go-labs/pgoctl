package profile

import "time"

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

type QualityReport struct {
	Valid         bool               `json:"valid"`
	QualityScore  float64            `json:"quality_score"`
	Samples       int64              `json:"samples"`
	UniqueStacks  int64              `json:"unique_stacks"`
	PackageShares map[string]float64 `json:"package_shares,omitempty"`
	Errors        []string           `json:"errors,omitempty"`
	Warnings      []string           `json:"warnings,omitempty"`
}
