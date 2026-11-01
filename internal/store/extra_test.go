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

func TestDate_IsZero(t *testing.T) {
	assert.True(t, store.Date{}.IsZero())
	assert.False(t, mustDate(t, "2026-10-31").IsZero())
}

func TestPrune_RejectsNonPositiveWindow(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	for i, d := range []string{"2026-10-25", "2026-10-29", "2026-10-30", "2026-10-31"} {
		publishRun(t, s, d, run(i), 0.9, 1000)
	}

	// A non-positive lookback or maxProfiles is the caller's input error, which
	// the CLI rejects with exit 2. The store rejects it rather than silently
	// defaulting it or indexing inWindow[-1] when maxProfiles is 0.
	cases := []struct {
		name                   string
		lookbackDays, maxProfs int
	}{
		{name: "zero lookback", lookbackDays: 0, maxProfs: 2},
		{name: "zero maxProfiles", lookbackDays: 14, maxProfs: 0},
		{name: "negative maxProfiles", lookbackDays: 14, maxProfs: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deleted, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), tc.lookbackDays, tc.maxProfs)
			require.ErrorIs(t, err, store.ErrInvalidInput)
			assert.Nil(t, deleted, "a rejected prune deletes nothing")
		})
	}
}

func TestPublish_RejectsNonPositiveWindow(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()

	_, err := store.Publish(ctx, s, store.PublishRequest{
		Key:          testKey,
		Today:        mustDate(t, "2026-10-31"),
		Run:          store.RunInfo{ID: "100-1", QualityScore: 0.9, CapturedAt: mustDate(t, "2026-10-31").Time()},
		Profile:      storetest.NewProfile(t, 1000),
		LookbackDays: 14,
		MaxProfiles:  0,
	})
	require.ErrorIs(t, err, store.ErrInvalidInput)
}

func TestExclude_RejectsNonPositiveWindow(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()

	// A published day whose stored window is invalid, so Exclude rejects it rather
	// than feeding a non-positive window into the rebuild and prune.
	d := mustDate(t, "2026-10-31")
	day, err := s.BeginDay(ctx, testKey, d, 0)
	require.NoError(t, err)
	profile := storetest.NewProfile(t, 1000)
	require.NoError(t, s.PutAsset(ctx, day, store.RunAsset("100-1"), bytes.NewReader(profile)))
	require.NoError(t, s.PutAsset(ctx, day, store.AssetDaily, bytes.NewReader(profile)))
	m := &store.Manifest{
		SchemaVersion: store.SchemaVersion,
		Date:          "2026-10-31",
		Tag:           testKey.Tag(d, 0),
		DailyRun:      "100-1",
		Runs:          []store.Run{{ID: "100-1", QualityScore: 0.9, SHA256: sha256hex(profile)}},
		Window:        store.Window{LookbackDays: 14, MaxProfiles: 0},
		Assets:        map[string]string{store.AssetDaily: sha256hex(profile), store.RunAsset("100-1"): sha256hex(profile)},
	}
	var buf bytes.Buffer
	require.NoError(t, m.Encode(&buf))
	require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, bytes.NewReader(buf.Bytes())))
	require.NoError(t, s.Publish(ctx, day))

	_, err = store.Exclude(ctx, s, store.ExcludeRequest{
		Key: testKey, Date: d, RunID: "100-1", Now: d.Time(),
	})
	require.ErrorIs(t, err, store.ErrInvalidInput)
}

func TestPublish_ConcurrentUpdateServiceKey(t *testing.T) {
	ctx := context.Background()
	key := store.Key{Repo: "o/r", Prefix: "profile", Service: "api"}
	var s store.Store
	s = storetest.NewMemory(storetest.BeforeList(func(call int) {
		if call != 2 {
			return
		}
		d := mustDate(t, "2026-10-31")
		day, err := s.BeginDay(ctx, key, d, 9)
		require.NoError(t, err)
		require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, bytes.NewReader([]byte("{}"))))
		require.NoError(t, s.Publish(ctx, day))
	}))

	_, err := store.Publish(ctx, s, store.PublishRequest{
		Key:          key,
		Today:        mustDate(t, "2026-10-31"),
		Run:          store.RunInfo{ID: "100-1", QualityScore: 0.9, CapturedAt: time.Now()},
		Profile:      storetest.NewProfile(t, 1000),
		LookbackDays: 14,
		MaxProfiles:  7,
	})
	require.ErrorIs(t, err, store.ErrConcurrentUpdate)
	assert.Contains(t, err.Error(), "profile-api", "the conflict hint names the service key")
}
