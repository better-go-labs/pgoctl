package store_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pub publishes one run with full control over the window parameters and capture
// time, for tests that need a non-default window or a specific ordering.
func pub(t *testing.T, s store.Store, date, runID string, quality float64, cpu int64, lookback, maxProfiles int, captured time.Time) *store.PublishResult {
	t.Helper()
	d := mustDate(t, date)
	res, err := store.Publish(context.Background(), s, store.PublishRequest{
		Key:          testKey,
		Today:        d,
		Branch:       "main",
		Run:          store.RunInfo{ID: runID, QualityScore: quality, CapturedAt: captured},
		Profile:      storetest.NewProfile(t, cpu),
		LookbackDays: lookback,
		MaxProfiles:  maxProfiles,
		GoVersion:    "go1.25",
	})
	require.NoError(t, err)
	return res
}

func TestPublish_SecondRunSameDay(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.5, 1000)
	res := publishRun(t, s, "2026-10-31", "100-2", 0.9, 1000)

	require.Len(t, res.Manifest.Runs, 2, "a second run on the same day is appended, not replaced")
	assert.Equal(t, "100-2", res.Manifest.DailyRun)

	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	require.Len(t, days, 1, "a mutable backend keeps both runs in one day")
	assert.Equal(t, 0, days[0].Seq)
}

func TestPublish_SameDayMutableKeepsSeqZero(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
	res := publishRun(t, s, "2026-10-31", "100-2", 0.9, 1000)

	assert.Equal(t, 0, res.Manifest.Sequence, "a mutable day is rewritten in place at sequence 0")
	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.Equal(t, 0, days[0].Seq)
}

func TestPublish_TieNewerRunWins(t *testing.T) {
	s := storetest.NewMemory()
	d := mustDate(t, "2026-10-31")
	pub(t, s, "2026-10-31", "100-1", 0.9, 1000, 14, 7, d.Time())
	res := pub(t, s, "2026-10-31", "100-2", 0.9, 1000, 14, 7, d.Time().Add(time.Hour))

	assert.Equal(t, "100-2", res.Manifest.DailyRun, "on an equal quality score the newer capture wins")
}

func TestPublish_NormalizesDays(t *testing.T) {
	s := storetest.NewMemory()
	// The two days carry very different CPU totals; the window normalizes them to
	// one total before merging, so the low-sample day still contributes. Each day
	// uses a distinct function so its share of the merged default.pgo is readable.
	// Captures are at noon, 24h apart, so the older day decays to half weight and
	// lands a one-third share, but only when the newer (own) day is weighted by
	// its run's capture time rather than the date's midnight.
	noon30 := mustDate(t, "2026-10-30").Time().Add(12 * time.Hour)
	noon31 := mustDate(t, "2026-10-31").Time().Add(12 * time.Hour)
	publishProfile(t, s, "2026-10-30", "100-1", cpuProfileFn(t, "main.g", 100), noon30)
	res := publishProfile(t, s, "2026-10-31", "101-1", cpuProfileFn(t, "main.h", 1000), noon31)

	assert.Equal(t, []string{"2026-10-31", "2026-10-30"}, res.Manifest.Window.Dates)
	assert.True(t, res.Manifest.Merge.Normalize, "the store normalizes every window input")
	require.NotEmpty(t, res.Default)

	shares := functionCPUShares(t, res.Default)
	assert.InDelta(t, 1.0/3.0, shares["main.g"], 0.01,
		"the older day, normalized and decayed to half weight, holds a third of the merge")
}

func TestPublish_SameRunTwice(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
	res := publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)

	require.Len(t, res.Manifest.Runs, 1, "re-publishing the same run id upserts rather than duplicating")
	assert.Equal(t, "100-1", res.Manifest.Runs[0].ID)
	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	require.Len(t, days, 1)
}

func TestPublish_WindowNewestSeven(t *testing.T) {
	s := storetest.NewMemory()
	dates := []string{
		"2026-10-24", "2026-10-25", "2026-10-26", "2026-10-27",
		"2026-10-28", "2026-10-29", "2026-10-30", "2026-10-31",
	}
	var res *store.PublishResult
	for i, d := range dates {
		res = publishRun(t, s, d, run(i), 0.9, 1000)
	}
	require.Len(t, res.Manifest.Window.Dates, 7, "max profiles caps the window at seven days")
	assert.Equal(t, "2026-10-31", res.Manifest.Window.Dates[0], "the window is newest first")
	assert.Equal(t, "2026-10-25", res.Manifest.Window.Dates[6])
}

func TestPublish_SkipsDraftDays(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	// An unpublished draft for a prior date must not enter the window.
	draft, err := s.BeginDay(ctx, testKey, mustDate(t, "2026-10-30"), 0)
	require.NoError(t, err)
	require.NoError(t, s.PutAsset(ctx, draft, store.AssetDaily, bytes.NewReader(storetest.NewProfile(t, 1000))))

	res := publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
	assert.Equal(t, []string{"2026-10-31"}, res.Manifest.Window.Dates, "a draft prior day is excluded")
}

func TestPublish_MissingRun(t *testing.T) {
	s := storetest.NewMemory(storetest.FailRead(store.RunAsset("100-1")))
	d := mustDate(t, "2026-10-31")
	res, err := store.Publish(context.Background(), s, store.PublishRequest{
		Key:          testKey,
		Today:        d,
		Run:          store.RunInfo{ID: "100-1", QualityScore: 0.9, CapturedAt: d.Time()},
		Profile:      storetest.NewProfile(t, 1000),
		LookbackDays: 14,
		MaxProfiles:  7,
	})
	require.NoError(t, err)
	require.Len(t, res.Manifest.Runs, 1)
	assert.True(t, res.Manifest.Runs[0].Missing, "a run whose asset cannot be read is marked missing")
	assert.Empty(t, res.Manifest.DailyRun, "no run is selected when the only run is missing")
}

func TestPublish_EmptyWindowWritesNoDefault(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory(storetest.FailRead(store.RunAsset("100-1")))
	d := mustDate(t, "2026-10-31")
	res, err := store.Publish(ctx, s, store.PublishRequest{
		Key:          testKey,
		Today:        d,
		Run:          store.RunInfo{ID: "100-1", QualityScore: 0.9, CapturedAt: d.Time()},
		Profile:      storetest.NewProfile(t, 1000),
		LookbackDays: 14,
		MaxProfiles:  7,
	})
	require.NoError(t, err)
	assert.Nil(t, res.Default, "an empty window produces no default.pgo")

	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.False(t, days[0].Has(store.AssetDefault), "no default.pgo asset is written")
	assert.False(t, days[0].Has(store.AssetDaily), "no daily.pprof asset is written")
}

func TestPublish_ReusesLeftoverDraft(t *testing.T) {
	ctx := context.Background()

	t.Run("reuses an empty draft at the same sequence", func(t *testing.T) {
		s := storetest.NewMemory()
		// A crashed prior run left an empty draft at sequence 0.
		_, err := s.BeginDay(ctx, testKey, mustDate(t, "2026-10-31"), 0)
		require.NoError(t, err)

		res := publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
		assert.Equal(t, 0, res.Manifest.Sequence)

		days, err := s.ListDays(ctx, testKey)
		require.NoError(t, err)
		require.Len(t, days, 1, "the leftover draft is reused, not duplicated")
		assert.False(t, days[0].Draft)
		assert.True(t, days[0].Has(store.RunAsset("100-1")))
	})

	t.Run("seeds a higher-sequence draft from the published lower sequence", func(t *testing.T) {
		s := storetest.NewMemory(storetest.Immutable())
		publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000) // sequence 0, now immutable
		// A crashed run left an empty draft at the next sequence.
		_, err := s.BeginDay(ctx, testKey, mustDate(t, "2026-10-31"), 1)
		require.NoError(t, err)

		res := publishRun(t, s, "2026-10-31", "100-2", 0.9, 1000)
		assert.Equal(t, 1, res.Manifest.Sequence)
		require.NotEmpty(t, res.Manifest.Supersedes)
		assert.Equal(t, "profile/2026-10-31.0", res.Manifest.Supersedes[0])
		assert.Len(t, res.Manifest.Runs, 2, "the seeded draft carries the earlier run plus the new one")
	})
}

func TestPublish_CopiesRunsToNewSequence(t *testing.T) {
	ctx := context.Background()
	// The backend does not enumerate run files in its day listing, so the flow
	// must copy runs forward from the manifest when it opens a new sequence.
	s := storetest.NewMemory(storetest.Immutable(), storetest.HideRunAssets())
	publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000) // sequence 0, now immutable
	publishRun(t, s, "2026-10-31", "100-2", 0.5, 1000) // sequence 1, supersedes 0

	day := highestPublishedDay(t, s, "2026-10-31")
	require.Equal(t, 1, day.Seq)
	rc, err := s.ReadAsset(ctx, day, store.RunAsset("100-1"))
	require.NoError(t, err, "the earlier run's profile is copied onto the new sequence")
	require.NoError(t, rc.Close())
}

func TestPublish_CopyRunsSkipsMissingFile(t *testing.T) {
	s := storetest.NewMemory(storetest.Immutable())
	publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000) // sequence 0, now immutable
	// The earlier run's file has vanished when the next sequence opens; copyRuns
	// skips it rather than failing the publish.
	storetest.Arm(s, storetest.FailRead(store.RunAsset("100-1")))
	res := publishRun(t, s, "2026-10-31", "100-2", 0.5, 1000)

	assert.Equal(t, 1, res.Manifest.Sequence)
	assert.Equal(t, "100-2", res.Manifest.DailyRun, "the surviving run becomes the daily run")
}

func TestPublish_WindowUsesHighestSequence(t *testing.T) {
	s := storetest.NewMemory()
	// Date 2026-10-30 has two published sequences. Only the highest sequence (1)
	// carries a daily.pprof, so the window can include that date only if it reads
	// the highest sequence.
	putPublishedDay(t, s, testKey, "2026-10-30", 0, nil, []byte("default-seq0"))
	putPublishedDay(t, s, testKey, "2026-10-30", 1, storetest.NewProfile(t, 1000), []byte("default-seq1"))

	res := publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
	assert.Equal(t, []string{"2026-10-31", "2026-10-30"}, res.Manifest.Window.Dates,
		"the window reads the highest published sequence of the prior date")
}

func TestPublish_WindowDropsMissingDailyPprof(t *testing.T) {
	s := storetest.NewMemory()
	// A published prior day with no daily.pprof must be dropped from the window.
	putPublishedDay(t, s, testKey, "2026-10-30", 0, nil, []byte("default-a"))

	res := publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
	assert.Equal(t, []string{"2026-10-31"}, res.Manifest.Window.Dates,
		"a prior day without a daily.pprof is skipped")
}

func TestPublish_DropsUnreadableDailyBeforeWindowCap(t *testing.T) {
	s := storetest.NewMemory()
	// A prior published day with no daily.pprof sits between today and an older
	// readable day. With maxProfiles 2, selecting the window before dropping the
	// unreadable day wastes a slot on it, so the older readable day never enters
	// the window.
	putPublishedDay(t, s, testKey, "2026-10-29", 0, storetest.NewProfile(t, 1000), []byte("default-29"))
	putPublishedDay(t, s, testKey, "2026-10-30", 0, nil, []byte("default-30"))
	res := pub(t, s, "2026-10-31", "100-1", 0.9, 1000, 14, 2, mustDate(t, "2026-10-31").Time())

	assert.Equal(t, []string{"2026-10-31", "2026-10-29"}, res.Manifest.Window.Dates,
		"the unreadable day is dropped before the cap so the older readable day fills the slot")
}

func TestPublish_CapturedAtZeroFallsBackToNow(t *testing.T) {
	s := storetest.NewMemory()
	// The prior day's selected run has no capture time. Rather than treat it as
	// epoch (ancient, zero weight), the window weights it by the day's own date so
	// it still contributes.
	pub(t, s, "2026-10-30", "100-1", 0.9, 1000, 14, 7, time.Time{})
	res := pub(t, s, "2026-10-31", "101-1", 0.9, 1000, 14, 7, mustDate(t, "2026-10-31").Time())

	assert.Equal(t, []string{"2026-10-31", "2026-10-30"}, res.Manifest.Window.Dates)
	assert.NotEmpty(t, res.Default, "a run with no capture time still contributes to the merge")
}

func TestPublish_Prunes(t *testing.T) {
	s := storetest.NewMemory()
	pub(t, s, "2026-10-29", "100-1", 0.9, 1000, 14, 2, mustDate(t, "2026-10-29").Time())
	pub(t, s, "2026-10-30", "101-1", 0.9, 1000, 14, 2, mustDate(t, "2026-10-30").Time())
	res := pub(t, s, "2026-10-31", "102-1", 0.9, 1000, 14, 2, mustDate(t, "2026-10-31").Time())

	require.NoError(t, res.PruneErr)
	assert.Equal(t, []string{"2026-10-29"}, strs(res.Pruned), "publishing prunes the day pushed out of the window")
}
