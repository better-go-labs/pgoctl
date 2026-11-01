package store

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// ExcludeRequest is the input to Exclude.
type ExcludeRequest struct {
	Key    Key
	Date   Date
	RunID  string
	Reason string
	Actor  string
	Now    time.Time
}

// ExcludeResult reports what Exclude changed.
type ExcludeResult struct {
	Rebuilt         []Date // oldest first
	AlreadyExcluded bool
	Skipped         bool // the date has no published day
	PruneErr        error
}

// Exclude marks a run excluded and rebuilds every snapshot whose window used the
// run's date, oldest first so each rebuild reads the updated earlier dailies. It
// is a no-op when the run is already excluded, and reports Skipped when the date
// has no published day. Prune runs afterwards; its error is reported but does
// not fail the exclude.
func Exclude(ctx context.Context, s Store, req ExcludeRequest) (*ExcludeResult, error) {
	if err := req.Key.Validate(); err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}
	days, err := s.ListDays(ctx, req.Key)
	if err != nil {
		return nil, fmt.Errorf("listing days: %w", err)
	}
	target, ok := highestPublishedForDate(days, req.Date)
	if !ok {
		return &ExcludeResult{Skipped: true}, nil
	}
	tm, err := readManifest(ctx, s, target)
	if err != nil {
		return nil, fmt.Errorf("reading manifest of %s: %w", req.Key.Tag(req.Date, target.Seq), err)
	}
	ri := findRun(tm, req.RunID)
	if ri < 0 {
		return nil, fmt.Errorf("run %s not in %s: %w", req.RunID, req.Key.Tag(req.Date, target.Seq), ErrNotFound)
	}
	if tm.Runs[ri].Excluded {
		return &ExcludeResult{AlreadyExcluded: true}, nil
	}

	// The rebuild set: the excluded date plus every later day whose window used
	// it.
	rebuild := map[Date]bool{req.Date: true}
	for d, day := range highestPublishedPerDate(days) {
		if d == req.Date {
			continue
		}
		if m, err := readManifest(ctx, s, day); err == nil && containsString(m.Window.Dates, req.Date.String()) {
			rebuild[d] = true
		}
	}
	order := make([]Date, 0, len(rebuild))
	for d := range rebuild {
		order = append(order, d)
	}
	sort.Slice(order, func(i, j int) bool { return order[i].Before(order[j]) })

	lookback, maxProfiles := windowDefaults(tm.Window.LookbackDays, tm.Window.MaxProfiles)
	res := &ExcludeResult{}
	for _, d := range order {
		cur, err := s.ListDays(ctx, req.Key)
		if err != nil {
			return nil, fmt.Errorf("listing days: %w", err)
		}
		want := manifestAssetForDate(cur, d)
		day, m, err := openForWrite(ctx, s, req.Key, cur, d)
		if err != nil {
			return nil, err
		}

		var own []byte
		if d == req.Date {
			if i := findRun(m, req.RunID); i >= 0 {
				at := req.Now
				m.Runs[i].Excluded = true
				m.Runs[i].ExcludedAt = &at
				m.Runs[i].ExcludedBy = req.Actor
				m.Runs[i].Reason = req.Reason
			}
			own, m.DailyRun, err = selectDaily(ctx, s, day, m)
			if err != nil {
				return nil, err
			}
		} else {
			own, err = readAll(ctx, s, day, AssetDaily)
			if err != nil {
				return nil, fmt.Errorf("reading daily of %s: %w", req.Key.Tag(d, day.Seq), err)
			}
		}
		// Every rebuilt day had req.Date in its window, so each records the
		// exclusion, naming the excluded run and the date it ran on.
		m.Exclusions = append(m.Exclusions, Exclusion{
			Date: req.Date.String(), Run: req.RunID, Reason: req.Reason, By: req.Actor, At: req.Now,
		})

		lb, mp := windowDefaults(m.Window.LookbackDays, m.Window.MaxProfiles)
		def, wd, err := buildDefault(ctx, s, req.Key, cur, d, own, runCapturedAt(m, d), lb, mp)
		if err != nil {
			return nil, err
		}
		m.Window = Window{LookbackDays: lb, MaxProfiles: mp, Dates: datesToStrings(wd)}

		if err := checkUnchanged(ctx, s, req.Key, d, want); err != nil {
			return nil, err
		}
		if err := writeSnapshot(ctx, s, day, m, own, def); err != nil {
			return nil, err
		}
		if day.Draft {
			if err := s.Publish(ctx, day); err != nil {
				return nil, fmt.Errorf("publishing %s: %w", m.Tag, err)
			}
		}
		res.Rebuilt = append(res.Rebuilt, d)
	}

	_, res.PruneErr = Prune(ctx, s, req.Key, DateOf(req.Now), lookback, maxProfiles)
	return res, nil
}

// containsString reports whether ss contains want.
func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
