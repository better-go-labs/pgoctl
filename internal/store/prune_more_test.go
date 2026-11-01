package store_test

import (
	"context"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publishKey publishes one run for date under an arbitrary key.
func publishKey(t *testing.T, s store.Store, key store.Key, date, runID string) {
	t.Helper()
	d := mustDate(t, date)
	_, err := store.Publish(context.Background(), s, store.PublishRequest{
		Key:          key,
		Today:        d,
		Branch:       "main",
		Run:          store.RunInfo{ID: runID, QualityScore: 0.9, CapturedAt: d.Time()},
		Profile:      storetest.NewProfile(t, 1000),
		LookbackDays: 14,
		MaxProfiles:  7,
	})
	require.NoError(t, err)
}

// datesOf returns the distinct dates currently stored under testKey.
func datesOf(t *testing.T, s store.Store) map[string]bool {
	t.Helper()
	days, err := s.ListDays(context.Background(), testKey)
	require.NoError(t, err)
	out := make(map[string]bool)
	for _, d := range days {
		out[d.Date.String()] = true
	}
	return out
}

func TestPrune_DeletesSuperseded(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	// Two published sequences for one date; the lower is superseded.
	putPublishedDay(t, s, testKey, "2026-10-31", 0, storetest.NewProfile(t, 1000), []byte("default-0"))
	putPublishedDay(t, s, testKey, "2026-10-31", 1, storetest.NewProfile(t, 1000), []byte("default-1"))

	_, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 14, 7)
	require.NoError(t, err)

	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	require.Len(t, days, 1, "the superseded sequence is deleted")
	assert.Equal(t, 1, days[0].Seq, "the highest sequence is kept")
}

func TestPrune_DeletesSupersededDraft(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	// A published sequence 1 plus a leftover draft at sequence 0 for the same
	// date. The draft is superseded by the higher published sequence and must be
	// deleted, not just the lower published sequences.
	putPublishedDay(t, s, testKey, "2026-10-31", 1, storetest.NewProfile(t, 1000), []byte("default-1"))
	_, err := s.BeginDay(ctx, testKey, mustDate(t, "2026-10-31"), 0)
	require.NoError(t, err)

	_, err = store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 14, 7)
	require.NoError(t, err)

	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	require.Len(t, days, 1, "the superseded draft is deleted")
	assert.Equal(t, 1, days[0].Seq)
	assert.False(t, days[0].Draft)
}

func TestPrune_ShortWindowKeepsOlder(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	// A day far outside the lookback, then two recent days. The window is not
	// full (2 < maxProfiles), so the old day is not swept.
	putPublishedDay(t, s, testKey, "2026-09-01", 0, storetest.NewProfile(t, 1000), []byte("default-old"))
	publishRun(t, s, "2026-10-30", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "101-1", 0.9, 1000)

	deleted, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 14, 7)
	require.NoError(t, err)
	assert.Empty(t, deleted, "a non-full window deletes nothing")
	assert.True(t, datesOf(t, s)["2026-09-01"], "the older day is kept while the window is short")
}

func TestPrune_ExactlyMaxDeletesOlder(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-30", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "101-1", 0.9, 1000)
	// An older in-lookback day that never produced a daily run, so it is not one
	// of the in-window dates but still sits before the cutoff.
	putPublishedDay(t, s, testKey, "2026-10-28", 0, nil, []byte("default-28"))

	deleted, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 14, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-10-28"}, strs(deleted),
		"with exactly max in-window days, an older day before the cutoff is swept")
	got := datesOf(t, s)
	assert.True(t, got["2026-10-30"] && got["2026-10-31"])
}

func TestPrune_FullWindowDeletesOlder(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-29", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-30", "101-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "102-1", 0.9, 1000)
	// A day far outside the lookback window.
	putPublishedDay(t, s, testKey, "2026-10-01", 0, storetest.NewProfile(t, 1000), []byte("default-oct1"))

	deleted, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 14, 2)
	require.NoError(t, err)
	got := strs(deleted)
	assert.Contains(t, got, "2026-10-01", "a full window sweeps the day outside the lookback")
	assert.Contains(t, got, "2026-10-29", "and the in-lookback day beyond the newest max")
	remaining := datesOf(t, s)
	assert.True(t, remaining["2026-10-30"] && remaining["2026-10-31"], "the newest max days plus restore target remain")
}

func TestPrune_KeyBoundary(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	other := store.Key{Repo: "o/r", Prefix: "other"}
	publishRun(t, s, "2026-10-27", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-30", "101-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "102-1", 0.9, 1000)
	publishKey(t, s, other, "2026-10-27", "200-1")

	_, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 14, 1)
	require.NoError(t, err)

	otherDays, err := s.ListDays(ctx, other)
	require.NoError(t, err)
	require.Len(t, otherDays, 1, "pruning one key never deletes another key's days")
	assert.Equal(t, "2026-10-27", otherDays[0].Date.String())
}
