package storetest_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/pprofutil"
	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemory_Contract runs the shared Store contract against the in-memory store.
func TestMemory_Contract(t *testing.T) {
	storetest.RunContract(t, func() store.Store { return storetest.NewMemory() })
}

// TestProfile_Valid checks the synthetic profile helper produces a parseable
// CPU profile whose total matches the requested sample count.
func TestProfile_Valid(t *testing.T) {
	data := storetest.NewProfile(t, 1234)
	p, err := profile.ParseData(data)
	require.NoError(t, err)

	idx, ok := pprofutil.CPUSampleIndex(p)
	require.True(t, ok)

	var total int64
	for _, s := range p.Sample {
		total += s.Value[idx]
	}
	assert.Equal(t, int64(1234), total)
}

// TestMemory_ImmutableOption validates the option the publish and exclude flows
// depend on: a published day is locked and forces a new sequence.
func TestMemory_ImmutableOption(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory(storetest.Immutable())
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	date, _ := store.ParseDate("2026-10-31")

	day, err := s.BeginDay(ctx, key, date, 0)
	require.NoError(t, err)
	require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, bytes.NewReader([]byte("{}"))))
	require.NoError(t, s.Publish(ctx, day))

	days, err := s.ListDays(ctx, key)
	require.NoError(t, err)
	require.Len(t, days, 1)
	published := days[0]
	assert.True(t, published.Immutable, "published day must be immutable")

	err = s.PutAsset(ctx, published, "x", bytes.NewReader([]byte("y")))
	require.ErrorIs(t, err, store.ErrImmutable)
	err = s.DeleteAsset(ctx, published, store.AssetManifest)
	require.ErrorIs(t, err, store.ErrImmutable)

	// A new sequence can still be created on top of an immutable day.
	next, err := s.BeginDay(ctx, key, date, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, next.Seq)
	assert.True(t, next.Draft)
}

// TestMemory_FailRead_Transient guards the single-shot read failure the
// AssetVanishes publish test depends on.
func TestMemory_FailRead_Transient(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory(storetest.FailRead(store.AssetDaily))
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	date, _ := store.ParseDate("2026-10-31")

	day, err := s.BeginDay(ctx, key, date, 0)
	require.NoError(t, err)
	require.NoError(t, s.PutAsset(ctx, day, store.AssetDaily, bytes.NewReader([]byte("payload"))))
	require.NoError(t, s.Publish(ctx, day))

	days, err := s.ListDays(ctx, key)
	require.NoError(t, err)
	require.Len(t, days, 1)

	_, err = s.ReadAsset(ctx, days[0], store.AssetDaily)
	require.ErrorIs(t, err, store.ErrNotFound, "first read fails once")

	rc, err := s.ReadAsset(ctx, days[0], store.AssetDaily)
	require.NoError(t, err, "second read succeeds")
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, []byte("payload"), got)
}
