package store_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestore_HighestSequence(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	// 2026-10-30 is a clean fallback. 2026-10-31 has two sequences: the highest
	// (1) is corrupt and the lower (0) is clean.
	putPublishedDay(t, s, testKey, "2026-10-30", 0, storetest.NewProfile(t, 1000), []byte("default-prev"))
	putPublishedDay(t, s, testKey, "2026-10-31", 0, storetest.NewProfile(t, 1000), []byte("default-31-0"))
	bad := putPublishedDay(t, s, testKey, "2026-10-31", 1, storetest.NewProfile(t, 1000), []byte("default-31-1"))

	// Break the digest of the highest sequence's default.pgo.
	require.NoError(t, s.PutAsset(ctx, bad, store.AssetDefault, bytes.NewReader([]byte("corrupt"))))

	var buf bytes.Buffer
	m, err := store.Restore(ctx, s, testKey, &buf)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-30", m.Date,
		"a corrupt highest sequence falls back to the previous date, never to the lower sequence of the same date")
}

func TestRestore_AssetVanishes(t *testing.T) {
	ctx := context.Background()
	// The newest date's default.pgo read fails once, modelling an asset that
	// vanished between listing and reading; Restore falls back to the prior date.
	s := storetest.NewMemory(storetest.FailRead(store.AssetDefault))
	putPublishedDay(t, s, testKey, "2026-10-30", 0, storetest.NewProfile(t, 1000), []byte("default-prev"))
	putPublishedDay(t, s, testKey, "2026-10-31", 0, storetest.NewProfile(t, 1000), []byte("default-newest"))

	var buf bytes.Buffer
	m, err := store.Restore(ctx, s, testKey, &buf)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-30", m.Date, "a vanished newest default falls back to the prior date")
	assert.Equal(t, []byte("default-prev"), buf.Bytes())
}

func TestRestore_KeyBoundary(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)

	other := store.Key{Repo: "o/r", Prefix: "other"}
	var buf bytes.Buffer
	_, err := store.Restore(ctx, s, other, &buf)
	require.ErrorIs(t, err, store.ErrNoSnapshot, "restore for one key never reads another key's days")
}

func TestRestore_SkipsUnknownSchema(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	putPublishedDay(t, s, testKey, "2026-10-30", 0, storetest.NewProfile(t, 1000), []byte("default-prev"))

	// The newest date carries a manifest from a future, unsupported schema.
	d := mustDate(t, "2026-10-31")
	day, err := s.BeginDay(ctx, testKey, d, 0)
	require.NoError(t, err)
	require.NoError(t, s.PutAsset(ctx, day, store.AssetDefault, bytes.NewReader([]byte("default-newest"))))
	future := &store.Manifest{
		SchemaVersion: store.SchemaVersion + 98,
		Date:          "2026-10-31",
		Tag:           testKey.Tag(d, 0),
		Assets:        map[string]string{store.AssetDefault: sha256hex([]byte("default-newest"))},
	}
	var mbuf bytes.Buffer
	require.NoError(t, future.Encode(&mbuf))
	require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, bytes.NewReader(mbuf.Bytes())))
	require.NoError(t, s.Publish(ctx, day))

	var buf bytes.Buffer
	m, err := store.Restore(ctx, s, testKey, &buf)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-30", m.Date, "an unknown schema is skipped to the prior date")
}
