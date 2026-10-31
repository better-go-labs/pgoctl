package store_test

import (
	"context"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestore_ReturnsNewestWindowDay(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	// Publish two days.
	for _, tc := range []struct {
		date  string
		runID string
	}{
		{"2026-10-29", "r1"},
		{"2026-10-30", "r2"},
	} {
		_, err := store.Publish(ctx, s, store.PublishOpts{
			Key:          "rk",
			Date:         tc.date,
			RunID:        tc.runID,
			ProfileData:  storetest.NewProfile(t, 100),
			QualityScore: 0.7,
			WindowOpts:   store.WindowOpts{Today: tc.date},
		})
		require.NoError(t, err)
	}

	day, data, err := store.Restore(ctx, s, "rk", store.WindowOpts{Today: "2026-10-31"})
	require.NoError(t, err)
	assert.Equal(t, "2026-10-30", day.Date, "newest window day must be selected")
	assert.NotEmpty(t, data, "default.pgo must be non-empty")
}

func TestRestore_ErrNotFound_EmptyStore(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	_, _, err := store.Restore(ctx, s, "nokey", store.WindowOpts{Today: "2026-10-31"})
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestRestore_ErrNotFound_AllOutsideWindow(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	// Publish a day outside the default 14-day window.
	_, err := store.Publish(ctx, s, store.PublishOpts{
		Key:          "old",
		Date:         "2026-10-01",
		RunID:        "r",
		ProfileData:  storetest.NewProfile(t, 100),
		QualityScore: 0.5,
		WindowOpts:   store.WindowOpts{Today: "2026-10-01"},
	})
	require.NoError(t, err)

	// Today is 30 days later; the day falls outside the 14-day window.
	_, _, err = store.Restore(ctx, s, "old", store.WindowOpts{Today: "2026-10-31"})
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestRestore_SkipsDayWithBadChecksum(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	// Publish a day normally so the manifest exists.
	_, err := store.Publish(ctx, s, store.PublishOpts{
		Key:          "ck",
		Date:         "2026-10-30",
		RunID:        "r1",
		ProfileData:  storetest.NewProfile(t, 100),
		QualityScore: 0.8,
		WindowOpts:   store.WindowOpts{Today: "2026-10-30"},
	})
	require.NoError(t, err)

	// Publish a second day that is newer and valid.
	_, err = store.Publish(ctx, s, store.PublishOpts{
		Key:          "ck",
		Date:         "2026-10-31",
		RunID:        "r2",
		ProfileData:  storetest.NewProfile(t, 200),
		QualityScore: 0.9,
		WindowOpts:   store.WindowOpts{Today: "2026-10-31"},
	})
	require.NoError(t, err)

	// Restore with a tampered manifest for 2026-10-31 so that day is skipped.
	// We simulate this by overwriting the manifest so the sha256 is wrong.
	d31 := store.Day{Date: "2026-10-31"}
	draft, err := s.BeginDay(ctx, "ck", d31)
	require.NoError(t, err)
	// Write a manifest with a wrong sha256 for default.pgo.
	require.NoError(t, draft.PutManifest(ctx, &store.Manifest{
		SchemaVersion: 1,
		Date:          "2026-10-31",
		SHA256:        map[string]string{"default.pgo": "badhash"},
	}))
	require.NoError(t, draft.Commit(ctx))

	day, _, err := store.Restore(ctx, s, "ck", store.WindowOpts{Today: "2026-10-31"})
	require.NoError(t, err)
	assert.Equal(t, "2026-10-30", day.Date, "bad-checksum day must be skipped; fallback to previous")
}
