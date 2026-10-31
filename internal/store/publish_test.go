package store_test

import (
	"context"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectDailyRun(t *testing.T) {
	tests := []struct {
		name    string
		runs    []store.Asset
		wantID  string
		wantNil bool
	}{
		{
			name:    "nil runs returns nil",
			runs:    nil,
			wantNil: true,
		},
		{
			name:    "all excluded returns nil",
			runs:    []store.Asset{{RunID: "a", Excluded: true}, {RunID: "b", Excluded: true}},
			wantNil: true,
		},
		{
			name:   "highest quality score wins",
			runs:   []store.Asset{{RunID: "a", QualityScore: 0.5}, {RunID: "b", QualityScore: 0.9}},
			wantID: "b",
		},
		{
			name:   "excluded runs are skipped",
			runs:   []store.Asset{{RunID: "a", QualityScore: 0.9, Excluded: true}, {RunID: "b", QualityScore: 0.5}},
			wantID: "b",
		},
		{
			name:   "tie broken by last in slice",
			runs:   []store.Asset{{RunID: "a", QualityScore: 0.7}, {RunID: "b", QualityScore: 0.7}},
			wantID: "b",
		},
		{
			name:   "single non-excluded run is selected",
			runs:   []store.Asset{{RunID: "x", QualityScore: 1.0}},
			wantID: "x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.SelectDailyRun(tt.runs)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantID, got.RunID)
		})
	}
}

func TestPublish_CommitsMakesDay(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	profileData := storetest.NewProfile(t, 1000)
	opts := store.PublishOpts{
		Key:          "testkey",
		Date:         "2026-10-31",
		RunID:        "run-1",
		ProfileData:  profileData,
		QualityScore: 0.8,
		WindowOpts:   store.WindowOpts{Today: "2026-10-31"},
	}

	day, err := store.Publish(ctx, s, opts)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-31", day.Date)

	days, err := s.ListDays(ctx, "testkey")
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.Equal(t, "2026-10-31", days[0].Date)
}

func TestPublish_ManifestRecordsRun(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	profileData := storetest.NewProfile(t, 500)
	opts := store.PublishOpts{
		Key:          "mankey",
		Date:         "2026-10-30",
		RunID:        "run-42",
		ProfileData:  profileData,
		QualityScore: 0.75,
		WindowOpts:   store.WindowOpts{Today: "2026-10-30"},
	}

	_, err := store.Publish(ctx, s, opts)
	require.NoError(t, err)

	m, err := s.GetManifest(ctx, "mankey", store.Day{Date: "2026-10-30"})
	require.NoError(t, err)
	require.Len(t, m.Runs, 1)
	assert.Equal(t, "run-42", m.Runs[0].RunID)
	assert.Equal(t, "run-42", m.DailyRun)
	assert.Equal(t, 1, m.SchemaVersion)
}

func TestPublish_AssetsExist(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	profileData := storetest.NewProfile(t, 100)
	opts := store.PublishOpts{
		Key:          "assetkey",
		Date:         "2026-10-29",
		RunID:        "run-7",
		ProfileData:  profileData,
		QualityScore: 0.6,
		WindowOpts:   store.WindowOpts{Today: "2026-10-29"},
	}
	_, err := store.Publish(ctx, s, opts)
	require.NoError(t, err)

	d := store.Day{Date: "2026-10-29"}
	for _, name := range []string{"daily.pprof", "default.pgo", "manifest.json"} {
		data, err := s.GetAsset(ctx, "assetkey", d, name)
		require.NoError(t, err, "asset %s must exist", name)
		assert.NotEmpty(t, data, "asset %s must not be empty", name)
	}
}

func TestPublish_SecondRunSelectsBest(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemoryStore()

	opts1 := store.PublishOpts{
		Key:          "dualrun",
		Date:         "2026-10-28",
		RunID:        "low",
		ProfileData:  storetest.NewProfile(t, 100),
		QualityScore: 0.4,
		WindowOpts:   store.WindowOpts{Today: "2026-10-28"},
	}
	_, err := store.Publish(ctx, s, opts1)
	require.NoError(t, err)

	opts2 := store.PublishOpts{
		Key:          "dualrun",
		Date:         "2026-10-28",
		RunID:        "high",
		ProfileData:  storetest.NewProfile(t, 200),
		QualityScore: 0.9,
		WindowOpts:   store.WindowOpts{Today: "2026-10-28"},
	}
	_, err = store.Publish(ctx, s, opts2)
	require.NoError(t, err)

	m, err := s.GetManifest(ctx, "dualrun", store.Day{Date: "2026-10-28"})
	require.NoError(t, err)
	assert.Equal(t, "high", m.DailyRun, "second run has higher quality score and must be selected")
	assert.Len(t, m.Runs, 2, "both runs must be recorded")
}
