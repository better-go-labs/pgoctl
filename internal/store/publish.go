package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/merge"
	"github.com/Better-Go-Labs/pgoctl/internal/pprofutil"
	"github.com/google/pprof/profile"
)

// RunAssetName returns the release asset filename for a run.
func RunAssetName(runID string) string {
	return "runs-" + runID + ".pprof"
}

// PublishOpts configures a Publish call.
type PublishOpts struct {
	Key          string
	Date         string // "2026-10-31"
	RunID        string // e.g. "11873365122-1"
	ProfileData  []byte // raw cpu.pprof bytes
	QualityScore float64
	WindowOpts   WindowOpts
	MergeOpts    merge.Options
}

// Publish uploads a CPU profile for today's run, selects the best daily run,
// normalizes and merges the window profiles into daily.pprof and default.pgo,
// and commits the day.
func Publish(ctx context.Context, s Store, opts PublishOpts) (Day, error) {
	day := Day{Date: opts.Date, Seq: 0}

	draft, err := s.BeginDay(ctx, opts.Key, day)
	if err != nil {
		return Day{}, fmt.Errorf("beginning day: %w", err)
	}

	// Read existing manifest if this day already has committed runs.
	var m *Manifest
	existing, err := s.GetManifest(ctx, opts.Key, day)
	switch {
	case err == nil:
		cp := *existing
		m = &cp
	case errors.Is(err, ErrNotFound):
		m = &Manifest{SchemaVersion: 1, Date: opts.Date, Seq: 0}
	default:
		return Day{}, fmt.Errorf("reading existing manifest: %w", err)
	}

	// Upload this run's raw profile.
	runFile := RunAssetName(opts.RunID)
	if err := draft.PutAsset(ctx, runFile, opts.ProfileData); err != nil {
		return Day{}, fmt.Errorf("uploading run asset: %w", err)
	}

	// Register the run, avoiding duplicates from retries.
	if !hasRun(m.Runs, opts.RunID) {
		m.Runs = append(m.Runs, Asset{
			RunID:        opts.RunID,
			QualityScore: opts.QualityScore,
			Filename:     runFile,
		})
	}

	// Select the best non-excluded run for today.
	daily := SelectDailyRun(m.Runs)
	if daily == nil {
		return Day{}, fmt.Errorf("uploading run %s: %w", opts.RunID, ErrNoRuns)
	}
	m.DailyRun = daily.RunID

	// Build merge inputs: today's selected run + previous window days' daily.pprof.
	allDays, err := s.ListDays(ctx, opts.Key)
	if err != nil {
		return Day{}, fmt.Errorf("listing days: %w", err)
	}
	windowDays := SelectWindow(allDays, opts.WindowOpts)
	m.WindowDates = daysToDateStrings(windowDays)

	// Determine which profile data to use for today. If the selected run is
	// the one just uploaded, use opts.ProfileData directly. Otherwise read the
	// committed asset for the selected run (second Publish on same day).
	selectedData := opts.ProfileData
	if daily.RunID != opts.RunID {
		selectedData, err = s.GetAsset(ctx, opts.Key, day, daily.Filename)
		if err != nil {
			return Day{}, fmt.Errorf("reading selected run asset: %w", err)
		}
	}

	// Collect window inputs: today's selected run first, then previous days.
	mergeInputs := []merge.Input{{Data: selectedData, CapturedAt: time.Now()}}
	for _, wd := range windowDays {
		if wd.Date == opts.Date {
			// Today's contribution is the selected run above, not the old commit.
			continue
		}
		data, err := s.GetAsset(ctx, opts.Key, wd, "daily.pprof")
		if err != nil {
			// Skip days with missing assets rather than aborting the publish.
			continue
		}
		mergeInputs = append(mergeInputs, merge.Input{Data: data})
	}

	// Normalize all inputs to the same CPU total before merging.
	normalized, err := normalizeProfiles(mergeInputs)
	if err != nil {
		return Day{}, fmt.Errorf("normalizing profiles: %w", err)
	}

	mergeOpts := opts.MergeOpts
	if mergeOpts.Strategy == "" {
		mergeOpts = merge.DefaultOptions()
	}

	var dailyBuf, defaultBuf bytes.Buffer
	if err := merge.Profiles(normalized, mergeOpts, &dailyBuf); err != nil {
		return Day{}, fmt.Errorf("merging daily.pprof: %w", err)
	}
	dailyBytes := dailyBuf.Bytes()

	// default.pgo and daily.pprof are the same merged output; default.pgo is
	// the filename the Go compiler reads for PGO.
	if err := merge.Profiles(normalized, mergeOpts, &defaultBuf); err != nil {
		return Day{}, fmt.Errorf("merging default.pgo: %w", err)
	}
	defaultBytes := defaultBuf.Bytes()

	if err := draft.PutAsset(ctx, "daily.pprof", dailyBytes); err != nil {
		return Day{}, fmt.Errorf("uploading daily.pprof: %w", err)
	}
	if err := draft.PutAsset(ctx, "default.pgo", defaultBytes); err != nil {
		return Day{}, fmt.Errorf("uploading default.pgo: %w", err)
	}

	m.SHA256 = map[string]string{
		runFile:       sha256Hex(opts.ProfileData),
		"daily.pprof": sha256Hex(dailyBytes),
		"default.pgo": sha256Hex(defaultBytes),
	}

	manifestBytes, err := json.Marshal(m)
	if err != nil {
		return Day{}, fmt.Errorf("marshalling manifest: %w", err)
	}
	if err := draft.PutAsset(ctx, "manifest.json", manifestBytes); err != nil {
		return Day{}, fmt.Errorf("uploading manifest.json: %w", err)
	}
	if err := draft.PutManifest(ctx, m); err != nil {
		return Day{}, fmt.Errorf("putting manifest: %w", err)
	}
	if err := draft.Commit(ctx); err != nil {
		return Day{}, fmt.Errorf("committing day: %w", err)
	}

	return day, nil
}

// SelectDailyRun returns the non-excluded run with the highest QualityScore.
// On a tie, the last run in the slice wins (appended runs are assumed newer).
// Returns nil when all runs are excluded or the slice is empty.
func SelectDailyRun(runs []Asset) *Asset {
	var best *Asset
	for i := range runs {
		r := &runs[i]
		if r.Excluded {
			continue
		}
		if best == nil || r.QualityScore >= best.QualityScore {
			best = r
		}
	}
	return best
}

// parsedInput holds a parsed pprof profile alongside the metadata from the
// original merge.Input so the final output loop does not need to re-index
// the inputs slice.
type parsedInput struct {
	p          *profile.Profile
	total      int64
	capturedAt time.Time
}

// normalizeProfiles scales each pprof input so all have the same CPU sample
// total. This prevents a high-traffic day from dominating the merge.
// Profiles are scaled down to the minimum total to avoid integer overflow.
func normalizeProfiles(inputs []merge.Input) ([]merge.Input, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	parsed := make([]parsedInput, len(inputs))
	for i, inp := range inputs {
		p, err := profile.ParseData(inp.Data)
		if err != nil {
			return nil, fmt.Errorf("parsing profile %d for normalization: %w", i, err)
		}
		idx, ok := pprofutil.CPUSampleIndex(p)
		if !ok {
			return nil, fmt.Errorf("profile %d has no CPU sample type", i)
		}
		var total int64
		for _, s := range p.Sample {
			if idx < len(s.Value) {
				total += s.Value[idx]
			}
		}
		parsed[i] = parsedInput{p: p, total: total, capturedAt: inp.CapturedAt}
	}

	// Find the minimum non-zero total to scale down to.
	var minTotal int64
	for _, pr := range parsed {
		if pr.total > 0 && (minTotal == 0 || pr.total < minTotal) {
			minTotal = pr.total
		}
	}
	if minTotal == 0 {
		// All totals are zero; nothing to scale.
		return inputs, nil
	}

	out := make([]merge.Input, len(inputs))
	for i, pr := range parsed {
		if pr.total > 0 {
			pr.p.Scale(float64(minTotal) / float64(pr.total))
		}
		var buf bytes.Buffer
		if err := pr.p.Write(&buf); err != nil {
			return nil, fmt.Errorf("writing normalized profile %d: %w", i, err)
		}
		out[i] = merge.Input{Data: buf.Bytes(), CapturedAt: pr.capturedAt}
	}
	return out, nil
}

func hasRun(runs []Asset, runID string) bool {
	for _, r := range runs {
		if r.RunID == runID {
			return true
		}
	}
	return false
}

func daysToDateStrings(days []Day) []string {
	if len(days) == 0 {
		return nil
	}
	dates := make([]string, len(days))
	for i, d := range days {
		dates[i] = d.Date
	}
	return dates
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
