package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/Better-Go-Labs/pgoctl/internal/merge"
)

// ExcludeOpts configures an Exclude call.
type ExcludeOpts struct {
	Key    string
	Date   string
	RunID  string
	Reason string
	// MergeOpts is used when rebuilding the daily.pprof after exclusion.
	// Zero value uses merge.DefaultOptions.
	MergeOpts merge.Options
}

// Exclude marks a run as excluded, updates the daily selection, and rebuilds
// daily.pprof and default.pgo for the affected day. If the excluded run was
// the current daily_run, the next-best eligible run is promoted.
//
// Exclude returns ErrNotFound when the day or run does not exist, and
// ErrNoRuns when excluding the run would leave no eligible runs.
func Exclude(ctx context.Context, s Store, opts ExcludeOpts) error {
	day := Day{Date: opts.Date}

	m, err := s.GetManifest(ctx, opts.Key, day)
	if err != nil {
		return fmt.Errorf("reading manifest for %s: %w", opts.Date, err)
	}

	// Find and mark the run as excluded.
	idx := -1
	for i, r := range m.Runs {
		if r.RunID == opts.RunID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("run %q not found in day %s: %w", opts.RunID, opts.Date, ErrNotFound)
	}
	m.Runs[idx].Excluded = true
	m.Runs[idx].ExcludeReason = opts.Reason

	// Re-select the daily run after marking the exclusion.
	daily := SelectDailyRun(m.Runs)
	if daily == nil {
		return fmt.Errorf("excluding run %q leaves no eligible runs: %w", opts.RunID, ErrNoRuns)
	}
	m.DailyRun = daily.RunID

	// Rebuild the merge inputs from the selected run's raw asset.
	selectedData, err := s.GetAsset(ctx, opts.Key, day, daily.Filename)
	if err != nil {
		return fmt.Errorf("reading selected run asset %q: %w", daily.Filename, err)
	}

	mergeOpts := opts.MergeOpts
	if mergeOpts.Strategy == "" {
		mergeOpts = merge.DefaultOptions()
	}

	normalized, err := normalizeProfiles([]merge.Input{{Data: selectedData}})
	if err != nil {
		return fmt.Errorf("normalizing profiles: %w", err)
	}

	var dailyBuf, defaultBuf bytes.Buffer
	if err := merge.Profiles(normalized, mergeOpts, &dailyBuf); err != nil {
		return fmt.Errorf("rebuilding daily.pprof: %w", err)
	}
	dailyBytes := dailyBuf.Bytes()

	if err := merge.Profiles(normalized, mergeOpts, &defaultBuf); err != nil {
		return fmt.Errorf("rebuilding default.pgo: %w", err)
	}
	defaultBytes := defaultBuf.Bytes()

	// Update the SHA-256 records for rebuilt assets.
	if m.SHA256 == nil {
		m.SHA256 = make(map[string]string)
	}
	m.SHA256["daily.pprof"] = sha256Hex(dailyBytes)
	m.SHA256["default.pgo"] = sha256Hex(defaultBytes)

	manifestBytes, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshalling updated manifest: %w", err)
	}

	// Open a new draft and commit the updated state.
	draft, err := s.BeginDay(ctx, opts.Key, day)
	if err != nil {
		return fmt.Errorf("opening draft for update: %w", err)
	}

	if err := draft.PutAsset(ctx, "daily.pprof", dailyBytes); err != nil {
		return fmt.Errorf("uploading updated daily.pprof: %w", err)
	}
	if err := draft.PutAsset(ctx, "default.pgo", defaultBytes); err != nil {
		return fmt.Errorf("uploading updated default.pgo: %w", err)
	}
	if err := draft.PutAsset(ctx, "manifest.json", manifestBytes); err != nil {
		return fmt.Errorf("uploading updated manifest.json: %w", err)
	}
	if err := draft.PutManifest(ctx, m); err != nil {
		return fmt.Errorf("putting updated manifest: %w", err)
	}
	if err := draft.Commit(ctx); err != nil {
		return fmt.Errorf("committing updated day: %w", err)
	}

	return nil
}
