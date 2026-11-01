package store

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/merge"
)

// SchemaVersion is the manifest schema version this build reads and writes.
const SchemaVersion = 1

// Manifest records the full state of a published day snapshot.
type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	Key           ManifestKey       `json:"key"`
	Branch        string            `json:"branch"`
	Date          string            `json:"date"`
	Tag           string            `json:"tag"`
	Sequence      int               `json:"sequence"`
	Supersedes    []string          `json:"supersedes,omitempty"` // earlier tags of this date
	DailyRun      string            `json:"daily_run,omitempty"`
	Runs          []Run             `json:"runs"`
	Window        Window            `json:"window"`
	Exclusions    []Exclusion       `json:"exclusions,omitempty"`
	Merge         MergeInfo         `json:"merge"`
	GoVersion     string            `json:"go_version"`
	PgoctlVersion string            `json:"pgoctl_version"`
	Assets        map[string]string `json:"assets"` // logical name -> sha256
}

// ManifestKey is the service component of the key recorded in the manifest.
type ManifestKey struct {
	Service string `json:"service"`
}

// Run records one CPU profile uploaded for a day.
type Run struct {
	ID           string     `json:"id"`
	Commit       string     `json:"commit"`
	CapturedAt   time.Time  `json:"captured_at"`
	Samples      int64      `json:"samples"`
	QualityScore float64    `json:"quality_score"`
	SHA256       string     `json:"sha256"`
	Excluded     bool       `json:"excluded"`
	ExcludedAt   *time.Time `json:"excluded_at,omitempty"`
	ExcludedBy   string     `json:"excluded_by,omitempty"`
	Reason       string     `json:"reason,omitempty"`
	Missing      bool       `json:"missing,omitempty"`
}

// Window records the rolling window used to build the day's default.pgo.
type Window struct {
	LookbackDays int      `json:"lookback_days"`
	MaxProfiles  int      `json:"max_profiles"`
	Dates        []string `json:"dates"` // newest first
}

// Exclusion records that a run was excluded from a day.
type Exclusion struct {
	Date   string    `json:"date"`
	Run    string    `json:"run"`
	Reason string    `json:"reason"`
	By     string    `json:"by"`
	At     time.Time `json:"at"`
}

// MergeInfo records the merge parameters used to build the default.pgo.
type MergeInfo struct {
	Strategy  string  `json:"strategy"`
	HalfLife  string  `json:"half_life"`
	MinWeight float64 `json:"min_weight"`
	Normalize bool    `json:"normalize"`
}

// DecodeManifest reads a manifest and returns ErrUnknownSchema when its
// schema_version is not the one this build supports.
func DecodeManifest(r io.Reader) (*Manifest, error) {
	var m Manifest
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		return nil, fmt.Errorf("decoding manifest: %w", err)
	}
	if m.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("manifest schema_version %d: %w", m.SchemaVersion, ErrUnknownSchema)
	}
	return &m, nil
}

// Encode writes the manifest as JSON with a two-space indent and a trailing
// newline.
func (m *Manifest) Encode(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("encoding manifest: %w", err)
	}
	return nil
}

// MergeInfoFrom records the merge parameters from o. HalfLife is formatted as
// whole hours and Normalize is always true, since the store normalizes every
// window input before merging.
func MergeInfoFrom(o merge.Options) MergeInfo {
	return MergeInfo{
		Strategy:  string(o.Strategy),
		HalfLife:  fmt.Sprintf("%dh", int(o.HalfLife.Hours())),
		MinWeight: o.MinWeight,
		Normalize: true,
	}
}
