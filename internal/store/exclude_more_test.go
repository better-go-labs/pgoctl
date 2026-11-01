package store_test

import (
	"context"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// highestPublishedDay returns the highest published sequence day for date.
func highestPublishedDay(t *testing.T, s store.Store, date string) store.Day {
	t.Helper()
	days, err := s.ListDays(context.Background(), testKey)
	require.NoError(t, err)
	d := mustDate(t, date)
	var best store.Day
	found := false
	for _, day := range days {
		if day.Date == d && !day.Draft && (!found || day.Seq > best.Seq) {
			best, found = day, true
		}
	}
	require.True(t, found, "no published day for %s", date)
	return best
}

func TestExclude_Idempotent(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.5, 1000)
	publishRun(t, s, "2026-10-31", "100-2", 0.9, 1000)

	first := excludeRun(t, s, "2026-10-31", "100-2")
	require.False(t, first.AlreadyExcluded)
	assert.Equal(t, []string{"2026-10-31"}, strs(first.Rebuilt))
	afterFirst := dayManifest(t, s, "2026-10-31").DailyRun

	second := excludeRun(t, s, "2026-10-31", "100-2")
	assert.True(t, second.AlreadyExcluded, "excluding an already-excluded run is a no-op")
	assert.Empty(t, second.Rebuilt, "a no-op exclude rebuilds nothing")
	assert.Equal(t, afterFirst, dayManifest(t, s, "2026-10-31").DailyRun, "the daily run is unchanged")
}

func TestExclude_AllRunsOfDay(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "100-2", 0.5, 1000)

	excludeRun(t, s, "2026-10-31", "100-1")
	excludeRun(t, s, "2026-10-31", "100-2")

	m := dayManifest(t, s, "2026-10-31")
	assert.Empty(t, m.DailyRun, "excluding every run leaves no daily run")
	day := highestPublishedDay(t, s, "2026-10-31")
	assert.False(t, day.Has(store.AssetDefault), "a day with no usable run has no default.pgo")
}

func TestExclude_ImmutableCreatesSequences(t *testing.T) {
	s := storetest.NewMemory(storetest.Immutable())
	publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000) // sequence 0
	publishRun(t, s, "2026-10-31", "100-2", 0.5, 1000) // sequence 1, supersedes 0

	excludeRun(t, s, "2026-10-31", "100-1")

	day := highestPublishedDay(t, s, "2026-10-31")
	assert.Equal(t, 2, day.Seq, "excluding on an immutable backend opens a new sequence")
	m := dayManifest(t, s, "2026-10-31")
	require.NotEmpty(t, m.Supersedes)
	assert.Equal(t, "profile/2026-10-31.1", m.Supersedes[len(m.Supersedes)-1])
	assert.Equal(t, "100-2", m.DailyRun, "the remaining run becomes the daily run")
}

func TestExclude_RecordsExclusion(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.5, 1000)
	publishRun(t, s, "2026-10-31", "100-2", 0.9, 1000)

	excludeRun(t, s, "2026-10-31", "100-1")

	m := dayManifest(t, s, "2026-10-31")
	var run *store.Run
	for i := range m.Runs {
		if m.Runs[i].ID == "100-1" {
			run = &m.Runs[i]
		}
	}
	require.NotNil(t, run)
	assert.True(t, run.Excluded)
	assert.Equal(t, "byte-bsl", run.ExcludedBy)
	assert.Equal(t, "load test traffic", run.Reason)
	require.NotNil(t, run.ExcludedAt)

	require.Len(t, m.Exclusions, 1)
	assert.Equal(t, "2026-10-31", m.Exclusions[0].Date)
	assert.Equal(t, "100-1", m.Exclusions[0].Run)
	assert.Equal(t, "load test traffic", m.Exclusions[0].Reason)
	assert.Equal(t, "byte-bsl", m.Exclusions[0].By)
}

func TestExclude_RebuildOrderOldestFirst(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-29", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-30", "101-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "102-1", 0.9, 1000)

	res := excludeRun(t, s, "2026-10-29", "100-1")
	assert.Equal(t, []string{"2026-10-29", "2026-10-30", "2026-10-31"}, strs(res.Rebuilt),
		"dependent days rebuild oldest first so each reads the updated earlier dailies")
}

func TestExclude_SliceHasOneEntry(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-29", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-30", "101-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "102-1", 0.9, 1000)

	excludeRun(t, s, "2026-10-29", "100-1")

	// The spec: a snapshot's exclusions list each excluded run that was in its
	// window. 2026-10-29 was in the window of every rebuilt day, so each of them
	// records that exclusion exactly once, naming the excluded run's date.
	for _, date := range []string{"2026-10-29", "2026-10-30", "2026-10-31"} {
		ex := dayManifest(t, s, date).Exclusions
		require.Len(t, ex, 1, "every rebuilt day records the excluded run once")
		assert.Equal(t, "2026-10-29", ex[0].Date, "the exclusion names the excluded run's date")
		assert.Equal(t, "100-1", ex[0].Run)
	}
}
