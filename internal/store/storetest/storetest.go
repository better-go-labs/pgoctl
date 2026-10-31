// Package storetest provides an in-memory Store implementation and a contract
// test suite that any Store backend must satisfy.
package storetest

import (
	"bytes"
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NewMemoryStore returns an empty in-memory Store for use in tests.
func NewMemoryStore() store.Store {
	return &memoryStore{days: make(map[string]map[string]*dayState)}
}

// dayKey builds a composite key from a store key and date.
func dayKey(key, date string) string { return key + "\x00" + date }

// dayState holds a committed snapshot plus any open draft.
type dayState struct {
	mu        sync.Mutex
	committed *snapshot
	draft     *snapshot
}

// snapshot is the data for one committed or draft day.
type snapshot struct {
	manifest *store.Manifest
	assets   map[string][]byte
}

func newSnapshot() *snapshot {
	return &snapshot{assets: make(map[string][]byte)}
}

// memoryStore implements store.Store entirely in memory.
type memoryStore struct {
	mu   sync.RWMutex
	days map[string]map[string]*dayState // key -> date -> state
}

func (s *memoryStore) getState(key, date string) *dayState {
	s.mu.RLock()
	ks := s.days[key]
	if ks != nil {
		st := ks[date]
		s.mu.RUnlock()
		return st
	}
	s.mu.RUnlock()
	return nil
}

func (s *memoryStore) getOrCreateState(key, date string) *dayState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.days[key] == nil {
		s.days[key] = make(map[string]*dayState)
	}
	if s.days[key][date] == nil {
		s.days[key][date] = &dayState{}
	}
	return s.days[key][date]
}

// ListDays returns all committed days for key, newest first (by date string).
func (s *memoryStore) ListDays(_ context.Context, key string) ([]store.Day, error) {
	s.mu.RLock()
	ks := s.days[key]
	s.mu.RUnlock()

	var days []store.Day
	for date, st := range ks {
		st.mu.Lock()
		if st.committed != nil {
			days = append(days, store.Day{Date: date, Seq: 0})
		}
		st.mu.Unlock()
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date > days[j].Date })
	return days, nil
}

// GetManifest returns the manifest for the committed day.
func (s *memoryStore) GetManifest(_ context.Context, key string, d store.Day) (*store.Manifest, error) {
	st := s.getState(key, d.Date)
	if st == nil {
		return nil, store.ErrNotFound
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.committed == nil || st.committed.manifest == nil {
		return nil, store.ErrNotFound
	}
	cp := *st.committed.manifest
	return &cp, nil
}

// GetAsset returns the named asset from the committed day.
func (s *memoryStore) GetAsset(_ context.Context, key string, d store.Day, name string) ([]byte, error) {
	st := s.getState(key, d.Date)
	if st == nil {
		return nil, store.ErrNotFound
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.committed == nil {
		return nil, store.ErrNotFound
	}
	data, ok := st.committed.assets[name]
	if !ok {
		return nil, store.ErrNotFound
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}

// BeginDay opens or returns the existing draft for key+d. Calling BeginDay
// twice for the same key and date returns the same draft, satisfying the
// BeginDay_CreatesDraftOnce contract.
func (s *memoryStore) BeginDay(_ context.Context, key string, d store.Day) (store.Draft, error) {
	st := s.getOrCreateState(key, d.Date)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.draft == nil {
		st.draft = newSnapshot()
	}
	return &memDraft{state: st}, nil
}

// DeleteDay removes all sequences of d.Date for key.
func (s *memoryStore) DeleteDay(_ context.Context, key string, d store.Day) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.days[key] != nil {
		delete(s.days[key], d.Date)
	}
	return nil
}

// memDraft is a Draft backed by a dayState.
type memDraft struct {
	state *dayState
}

func (dr *memDraft) PutAsset(_ context.Context, name string, data []byte) error {
	dr.state.mu.Lock()
	defer dr.state.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	dr.state.draft.assets[name] = cp
	return nil
}

func (dr *memDraft) PutManifest(_ context.Context, m *store.Manifest) error {
	dr.state.mu.Lock()
	defer dr.state.mu.Unlock()
	cp := *m
	dr.state.draft.manifest = &cp
	return nil
}

func (dr *memDraft) Commit(_ context.Context) error {
	dr.state.mu.Lock()
	defer dr.state.mu.Unlock()
	dr.state.committed = dr.state.draft
	dr.state.draft = nil
	return nil
}

func (dr *memDraft) Discard(_ context.Context) error {
	dr.state.mu.Lock()
	defer dr.state.mu.Unlock()
	dr.state.draft = nil
	return nil
}

// RunContracts runs the Store contract test suite against s. Any Store
// backend must pass all of these cases.
func RunContracts(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("BeginDay_CreatesDraftOnce", func(t *testing.T) {
		// Two BeginDay calls for the same key and date must return a Draft
		// backed by the same state: an asset put via the first handle must be
		// visible after the second handle commits.
		d1, err := s.BeginDay(ctx, "k-draft-once", store.Day{Date: "2026-01-01"})
		require.NoError(t, err)
		require.NotNil(t, d1)

		d2, err := s.BeginDay(ctx, "k-draft-once", store.Day{Date: "2026-01-01"})
		require.NoError(t, err)
		require.NotNil(t, d2)

		// Put an asset via d1, then commit via d2.
		require.NoError(t, d1.PutAsset(ctx, "x.pprof", []byte("payload")))
		m := &store.Manifest{SchemaVersion: 1, Date: "2026-01-01"}
		require.NoError(t, d2.PutManifest(ctx, m))
		require.NoError(t, d2.Commit(ctx))

		// The committed day must now be visible.
		days, err := s.ListDays(ctx, "k-draft-once")
		require.NoError(t, err)
		require.Len(t, days, 1)
		assert.Equal(t, "2026-01-01", days[0].Date)

		// The asset put via d1 must be in the committed snapshot.
		data, err := s.GetAsset(ctx, "k-draft-once", days[0], "x.pprof")
		require.NoError(t, err)
		assert.Equal(t, []byte("payload"), data)
	})

	t.Run("Commit_MakesDayVisible", func(t *testing.T) {
		draft, err := s.BeginDay(ctx, "k-commit", store.Day{Date: "2026-02-01"})
		require.NoError(t, err)

		// Not visible before Commit.
		days, err := s.ListDays(ctx, "k-commit")
		require.NoError(t, err)
		assert.Empty(t, days, "day must not be visible before Commit")

		require.NoError(t, draft.PutManifest(ctx, &store.Manifest{SchemaVersion: 1, Date: "2026-02-01"}))
		require.NoError(t, draft.Commit(ctx))

		days, err = s.ListDays(ctx, "k-commit")
		require.NoError(t, err)
		require.Len(t, days, 1)
		assert.Equal(t, "2026-02-01", days[0].Date)
	})

	t.Run("GetManifest_ErrNotFound_BeforeCommit", func(t *testing.T) {
		_, err := s.GetManifest(ctx, "k-notfound", store.Day{Date: "2026-03-01"})
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("GetAsset_ErrNotFound_BeforeCommit", func(t *testing.T) {
		_, err := s.GetAsset(ctx, "k-notfound", store.Day{Date: "2026-03-01"}, "x.pprof")
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("DeleteDay_RemovesDay", func(t *testing.T) {
		draft, err := s.BeginDay(ctx, "k-delete", store.Day{Date: "2026-04-01"})
		require.NoError(t, err)
		require.NoError(t, draft.PutManifest(ctx, &store.Manifest{SchemaVersion: 1, Date: "2026-04-01"}))
		require.NoError(t, draft.Commit(ctx))

		require.NoError(t, s.DeleteDay(ctx, "k-delete", store.Day{Date: "2026-04-01"}))

		days, err := s.ListDays(ctx, "k-delete")
		require.NoError(t, err)
		assert.Empty(t, days)
	})

	t.Run("ListDays_NewestFirst", func(t *testing.T) {
		for _, date := range []string{"2026-05-01", "2026-05-03", "2026-05-02"} {
			draft, err := s.BeginDay(ctx, "k-order", store.Day{Date: date})
			require.NoError(t, err)
			require.NoError(t, draft.PutManifest(ctx, &store.Manifest{SchemaVersion: 1, Date: date}))
			require.NoError(t, draft.Commit(ctx))
		}
		days, err := s.ListDays(ctx, "k-order")
		require.NoError(t, err)
		require.Len(t, days, 3)
		assert.Equal(t, "2026-05-03", days[0].Date)
		assert.Equal(t, "2026-05-02", days[1].Date)
		assert.Equal(t, "2026-05-01", days[2].Date)
	})
}

// NewProfile builds a minimal synthetic CPU pprof for use in store tests.
// cpuSamples controls the total number of cpu/nanoseconds sample values so
// tests can verify normalization without capturing a real profile.
func NewProfile(t *testing.T, cpuSamples int64) []byte {
	t.Helper()
	fn := &profile.Function{ID: 1, Name: "test.work", Filename: "work.go"}
	loc := &profile.Location{
		ID: 1,
		Line: []profile.Line{
			{Function: fn, Line: 1},
		},
	}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		Sample: []*profile.Sample{
			{
				Value:    []int64{1, cpuSamples},
				Location: []*profile.Location{loc},
			},
		},
		Function: []*profile.Function{fn},
		Location: []*profile.Location{loc},
	}
	var buf bytes.Buffer
	require.NoError(t, p.Write(&buf))
	return buf.Bytes()
}
