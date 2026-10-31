package store_test

import (
	"fmt"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/stretchr/testify/assert"
)

func TestSelectWindow(t *testing.T) {
	tests := []struct {
		name string
		days []store.Day
		opts store.WindowOpts
		want []store.Day
	}{
		{
			name: "empty input returns no days",
			days: nil,
			opts: store.WindowOpts{Today: "2026-10-31", LookbackDays: 14, MaxProfiles: 7},
			want: nil,
		},
		{
			name: "day within window is included",
			days: []store.Day{{Date: "2026-10-30"}},
			opts: store.WindowOpts{Today: "2026-10-31", LookbackDays: 14, MaxProfiles: 7},
			want: []store.Day{{Date: "2026-10-30"}},
		},
		{
			name: "today is included",
			days: []store.Day{{Date: "2026-10-31"}},
			opts: store.WindowOpts{Today: "2026-10-31", LookbackDays: 14, MaxProfiles: 7},
			want: []store.Day{{Date: "2026-10-31"}},
		},
		{
			name: "day exactly at lookback boundary is excluded",
			// lookback=14: earliest allowed is today-14; that day itself is NOT in the window
			days: []store.Day{{Date: "2026-10-17"}},
			opts: store.WindowOpts{Today: "2026-10-31", LookbackDays: 14, MaxProfiles: 7},
			want: nil,
		},
		{
			name: "day one inside lookback boundary is included",
			days: []store.Day{{Date: "2026-10-18"}},
			opts: store.WindowOpts{Today: "2026-10-31", LookbackDays: 14, MaxProfiles: 7},
			want: []store.Day{{Date: "2026-10-18"}},
		},
		{
			name: "future day beyond today is excluded",
			days: []store.Day{{Date: "2026-11-01"}},
			opts: store.WindowOpts{Today: "2026-10-31", LookbackDays: 14, MaxProfiles: 7},
			want: nil,
		},
		{
			name: "result is truncated to MaxProfiles",
			days: []store.Day{
				{Date: "2026-10-31"},
				{Date: "2026-10-30"},
				{Date: "2026-10-29"},
				{Date: "2026-10-28"},
			},
			opts: store.WindowOpts{Today: "2026-10-31", LookbackDays: 14, MaxProfiles: 3},
			want: []store.Day{
				{Date: "2026-10-31"},
				{Date: "2026-10-30"},
				{Date: "2026-10-29"},
			},
		},
		{
			name: "days outside window are skipped while keeping order",
			days: []store.Day{
				{Date: "2026-10-31"},
				{Date: "2026-10-15"}, // outside window (14 days back from 10-31 = 10-17)
				{Date: "2026-10-29"},
			},
			opts: store.WindowOpts{Today: "2026-10-31", LookbackDays: 14, MaxProfiles: 7},
			want: []store.Day{
				{Date: "2026-10-31"},
				{Date: "2026-10-29"},
			},
		},
		{
			name: "zero LookbackDays defaults to 14",
			days: []store.Day{
				{Date: "2026-10-31"},
				{Date: "2026-10-01"}, // 30 days back; excluded under default 14
			},
			opts: store.WindowOpts{Today: "2026-10-31", MaxProfiles: 7},
			want: []store.Day{{Date: "2026-10-31"}},
		},
		{
			name: "zero MaxProfiles defaults to 7",
			days: func() []store.Day {
				out := make([]store.Day, 10)
				for i := range out {
					out[i] = store.Day{Date: "2026-10-" + pad(31-i)}
				}
				return out
			}(),
			opts: store.WindowOpts{Today: "2026-10-31", LookbackDays: 30},
			want: func() []store.Day {
				out := make([]store.Day, 7)
				for i := range out {
					out[i] = store.Day{Date: "2026-10-" + pad(31-i)}
				}
				return out
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.SelectWindow(tt.days, tt.opts)
			assert.Equal(t, tt.want, got)
		})
	}
}

func pad(n int) string {
	if n < 10 {
		return fmt.Sprintf("0%d", n)
	}
	return fmt.Sprintf("%d", n)
}
