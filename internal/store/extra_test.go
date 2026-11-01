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

func TestPrune_DoesNotDefaultInvalidWindow(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	for i, d := range []string{"2026-10-25", "2026-10-29", "2026-10-30", "2026-10-31"} {
		publishRun(t, s, d, run(i), 0.9, 1000)
	}

	// A non-positive lookback is the caller's input error, which the CLI rejects
	// with exit 2. The store must not silently rescue it to the 14-day default and
	// sweep days a 14-day window would; with lookback 0 no date is in the window,
	// so nothing is pruned.
	deleted, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 0, 2)
	require.NoError(t, err)
	assert.Empty(t, deleted, "an invalid lookback is not defaulted, so prune sweeps nothing")
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
