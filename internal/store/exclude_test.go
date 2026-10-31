package store_test

import (
	"context"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExclude_MarksRunAndUpdatesDaily(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	// Publish two runs; high quality run is selected initially.
	for _, r := range []struct {
		id    string
		score float64
	}{
		{"low", 0.4},
		{"high", 0.9},
	} {
		_, err := store.Publish(ctx, s, store.PublishOpts{
			Key:          "ek",
			Date:         "2026-10-30",
			RunID:        r.id,
			ProfileData:  storetest.NewProfile(t, 100),
			QualityScore: r.score,
			WindowOpts:   store.WindowOpts{Today: "2026-10-30"},
		})
		require.NoError(t, err)
	}

	m, err := s.GetManifest(ctx, "ek", store.Day{Date: "2026-10-30"})
	require.NoError(t, err)
	assert.Equal(t, "high", m.DailyRun, "high-quality run must be selected before exclude")

	// Exclude the high-quality run.
	err = store.Exclude(ctx, s, store.ExcludeOpts{
		Key:    "ek",
		Date:   "2026-10-30",
		RunID:  "high",
		Reason: "load test traffic",
	})
	require.NoError(t, err)

	m, err = s.GetManifest(ctx, "ek", store.Day{Date: "2026-10-30"})
	require.NoError(t, err)
	assert.Equal(t, "low", m.DailyRun, "low-quality run must take over after high is excluded")

	// The excluded run must be marked in the manifest.
	var found bool
	for _, r := range m.Runs {
		if r.RunID == "high" {
			found = true
			assert.True(t, r.Excluded)
			assert.Equal(t, "load test traffic", r.ExcludeReason)
		}
	}
	assert.True(t, found, "high run must still appear in manifest")
}

func TestExclude_ErrNotFound_MissingDate(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	err := store.Exclude(ctx, s, store.ExcludeOpts{
		Key:   "ek2",
		Date:  "2026-10-30",
		RunID: "r1",
	})
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestExclude_ErrNoRuns_AllExcluded(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	_, err := store.Publish(ctx, s, store.PublishOpts{
		Key:          "ek3",
		Date:         "2026-10-30",
		RunID:        "only",
		ProfileData:  storetest.NewProfile(t, 100),
		QualityScore: 0.5,
		WindowOpts:   store.WindowOpts{Today: "2026-10-30"},
	})
	require.NoError(t, err)

	// Excluding the only run must fail with ErrNoRuns.
	err = store.Exclude(ctx, s, store.ExcludeOpts{
		Key:   "ek3",
		Date:  "2026-10-30",
		RunID: "only",
	})
	require.ErrorIs(t, err, store.ErrNoRuns)
}
