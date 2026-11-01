package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
)

// Prune removes days that can no longer enter a window plus superseded
// sequences, and returns the deleted dates. Restore is the single source of the
// protected date, which is never deleted. A prune for one key never touches
// another key.
func Prune(ctx context.Context, s Store, key Key, today Date, lookbackDays, maxProfiles int) ([]Date, error) {
	// 1. Restore names the one protected day.
	var protected Day
	haveProtected := false
	if m, err := Restore(ctx, s, key, io.Discard); err == nil {
		if d, derr := ParseDate(m.Date); derr == nil {
			protected, haveProtected = Day{Date: d, Seq: m.Sequence}, true
		}
	} else if !errors.Is(err, ErrNoSnapshot) {
		return nil, fmt.Errorf("restore for prune: %w", err)
	}

	days, err := s.ListDays(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("listing days: %w", err)
	}

	// Dates in the window: a date counts when its highest published sequence has
	// a daily run.
	byDate := highestPublishedPerDate(days)
	earliest := today.AddDays(-lookbackDays)
	var inWindow []Date
	for d, day := range byDate {
		if !earliest.Before(d) || d.After(today) {
			continue
		}
		if m, err := readManifest(ctx, s, day); err == nil && m.DailyRun != "" {
			inWindow = append(inWindow, d)
		}
	}
	sort.Slice(inWindow, func(i, j int) bool { return inWindow[i].After(inWindow[j]) })

	// Highest published sequence per date, for superseded detection.
	highest := make(map[Date]int)
	for _, day := range days {
		if day.Draft {
			continue
		}
		if seq, ok := highest[day.Date]; !ok || day.Seq > seq {
			highest[day.Date] = day.Seq
		}
	}

	del := make(map[string]Day)
	mark := func(day Day) {
		if haveProtected && day.Date == protected.Date && day.Seq == protected.Seq {
			return
		}
		del[identityOf(day)] = day
	}

	// Always delete superseded days: any day, draft or published, below the
	// highest published sequence of its date.
	for _, day := range days {
		if day.Seq < highest[day.Date] {
			mark(day)
		}
	}

	// When the window is full, delete days older than the newest maxProfiles
	// in-window dates, and every day outside the lookback. Drafts included.
	if len(inWindow) >= maxProfiles {
		cutoff := inWindow[maxProfiles-1]
		for _, day := range days {
			if day.Date.Before(cutoff) || !earliest.Before(day.Date) {
				mark(day)
			}
		}
	}

	deleted := make(map[Date]bool)
	for _, day := range del {
		if err := s.DeleteDay(ctx, day); err != nil {
			return nil, fmt.Errorf("deleting %s: %w", key.Tag(day.Date, day.Seq), err)
		}
		deleted[day.Date] = true
	}
	if len(deleted) == 0 {
		return nil, nil
	}
	out := make([]Date, 0, len(deleted))
	for d := range deleted {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].After(out[j]) })
	return out, nil
}

// identityOf keys a day by date and sequence within one key's listing.
func identityOf(day Day) string {
	return fmt.Sprintf("%s\x00%d", day.Date.String(), day.Seq)
}
