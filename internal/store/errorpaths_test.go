package store_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errWriter fails every Write, to exercise writer error paths.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// baseRequest returns a valid publish request for 2026-10-31 that each error
// case mutates.
func baseRequest(t *testing.T) store.PublishRequest {
	t.Helper()
	d := mustDate(t, "2026-10-31")
	return store.PublishRequest{
		Key:          testKey,
		Today:        d,
		Branch:       "main",
		Run:          store.RunInfo{ID: "100-1", QualityScore: 0.9, CapturedAt: d.Time()},
		Profile:      storetest.NewProfile(t, 1000),
		LookbackDays: 14,
		MaxProfiles:  7,
	}
}

func TestPublish_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid key", func(t *testing.T) {
		req := baseRequest(t)
		req.Key = store.Key{Repo: "no-slash", Prefix: "profile"}
		_, err := store.Publish(ctx, storetest.NewMemory(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid key")
	})

	t.Run("invalid run id", func(t *testing.T) {
		req := baseRequest(t)
		req.Run.ID = "not-a-run-id!"
		_, err := store.Publish(ctx, storetest.NewMemory(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid run id")
	})

	t.Run("list days fails", func(t *testing.T) {
		_, err := store.Publish(ctx, storetest.NewMemory(storetest.FailListDaysAt(1)), baseRequest(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "listing days")
	})

	t.Run("begin day fails", func(t *testing.T) {
		_, err := store.Publish(ctx, storetest.NewMemory(storetest.FailBeginDay()), baseRequest(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "beginning")
	})

	t.Run("upload run fails", func(t *testing.T) {
		_, err := store.Publish(ctx, storetest.NewMemory(storetest.FailWrite(store.RunAsset("100-1"))), baseRequest(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uploading run")
	})

	t.Run("select daily read fails", func(t *testing.T) {
		_, err := store.Publish(ctx, storetest.NewMemory(storetest.FailReadErr(store.RunAsset("100-1"))), baseRequest(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading run")
	})

	t.Run("write daily fails", func(t *testing.T) {
		_, err := store.Publish(ctx, storetest.NewMemory(storetest.FailWrite(store.AssetDaily)), baseRequest(t))
		require.Error(t, err)
	})

	t.Run("write manifest fails", func(t *testing.T) {
		_, err := store.Publish(ctx, storetest.NewMemory(storetest.FailWrite(store.AssetManifest)), baseRequest(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "manifest")
	})

	t.Run("publish draft fails", func(t *testing.T) {
		_, err := store.Publish(ctx, storetest.NewMemory(storetest.FailPublish()), baseRequest(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "publishing")
	})

	t.Run("concurrent change in checkUnchanged listing", func(t *testing.T) {
		// The second ListDays of the flow (checkUnchanged) fails.
		_, err := store.Publish(ctx, storetest.NewMemory(storetest.FailListDaysAt(2)), baseRequest(t))
		require.Error(t, err)
	})

	t.Run("window profile unparseable", func(t *testing.T) {
		s := storetest.NewMemory()
		// A prior published day carries an unparseable daily.pprof; building the
		// window must surface the parse error.
		putPublishedDay(t, s, testKey, "2026-10-30", 0, []byte("not a profile"), []byte("default-30"))
		_, err := store.Publish(ctx, s, baseRequest(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "window profile")
	})
}

func TestExclude_InvalidKey(t *testing.T) {
	_, err := store.Exclude(context.Background(), storetest.NewMemory(), store.ExcludeRequest{
		Key:   store.Key{Repo: "no-slash", Prefix: "profile"},
		Date:  mustDate(t, "2026-10-31"),
		RunID: "100-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid key")
}

func TestExclude_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	req := func() store.ExcludeRequest {
		return store.ExcludeRequest{
			Key:   testKey,
			Date:  mustDate(t, "2026-10-31"),
			RunID: "100-1",
			Now:   mustDate(t, "2026-10-31").Time(),
		}
	}

	t.Run("list days fails", func(t *testing.T) {
		s := storetest.NewMemory()
		publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
		storetest.Arm(s, storetest.FailListNext())
		_, err := store.Exclude(ctx, s, req())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "listing days")
	})

	t.Run("read target manifest fails", func(t *testing.T) {
		s := storetest.NewMemory()
		publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
		storetest.Arm(s, storetest.FailReadErr(store.AssetManifest))
		_, err := store.Exclude(ctx, s, req())
		require.Error(t, err)
	})

	t.Run("reselect daily read fails", func(t *testing.T) {
		s := storetest.NewMemory()
		publishRun(t, s, "2026-10-31", "100-1", 0.5, 1000)
		publishRun(t, s, "2026-10-31", "100-2", 0.9, 1000)
		storetest.Arm(s, storetest.FailReadErr(store.RunAsset("100-1")))
		_, err := store.Exclude(ctx, s, req())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading run")
	})

	t.Run("write snapshot fails", func(t *testing.T) {
		s := storetest.NewMemory()
		publishRun(t, s, "2026-10-31", "100-1", 0.5, 1000)
		publishRun(t, s, "2026-10-31", "100-2", 0.9, 1000)
		storetest.Arm(s, storetest.FailWrite(store.AssetManifest))
		_, err := store.Exclude(ctx, s, req())
		require.Error(t, err)
	})

	t.Run("immutable opens next sequence then begin fails", func(t *testing.T) {
		s := storetest.NewMemory(storetest.Immutable())
		publishRun(t, s, "2026-10-31", "100-1", 0.5, 1000)
		publishRun(t, s, "2026-10-31", "100-2", 0.9, 1000)
		storetest.Arm(s, storetest.FailBeginDay())
		_, err := store.Exclude(ctx, s, req())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "beginning")
	})

	t.Run("rebuild window profile unparseable", func(t *testing.T) {
		s := storetest.NewMemory()
		publishRun(t, s, "2026-10-30", "100-1", 0.9, 1000)
		// Two runs on the excluded day, so a run remains after the exclude and the
		// rebuild still reads the window rather than short-circuiting on an empty day.
		publishRun(t, s, "2026-10-31", "200-1", 0.5, 1000)
		publishRun(t, s, "2026-10-31", "200-2", 0.9, 1000)
		// Corrupt the prior day's daily so rebuilding 2026-10-31 fails to parse it.
		prior := highestPublishedDay(t, s, "2026-10-30")
		require.NoError(t, s.PutAsset(ctx, prior, store.AssetDaily, bytes.NewReader([]byte("not a profile"))))
		_, err := store.Exclude(ctx, s, store.ExcludeRequest{
			Key: testKey, Date: mustDate(t, "2026-10-31"), RunID: "200-1", Now: mustDate(t, "2026-10-31").Time(),
		})
		require.Error(t, err)
	})
}

func TestRestore_WriterError(t *testing.T) {
	s := storetest.NewMemory()
	publishRun(t, s, "2026-10-31", "100-1", 0.9, 1000)
	_, err := store.Restore(context.Background(), s, testKey, errWriter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing default")
}

func TestRestore_ListDaysError(t *testing.T) {
	_, err := store.Restore(context.Background(), storetest.NewMemory(storetest.FailListDaysAt(1)), testKey, &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing days")
}

func TestPrune_DeleteDayError(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	putPublishedDay(t, s, testKey, "2026-10-31", 0, storetest.NewProfile(t, 1000), []byte("d0"))
	putPublishedDay(t, s, testKey, "2026-10-31", 1, storetest.NewProfile(t, 1000), []byte("d1"))
	// The superseded sequence 0 is deleted; make that delete fail.
	storetest.Arm(s, storetest.FailDeleteDay())
	_, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 14, 7)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting")
}

func TestRestore_ReadManifestHardError(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	putPublishedDay(t, s, testKey, "2026-10-31", 0, storetest.NewProfile(t, 1000), []byte("default-31"))
	storetest.Arm(s, storetest.FailReadErr(store.AssetManifest))
	_, err := store.Restore(ctx, s, testKey, &bytes.Buffer{})
	require.Error(t, err)
	assert.NotErrorIs(t, err, store.ErrNoSnapshot, "a hard manifest read error is not a missing snapshot")
}

func TestRestore_ReadDefaultHardError(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	putPublishedDay(t, s, testKey, "2026-10-31", 0, storetest.NewProfile(t, 1000), []byte("default-31"))
	storetest.Arm(s, storetest.FailReadErr(store.AssetDefault))
	_, err := store.Restore(ctx, s, testKey, &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading default")
}

func TestPrune_RestoreError(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewMemory()
	putPublishedDay(t, s, testKey, "2026-10-31", 0, storetest.NewProfile(t, 1000), []byte("default-31"))
	// Prune calls Restore first to find the protected date; a hard listing error
	// there must fail the prune.
	storetest.Arm(s, storetest.FailListNext())
	_, err := store.Prune(ctx, s, testKey, mustDate(t, "2026-10-31"), 14, 7)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restore for prune")
}

func TestManifest_DecodeAndEncodeErrors(t *testing.T) {
	_, err := store.DecodeManifest(strings.NewReader("{ not valid json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding manifest")

	m := &store.Manifest{SchemaVersion: store.SchemaVersion, Assets: map[string]string{}}
	err = m.Encode(errWriter{})
	require.Error(t, err)
}

func TestKey_ParseTagAndRunAssetEdges(t *testing.T) {
	key := store.Key{Repo: "o/r", Prefix: "profile"}

	_, _, ok := key.ParseTag("profile/2026-10-31")
	assert.False(t, ok, "a tag with no sequence separator is rejected")

	_, _, ok = key.ParseTag("profile/not-a-date.0")
	assert.False(t, ok, "a non-date segment is rejected")

	_, _, ok = key.ParseTag("profile/2026-10-31.notnum")
	assert.False(t, ok, "a non-numeric sequence is rejected")

	_, _, ok = key.ParseTag("profile/2026-10-31.-1")
	assert.False(t, ok, "a negative sequence is rejected")

	_, ok = store.RunIDFromAsset("runs/bad.pprof")
	assert.False(t, ok, "an asset whose id is not a valid run id is rejected")
}
