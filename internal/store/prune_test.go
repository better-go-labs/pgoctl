package store_test

import (
	"context"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publishDay is a test helper that publishes a single run for a given date.
func publishDay(ctx context.Context, t *testing.T, s store.Store, key, date string) {
	t.Helper()
	_, err := store.Publish(ctx, s, store.PublishOpts{
		Key:          key,
		Date:         date,
		RunID:        "r-" + date,
		ProfileData:  storetest.NewProfile(t, 100),
		QualityScore: 0.7,
		WindowOpts:   store.WindowOpts{Today: date},
	})
	require.NoError(t, err, "publishing %s", date)
}

func TestPrune_RemovesDaysOutsideFullWindow(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	// Publish 9 days; window is max 7, lookback 30 from today 2026-10-31.
	for _, d := range []string{
		"2026-10-23", "2026-10-24", "2026-10-25", "2026-10-26",
		"2026-10-27", "2026-10-28", "2026-10-29", "2026-10-30", "2026-10-31",
	} {
		publishDay(ctx, t, s, "pk", d)
	}

	opts := store.WindowOpts{Today: "2026-10-31", LookbackDays: 30, MaxProfiles: 7}
	require.NoError(t, store.Prune(ctx, s, "pk", opts))

	days, err := s.ListDays(ctx, "pk")
	require.NoError(t, err)
	// 9 days published; window cap is 7; window contains the 7 newest.
	assert.LessOrEqual(t, len(days), 7, "must not exceed MaxProfiles after prune")
	for _, d := range days {
		assert.GreaterOrEqual(t, d.Date, "2026-10-25", "pruned days must not appear")
	}
}

func TestPrune_KeepsDaysWhenWindowIsNotFull(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	// Publish 3 days; window max is 7, so window is not full.
	for _, d := range []string{"2026-10-29", "2026-10-30", "2026-10-31"} {
		publishDay(ctx, t, s, "sparse", d)
	}

	opts := store.WindowOpts{Today: "2026-10-31", LookbackDays: 30, MaxProfiles: 7}
	require.NoError(t, store.Prune(ctx, s, "sparse", opts))

	days, err := s.ListDays(ctx, "sparse")
	require.NoError(t, err)
	// Window is not full; nothing should be deleted.
	assert.Len(t, days, 3, "no days must be deleted when window is not full")
}

func TestPrune_NeverDeletesRestoreDay(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	// Publish enough days to fill the window.
	for _, d := range []string{
		"2026-10-25", "2026-10-26", "2026-10-27", "2026-10-28",
		"2026-10-29", "2026-10-30", "2026-10-31",
	} {
		publishDay(ctx, t, s, "protect", d)
	}

	opts := store.WindowOpts{Today: "2026-10-31", LookbackDays: 30, MaxProfiles: 7}
	require.NoError(t, store.Prune(ctx, s, "protect", opts))

	// Restore must still succeed: the protected day was not deleted.
	restoreDay, _, err := store.Restore(ctx, s, "protect", opts)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-31", restoreDay.Date)
}

func TestPrune_EmptyStore_NoError(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()
	require.NoError(t, store.Prune(ctx, s, "empty", store.WindowOpts{Today: "2026-10-31"}))
}
