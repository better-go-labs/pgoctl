package store

import "sort"

// SelectWindow returns the newest-first set of days that fall within the
// lookback window ending at today, capped at maxProfiles.
//
// A day d is in the window when today.AddDays(-lookbackDays) is strictly before
// d (the lookback end is exclusive) and d is not after today. Input days are
// deduplicated and sorted newest first before filtering, so callers need not
// pre-sort. The result is nil when no day qualifies.
func SelectWindow(days []Date, today Date, lookbackDays, maxProfiles int) []Date {
	// Deduplicate; Date values for the same calendar day compare equal with ==.
	seen := make(map[Date]struct{}, len(days))
	uniq := make([]Date, 0, len(days))
	for _, d := range days {
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		uniq = append(uniq, d)
	}

	// Sort newest first.
	sort.Slice(uniq, func(i, j int) bool { return uniq[i].After(uniq[j]) })

	earliest := today.AddDays(-lookbackDays) // exclusive lower bound

	var out []Date
	for _, d := range uniq {
		if earliest.Before(d) && !d.After(today) {
			out = append(out, d)
			if len(out) == maxProfiles {
				break
			}
		}
	}
	return out
}
