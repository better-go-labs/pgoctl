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

func TestPrune_AppliesWindowDefaults(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-30", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "101-1", 0.9, 1000)

	// Non-positive window parameters fall back to the issue #62 defaults of 14
	// lookback days and 7 profiles.
	deleted, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 0, 0)
	require.NoError(t, err)
	assert.Empty(t, deleted, "with the default window the two recent days are kept")
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
