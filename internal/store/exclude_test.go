package store_test

import (
	"context"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dayManifest reads the manifest of the highest published sequence for date.
func dayManifest(t *testing.T, s store.Store, date string) *store.Manifest {
	t.Helper()
	ctx := context.Background()
	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	d := mustDate(t, date)
	for _, day := range days {
		if day.Date == d && !day.Draft {
			rc, err := s.ReadAsset(ctx, day, store.AssetManifest)
			require.NoError(t, err)
			defer func() { _ = rc.Close() }()
			m, err := store.DecodeManifest(rc)
			require.NoError(t, err)
			return m
		}
	}
	t.Fatalf("no published day for %s", date)
	return nil
}

func excludeRun(t *testing.T, s store.Store, date, runID string) *store.ExcludeResult {
	t.Helper()
	res, err := store.Exclude(context.Background(), s, store.ExcludeRequest{
		Key:    testKey,
		Date:   mustDate(t, date),
		RunID:  runID,
		Reason: "load test traffic",
		Actor:  "byte-bsl",
		Now:    mustDate(t, "2026-10-31").Time(),
	})
	require.NoError(t, err)
	return res
}

func TestExclude_SkippedWhenNoDay(t *testing.T) {
	res := excludeRun(t, storetest.NewMemory(), "2026-10-31", "100-1")
	assert.True(t, res.Skipped)
}

func TestExclude_RunNotFound(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
	_, err := store.Exclude(context.Background(), s, store.ExcludeRequest{
		Key: testKey, Date: mustDate(t, "2026-10-31"), RunID: "999-1", Now: mustDate(t, "2026-10-31").Time(),
	})
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestExclude_AlreadyExcluded(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.5, 1000)
	publishRun(t, s, "2026-10-31", "100-2", 0.9, 1000)

	first := excludeRun(t, s, "2026-10-31", "100-1")
	assert.False(t, first.AlreadyExcluded)
	second := excludeRun(t, s, "2026-10-31", "100-1")
	assert.True(t, second.AlreadyExcluded)
}

func TestExclude_ReselectsDailyRun(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000) // selected
	publishRun(t, s, "2026-10-31", "100-2", 0.5, 1000)
	require.Equal(t, "100-1", dayManifest(t, s, "2026-10-31").DailyRun)

	res := excludeRun(t, s, "2026-10-31", "100-1")
	assert.Equal(t, []string{"2026-10-31"}, strs(res.Rebuilt))

	m := dayManifest(t, s, "2026-10-31")
	assert.Equal(t, "100-2", m.DailyRun, "excluding the selected run promotes the next best")
	idx := -1
	for i, r := range m.Runs {
		if r.ID == "100-1" {
			idx = i
		}
	}
	require.GreaterOrEqual(t, idx, 0)
	assert.True(t, m.Runs[idx].Excluded)
	require.Len(t, m.Exclusions, 1)
}

func TestExclude_RebuildsDependentDays(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-30", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "200-1", 0.9, 1000)
	require.Equal(t, []string{"2026-10-31", "2026-10-30"}, dayManifest(t, s, "2026-10-31").Window.Dates)

	res := excludeRun(t, s, "2026-10-30", "100-1")
	// The excluded day and the later day whose window used it are both rebuilt.
	assert.Equal(t, []string{"2026-10-30", "2026-10-31"}, strs(res.Rebuilt))
	// With 2026-10-30 now empty, the later day's window drops it.
	assert.Equal(t, []string{"2026-10-31"}, dayManifest(t, s, "2026-10-31").Window.Dates)
}
