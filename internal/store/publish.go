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
	if req.LookbackDays < 1 || req.MaxProfiles < 1 {
		return nil, fmt.Errorf("window needs lookbackDays >= 1 and maxProfiles >= 1, got %d and %d: %w", req.LookbackDays, req.MaxProfiles, ErrInvalidInput)
	}

	p, err := profile.ParseData(req.Profile)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", internerrors.ErrParseProfile, err)
	}
	if cpuTotal(p) == 0 {
		return nil, fmt.Errorf("run %s profile has zero CPU samples", req.Run.ID)
	}

	days, err := s.ListDays(ctx, req.Key)
	if err != nil {
		return nil, fmt.Errorf("listing days: %w", err)
	}
	want := manifestAssetForDate(days, req.Today)

	day, m, err := openForWrite(ctx, s, req.Key, days, req.Today)
	if err != nil {
		return nil, err
	}

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

	daily, dailyRun, err := selectDaily(ctx, s, day, m)
	if err != nil {
		return nil, err
	}
	m.DailyRun = dailyRun

	def, windowDates, err := buildDefault(ctx, s, days, req.Today, daily, runCapturedAt(m, req.Today), req.LookbackDays, req.MaxProfiles)
	if err != nil {
		return nil, err
	}
	m.Window = Window{LookbackDays: req.LookbackDays, MaxProfiles: req.MaxProfiles, Dates: datesToStrings(windowDates)}

	if err := checkUnchanged(ctx, s, req.Key, req.Today, want); err != nil {
		return nil, err
	}

	if err := writeSnapshot(ctx, s, day, m, daily, def); err != nil {
		return nil, err
	}

	if day.Draft {
		if err := s.Publish(ctx, day); err != nil {
			return nil, fmt.Errorf("publishing %s: %w", m.Tag, err)
		}
	}

	res := &PublishResult{Manifest: m, Default: def}
	res.Pruned, res.PruneErr = Prune(ctx, s, req.Key, req.Today, req.LookbackDays, req.MaxProfiles)
	return res, nil
}
