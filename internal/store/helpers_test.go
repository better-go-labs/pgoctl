package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/stretchr/testify/require"
)

// sha256hex mirrors the unexported store hash used to record asset digests, so a
// manually built manifest matches the digests Restore verifies.
func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// putPublishedDay writes a published day for date at seq with the given daily and
// default bytes plus a matching manifest, and returns the created day. A nil
// slice omits that asset. It builds store states the normal Publish flow would
// prune or never produce, so Restore and Prune edge cases can be exercised in
// isolation. The manifest schema and digests are valid, so the day is usable
// unless a caller later overwrites an asset to break its digest.
func putPublishedDay(t *testing.T, s store.Store, key store.Key, date string, seq int, daily, def []byte) store.Day {
	t.Helper()
	ctx := context.Background()
	d := mustDate(t, date)
	day, err := s.BeginDay(ctx, key, d, seq)
	require.NoError(t, err)

	m := &store.Manifest{
		SchemaVersion: store.SchemaVersion,
		Key:           store.ManifestKey{Service: key.Service},
		Date:          date,
		Tag:           key.Tag(d, seq),
		Sequence:      seq,
		Assets:        map[string]string{},
	}
	if daily != nil {
		require.NoError(t, s.PutAsset(ctx, day, store.AssetDaily, bytes.NewReader(daily)))
		m.Assets[store.AssetDaily] = sha256hex(daily)
	}
	if def != nil {
		require.NoError(t, s.PutAsset(ctx, day, store.AssetDefault, bytes.NewReader(def)))
		m.Assets[store.AssetDefault] = sha256hex(def)
	}
	var buf bytes.Buffer
	require.NoError(t, m.Encode(&buf))
	require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, bytes.NewReader(buf.Bytes())))
	require.NoError(t, s.Publish(ctx, day))

	days, err := s.ListDays(ctx, key)
	require.NoError(t, err)
	for _, got := range days {
		if got.Date == d && got.Seq == seq {
			return got
		}
	}
	t.Fatalf("published day %s.%d not found after write", date, seq)
	return store.Day{}
}
