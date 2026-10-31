package store

import (
	"bytes"
	"context"
	"fmt"
	"time"

	internerrors "github.com/Better-Go-Labs/pgoctl/internal/errors"
	"github.com/google/pprof/profile"
)

// RunInfo describes the run whose profile is being published.
type RunInfo struct {
	ID           string
	Commit       string
	CapturedAt   time.Time
	Samples      int64
	QualityScore float64
}

// PublishRequest is the input to Publish.
type PublishRequest struct {
	Key           Key
	Today         Date
	Branch        string
	Run           RunInfo
	Profile       []byte // raw cpu.pprof bytes for this run
	LookbackDays  int
	MaxProfiles   int
	GoVersion     string
	PgoctlVersion string
}

// PublishResult is the outcome of a successful Publish.
type PublishResult struct {
	Manifest *Manifest
	Default  []byte // the merged default.pgo, nil when the window has no selected run
	Pruned   []Date
	PruneErr error // a failed prune does not fail the publish
}

// Publish uploads the run profile for today, selects the day's best run,
// rebuilds daily.pprof and default.pgo from the window, writes the snapshot and
// publishes the day. It then prunes; a prune failure is reported in the result
// but does not fail the publish.
func Publish(ctx context.Context, s Store, req PublishRequest) (*PublishResult, error) {
	if err := req.Key.Validate(); err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}
	if !ValidRunID(req.Run.ID) {
		return nil, fmt.Errorf("invalid run id %q", req.Run.ID)
	}

	// 1. Parse and gate the profile before any store call.
	p, err := profile.ParseData(req.Profile)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", internerrors.ErrParseProfile, err)
	}
	if cpuTotal(p) == 0 {
		return nil, fmt.Errorf("run %s profile has zero CPU samples", req.Run.ID)
	}

	// 2. Snapshot the current days and remember today's manifest asset.
	days, err := s.ListDays(ctx, req.Key)
	if err != nil {
		return nil, fmt.Errorf("listing days: %w", err)
	}
	want := manifestAssetForDate(days, req.Today)

	// 3. Open the day to write.
	day, m, err := openForWrite(ctx, s, req.Key, days, req.Today)
	if err != nil {
		return nil, err
	}

	// 4. Upload this run's raw profile.
	runName := RunAsset(req.Run.ID)
	if err := s.PutAsset(ctx, day, runName, bytes.NewReader(req.Profile)); err != nil {
		return nil, fmt.Errorf("uploading run %s: %w", req.Run.ID, err)
	}
	upsertRun(m, req.Run, sha256Hex(req.Profile))
	m.Branch = req.Branch
	m.GoVersion = req.GoVersion
	m.PgoctlVersion = req.PgoctlVersion
	if m.Assets == nil {
		m.Assets = map[string]string{}
	}
	m.Assets[runName] = sha256Hex(req.Profile)

	// 5. Pick the day's daily run.
	daily, dailyRun, err := selectDaily(ctx, s, day, m)
	if err != nil {
		return nil, err
	}
	m.DailyRun = dailyRun

	// 6. Build default.pgo from the window.
	def, windowDates, err := buildDefault(ctx, s, req.Key, days, req.Today, daily, req.LookbackDays, req.MaxProfiles)
	if err != nil {
		return nil, err
	}
	m.Window = Window{LookbackDays: req.LookbackDays, MaxProfiles: req.MaxProfiles, Dates: datesToStrings(windowDates)}

	// 7. Guard against a competing publish for this key.
	if err := checkUnchanged(ctx, s, req.Key, req.Today, want); err != nil {
		return nil, err
	}

	// 8. Write daily.pprof, default.pgo and manifest.json (manifest last).
	if err := writeSnapshot(ctx, s, day, m, daily, def); err != nil {
		return nil, err
	}

	// 9. Publish the draft.
	if day.Draft {
		if err := s.Publish(ctx, day); err != nil {
			return nil, fmt.Errorf("publishing %s: %w", m.Tag, err)
		}
	}

	// 10. Prune. Its error is reported but does not fail the publish.
	res := &PublishResult{Manifest: m, Default: def}
	res.Pruned, res.PruneErr = Prune(ctx, s, req.Key, req.Today, req.LookbackDays, req.MaxProfiles)
	return res, nil
}
