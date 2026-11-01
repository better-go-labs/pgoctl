package store_test

import (
	"testing"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDate(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "round trips a valid date", in: "2026-10-31", want: "2026-10-31"},
		{name: "rejects an invalid month", in: "2026-13-01", wantErr: true},
		{name: "rejects a non-date string", in: "not-a-date", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := store.ParseDate(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, d.String())
		})
	}
}

func TestDateOf_TruncatesToUTCMidnight(t *testing.T) {
	// A late-evening time in a positive offset zone still names its UTC day.
	loc := time.FixedZone("UTC+5", 5*3600)
	in := time.Date(2026, 10, 31, 2, 30, 0, 0, loc) // 2026-10-30 21:30 UTC
	got := store.DateOf(in)
	assert.Equal(t, "2026-10-30", got.String())
}

func TestDate_AddDays(t *testing.T) {
	d, err := store.ParseDate("2026-10-31")
	require.NoError(t, err)
	assert.Equal(t, "2026-10-17", d.AddDays(-14).String())
	assert.Equal(t, "2026-11-02", d.AddDays(2).String())
}

func TestDate_BeforeAfter(t *testing.T) {
	a, _ := store.ParseDate("2026-10-30")
	b, _ := store.ParseDate("2026-10-31")
	assert.True(t, a.Before(b))
	assert.True(t, b.After(a))
	assert.False(t, a.After(b))
}

// TestDate_EqualityIsUTCMidnight guards the "always UTC midnight" invariant:
// two DateOf calls on the same UTC day from different locations must be ==.
func TestDate_EqualityIsUTCMidnight(t *testing.T) {
	instant := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	west := instant.In(time.FixedZone("UTC-8", -8*3600))
	east := instant.In(time.FixedZone("UTC+9", 9*3600))
	assert.True(t, store.DateOf(west) == store.DateOf(east),
		"same UTC day from different zones must compare equal")
}
