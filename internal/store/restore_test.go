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

func TestRestore_NoSnapshot(t *testing.T) {
	var buf bytes.Buffer
	_, err := store.Restore(context.Background(), storetest.NewMemory(), testKey, &buf)
	require.ErrorIs(t, err, store.ErrNoSnapshot)
}

func TestRestore_ReturnsNewestDate(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-30", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "101-1", 0.9, 1000)

	var buf bytes.Buffer
	m, err := store.Restore(ctx, s, testKey, &buf)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-31", m.Date)
	assert.NotEmpty(t, buf.Bytes())
}

func TestRestore_SkipsCorruptToPriorDate(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-30", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "101-1", 0.9, 1000)

	// Corrupt the newest default.pgo so its sha256 no longer matches its manifest.
	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	require.NoError(t, s.PutAsset(ctx, days[0], store.AssetDefault, bytes.NewReader([]byte("corrupt"))))

	var buf bytes.Buffer
	m, err := store.Restore(ctx, s, testKey, &buf)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-30", m.Date, "a corrupt newest default falls back to the prior date")
}

func TestRestore_SkipsDraft(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	// A bare draft with no assets must not be restored.
	_, err := s.BeginDay(ctx, testKey, mustDate(t, "2026-10-31"), 0)
	require.NoError(t, err)

	var buf bytes.Buffer
	_, err = store.Restore(ctx, s, testKey, &buf)
	require.ErrorIs(t, err, store.ErrNoSnapshot)
}
