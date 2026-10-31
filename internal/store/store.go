// Package store defines the backend interface and types for profile storage.
// The default backend is GitHub Releases; the same Store interface covers
// Actions Artifacts and future backends.
package store

import (
	"context"
	"errors"
)

// Sentinel errors returned by Store and flow operations.
var (
	// ErrNotFound is returned when a day, manifest, or asset does not exist.
	ErrNotFound = errors.New("store: not found")

	// ErrConflict is returned when a manifest changed between read and commit.
	ErrConflict = errors.New("store: manifest changed since read")

	// ErrNoRuns is returned when all runs for a day are excluded.
	ErrNoRuns = errors.New("store: no eligible runs for this day")

	// ErrBadChecksum is returned when a downloaded asset does not match its
	// recorded SHA-256 hash.
	ErrBadChecksum = errors.New("store: asset checksum mismatch")
)

// Day identifies a daily profile snapshot for a key.
// Seq is the sequence number (0-based) within the date; it is > 0 only when
// immutable release mode forces a new sequence rather than updating in place.
type Day struct {
	Date string // "2026-10-31"
	Seq  int    // 0-based
}

// Asset records one CPU profile run uploaded to a day.
type Asset struct {
	RunID         string  `json:"run_id"`
	QualityScore  float64 `json:"quality_score"`
	Excluded      bool    `json:"excluded,omitempty"`
	ExcludeReason string  `json:"exclude_reason,omitempty"`
	Filename      string  `json:"filename"`
}

// Manifest records the full state of a published Day snapshot.
// SchemaVersion is always 1.
type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	Date          string            `json:"date"`
	Seq           int               `json:"seq"`
	Supersedes    int               `json:"supersedes,omitempty"`
	DailyRun      string            `json:"daily_run"`
	Runs          []Asset           `json:"runs"`
	WindowDates   []string          `json:"window_dates"`
	SHA256        map[string]string `json:"sha256"`
}

// Store is the backend interface for profile storage.
// Implementations include the in-memory store (storetest), GitHub Releases,
// and Actions Artifacts.
type Store interface {
	// ListDays returns all published days for key, newest first.
	// Each date appears at most once at its highest published sequence.
	// A day is published once its Draft has been Committed.
	ListDays(ctx context.Context, key string) ([]Day, error)

	// GetManifest returns the manifest for the highest published sequence of d.
	GetManifest(ctx context.Context, key string, d Day) (*Manifest, error)

	// GetAsset returns the named asset for the highest published sequence of d.
	GetAsset(ctx context.Context, key string, d Day, name string) ([]byte, error)

	// BeginDay opens or returns the existing draft for key+d.
	// Calling BeginDay twice for the same key and day must return a Draft backed
	// by the same underlying state; callers must not commit two drafts for the
	// same day concurrently.
	BeginDay(ctx context.Context, key string, d Day) (Draft, error)

	// DeleteDay removes all sequences of d.Date for key from the store.
	DeleteDay(ctx context.Context, key string, d Day) error
}

// Draft is a mutable write session for one day. Assets and the manifest are
// held privately until Commit publishes them.
type Draft interface {
	PutAsset(ctx context.Context, name string, data []byte) error
	PutManifest(ctx context.Context, m *Manifest) error
	Commit(ctx context.Context) error
	Discard(ctx context.Context) error
}
