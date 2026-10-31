package store

import (
	"context"
	"errors"
	"fmt"
)

// Prune removes days that can no longer enter any future window and cleans up
// superseded sequences. It applies the following rules in order:
//
//  1. Call Restore to identify the currently-selected day. That day must never
//     be deleted, regardless of any other rule.
//  2. If the lookback window contains at least MaxProfiles dates, delete every
//     day that is both outside the window and not the restore-protected day.
//  3. If the window contains fewer than MaxProfiles dates, delete nothing for
//     age: those older days represent the full history of the key.
//  4. Superseded sequences (a lower Seq for a date that has a higher published
//     one) are always deleted. The in-memory store never creates sequences > 0,
//     so rule 4 is a no-op here; GitHub Releases backends handle it.
func Prune(ctx context.Context, s Store, key string, opts WindowOpts) error {
	allDays, err := s.ListDays(ctx, key)
	if err != nil {
		return fmt.Errorf("listing days: %w", err)
	}
	if len(allDays) == 0 {
		return nil
	}

	// Determine the protected day via Restore (rule 1).
	protectedDay, _, restoreErr := Restore(ctx, s, key, opts)
	hasProtected := restoreErr == nil || !errors.Is(restoreErr, ErrNotFound)

	window := SelectWindow(allDays, opts)

	maxProfiles := opts.MaxProfiles
	if maxProfiles == 0 {
		maxProfiles = defaultMaxProfiles
	}

	// Rule 2 vs 3: only delete by age when the window is full.
	if len(window) < maxProfiles {
		return nil
	}

	// Build the set of window dates.
	inWindow := make(map[string]bool, len(window))
	for _, d := range window {
		inWindow[d.Date] = true
	}

	for _, d := range allDays {
		if inWindow[d.Date] {
			continue
		}
		// Rule 1: never delete the protected day.
		if hasProtected && d.Date == protectedDay.Date {
			continue
		}
		if err := s.DeleteDay(ctx, key, d); err != nil {
			return fmt.Errorf("deleting day %s: %w", d.Date, err)
		}
	}

	return nil
}
