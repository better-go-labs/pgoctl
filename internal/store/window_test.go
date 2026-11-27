package store_test

import (
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustDate parses s as a Date or fails the test.
func mustDate(t *testing.T, s string) store.Date {
	t.Helper()
	d, err := store.ParseDate(s)
	require.NoError(t, err)
	return d
}

// dateList parses each string into a Date. It returns nil for an empty input so
// that a nil want matches SelectWindow's nil-on-empty result.
func dateList(t *testing.T, ss ...string) []store.Date {
	t.Helper()
	if len(ss) == 0 {
		return nil
	}
	out := make([]store.Date, len(ss))
	for i, s := range ss {
		out[i] = mustDate(t, s)
	}
	return out
}

func TestSelectWindow(t *testing.T) {
	const today = "2026-10-31"
	tests := []struct {
		name         string
		days         []string
		lookbackDays int
		maxProfiles  int
		want         []string
	}{
		{
			name:         "empty input returns nil",
			days:         nil,
			lookbackDays: 14,
			maxProfiles:  7,
			want:         nil,
		},
		{
			name:         "day within the window is included",
			days:         []string{"2026-10-30"},
			lookbackDays: 14,
			maxProfiles:  7,
			want:         []string{"2026-10-30"},
		},
		{
			name:         "today is included",
			days:         []string{"2026-10-31"},
			lookbackDays: 14,
			maxProfiles:  7,
			want:         []string{"2026-10-31"},
		},
		{
			// earliest allowed is today-14 = 2026-10-17; the boundary day itself
			// is excluded (the lookback end is exclusive).
			name:         "lookback boundary day is excluded",
			days:         []string{"2026-10-17"},
			lookbackDays: 14,
			maxProfiles:  7,
			want:         nil,
		},
		{
			name:         "day one past the boundary is included",
			days:         []string{"2026-10-18"},
			lookbackDays: 14,
			maxProfiles:  7,
			want:         []string{"2026-10-18"},
		},
		{
			name:         "future day beyond today is excluded",
			days:         []string{"2026-11-01"},
			lookbackDays: 14,
			maxProfiles:  7,
			want:         nil,
		},
		{
			name:         "out-of-window days are skipped",
			days:         []string{"2026-10-31", "2026-10-15", "2026-10-29"},
			lookbackDays: 14,
			maxProfiles:  7,
			want:         []string{"2026-10-31", "2026-10-29"},
		},
		{
			name:         "unsorted input is returned newest first",
			days:         []string{"2026-10-28", "2026-10-31", "2026-10-29"},
			lookbackDays: 14,
			maxProfiles:  7,
			want:         []string{"2026-10-31", "2026-10-29", "2026-10-28"},
		},
		{
			name:         "duplicate dates are collapsed",
			days:         []string{"2026-10-30", "2026-10-30", "2026-10-29"},
			lookbackDays: 14,
			maxProfiles:  7,
			want:         []string{"2026-10-30", "2026-10-29"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.SelectWindow(dateList(t, tt.days...), mustDate(t, today), tt.lookbackDays, tt.maxProfiles)
			assert.Equal(t, dateList(t, tt.want...), got)
		})
	}
}

// TestSelectWindow_LookbackLargerThanMax covers the case where the lookback
// window admits more days than MaxProfiles: the result keeps only the newest
// MaxProfiles days, in newest-first order.
func TestSelectWindow_LookbackLargerThanMax(t *testing.T) {
	today := mustDate(t, "2026-10-31")
	// All five days fall inside the 14-day lookback window, but MaxProfiles caps
	// the result at the three newest.
	in := dateList(t, "2026-10-27", "2026-10-31", "2026-10-29", "2026-10-30", "2026-10-28")

	got := store.SelectWindow(in, today, 14, 3)

	want := dateList(t, "2026-10-31", "2026-10-30", "2026-10-29")
	assert.Equal(t, want, got)
}
