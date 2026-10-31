package store

import "time"

// WindowOpts configures the rolling profile window.
type WindowOpts struct {
	Today        string // "2026-10-31"
	LookbackDays int    // calendar days to look back; 0 defaults to 14
	MaxProfiles  int    // cap on days in the window; 0 defaults to 7
}

const (
	defaultLookbackDays = 14
	defaultMaxProfiles  = 7
)

// SelectWindow returns the newest-first subset of days within the lookback
// window, capped at MaxProfiles.
//
// A day d is in the window when today - lookback_days < d.Date <= today.
// Input days are expected to be sorted newest first with at most one entry
// per date.
func SelectWindow(days []Day, opts WindowOpts) []Day {
	lookback := opts.LookbackDays
	if lookback == 0 {
		lookback = defaultLookbackDays
	}
	maxDays := opts.MaxProfiles
	if maxDays == 0 {
		maxDays = defaultMaxProfiles
	}

	today, err := time.Parse("2006-01-02", opts.Today)
	if err != nil {
		return nil
	}
	earliest := today.AddDate(0, 0, -lookback)

	var out []Day
	for _, d := range days {
		t, err := time.Parse("2006-01-02", d.Date)
		if err != nil {
			continue
		}
		// Window: strictly after earliest (not equal), not after today.
		if t.After(earliest) && !t.After(today) {
			out = append(out, d)
			if len(out) == maxDays {
				break
			}
		}
	}
	return out
}
