package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrune_KeepsAllWhenUnderMax(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-30", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-31", "101-1", 0.9, 1000)

	deleted, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 14, 7)
	require.NoError(t, err)
	assert.Empty(t, deleted)
	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	assert.Len(t, days, 2)
}

func TestPrune_DeletesBeyondMaxButKeepsRestoreTarget(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	for i, d := range []string{"2026-10-27", "2026-10-28", "2026-10-29", "2026-10-30", "2026-10-31"} {
		publishRun(t, s, d, run(i), 0.9, 1000)
	}

	deleted, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 14, 2)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"2026-10-27", "2026-10-28", "2026-10-29"}, strs(deleted))

	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	require.Len(t, days, 2)
	assert.Equal(t, "2026-10-31", days[0].Date.String(), "the restore target is never deleted")
	assert.Equal(t, "2026-10-30", days[1].Date.String())
}

// run returns a distinct valid run id per index.
func run(i int) string { return fmt.Sprintf("10%d-1", i) }

// strs renders dates as "2006-01-02" strings, nil for an empty slice.
func strs(ds []store.Date) []string {
	if len(ds) == 0 {
		return nil
	}
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.String()
	}
	return out
}
