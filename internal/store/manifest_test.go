package store_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/merge"
	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifest_RoundTrip(t *testing.T) {
	raw, err := os.ReadFile("testdata/manifest_v1.json")
	require.NoError(t, err)

	m, err := store.DecodeManifest(bytes.NewReader(raw))
	require.NoError(t, err)
	assert.Equal(t, 1, m.SchemaVersion)
	assert.Equal(t, "api", m.Key.Service)
	assert.Equal(t, "profile/api/2026-10-26.0", m.Tag)
	assert.Equal(t, "11873365122-1", m.DailyRun)
	require.Len(t, m.Runs, 2)
	assert.True(t, m.Runs[1].Excluded)
	assert.Equal(t, []string{"2026-10-26", "2026-10-25"}, m.Window.Dates)
	assert.Equal(t, "cccc", m.Assets["manifest.json"])

	// Encode then decode again: the result must be identical.
	var buf bytes.Buffer
	require.NoError(t, m.Encode(&buf))
	assert.True(t, strings.HasSuffix(buf.String(), "\n"), "encoded manifest must end with a newline")
	assert.Contains(t, buf.String(), "  ", "encoded manifest must use two-space indent")

	m2, err := store.DecodeManifest(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	assert.Equal(t, m, m2)
}

func TestManifest_UnknownSchema(t *testing.T) {
	const future = `{"schema_version": 2, "date": "2026-10-26"}`
	_, err := store.DecodeManifest(strings.NewReader(future))
	require.ErrorIs(t, err, store.ErrUnknownSchema)
}

func TestMergeInfoFrom_Defaults(t *testing.T) {
	mi := store.MergeInfoFrom(merge.DefaultOptions())
	assert.Equal(t, "weighted", mi.Strategy)
	assert.Equal(t, "24h", mi.HalfLife)
	assert.InDelta(t, 0.125, mi.MinWeight, 1e-9)
	assert.True(t, mi.Normalize, "normalize is always true")
}
