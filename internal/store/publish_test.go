package store_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	internerrors "github.com/Better-Go-Labs/pgoctl/internal/errors"
	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testKey = store.Key{Repo: "o/r", Prefix: "profile"}

// publishRun publishes one run for date on s with the given run id, quality and
// CPU sample total, and returns the result.
func publishRun(t *testing.T, s store.Store, date, runID string, quality float64, cpu int64) *store.PublishResult {
	t.Helper()
	d := mustDate(t, date)
	res, err := store.Publish(context.Background(), s, store.PublishRequest{
		Key:          testKey,
		Today:        d,
		Branch:       "main",
		Run:          store.RunInfo{ID: runID, QualityScore: quality, CapturedAt: d.Time()},
		Profile:      storetest.NewProfile(t, cpu),
		LookbackDays: 14,
		MaxProfiles:  7,
		GoVersion:    "go1.25",
	})
	require.NoError(t, err)
	return res
}

func TestPublish_FirstRun(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()

	res := publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)

	require.NotNil(t, res.Manifest)
	assert.Equal(t, "100-1", res.Manifest.DailyRun)
	assert.Equal(t, []string{"2026-10-31"}, res.Manifest.Window.Dates)
	assert.NotEmpty(t, res.Default)

	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.False(t, days[0].Draft)
	for _, name := range []string{store.AssetDaily, store.AssetDefault, store.AssetManifest, store.RunAsset("100-1")} {
		assert.True(t, days[0].Has(name), "published day must carry %s", name)
	}
}

func TestPublish_RejectsBadProfiles(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		profile []byte
		wantErr error
	}{
		{name: "unparseable bytes wrap ErrParseProfile", profile: []byte("not a profile"), wantErr: internerrors.ErrParseProfile},
		{name: "zero CPU samples are rejected", profile: storetest.NewProfile(t, 0), wantErr: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Publish(ctx, storetest.NewMemory(), store.PublishRequest{
				Key:          testKey,
				Today:        mustDate(t, "2026-10-31"),
				Run:          store.RunInfo{ID: "100-1"},
				Profile:      tt.profile,
				LookbackDays: 14,
				MaxProfiles:  7,
			})
			require.Error(t, err)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestPublish_SelectsHighestQualityRun(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.5, 1000)
	res := publishRun(t, s, "2026-10-31", "100-2", 0.9, 1000)

	assert.Equal(t, "100-2", res.Manifest.DailyRun, "the higher quality run wins")
	require.Len(t, res.Manifest.Runs, 2)
}

func TestPublish_WindowSpansPriorDays(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-29", "100-1", 0.9, 1000)
	publishRun(t, s, "2026-10-30", "101-1", 0.9, 1000)
	res := publishRun(t, s, "2026-10-31", "102-1", 0.9, 1000)

	assert.Equal(t, []string{"2026-10-31", "2026-10-30", "2026-10-29"}, res.Manifest.Window.Dates)
}

func TestPublish_ConcurrentUpdateDetected(t *testing.T) {
	ctx := context.Background()
	var s store.Store
	// On the second ListDays of the publish (checkUnchanged), a competitor
	// publishes a day for today, so the manifest the flow guards against no
	// longer matches what it saw at the start.
	s = storetest.NewMemory(storetest.BeforeList(func(call int) {
		if call != 2 {
			return
		}
		d := mustDate(t, "2026-10-31")
		day, err := s.BeginDay(ctx, testKey, d, 9)
		require.NoError(t, err)
		require.NoError(t, s.PutAsset(ctx, day, store.AssetManifest, bytes.NewReader([]byte("{}"))))
		require.NoError(t, s.Publish(ctx, day))
	}))

	_, err := store.Publish(ctx, s, store.PublishRequest{
		Key:          testKey,
		Today:        mustDate(t, "2026-10-31"),
		Run:          store.RunInfo{ID: "100-1", QualityScore: 0.9, CapturedAt: time.Now()},
		Profile:      storetest.NewProfile(t, 1000),
		LookbackDays: 14,
		MaxProfiles:  7,
	})
	require.ErrorIs(t, err, store.ErrConcurrentUpdate)
}

func TestPublish_ImmutableOpensNextSequence(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory(storetest.Immutable())
	publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
	res := publishRun(t, s, "2026-10-31", "100-2", 0.9, 1000)

	assert.Equal(t, 1, res.Manifest.Sequence, "a second run on an immutable day opens sequence 1")
	require.NotEmpty(t, res.Manifest.Supersedes)
	assert.Equal(t, "profile/2026-10-31.0", res.Manifest.Supersedes[0])

	// Prune deletes the superseded sequence 0, leaving only the newest sequence.
	days, err := s.ListDays(ctx, testKey)
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.Equal(t, 1, days[0].Seq)
}
