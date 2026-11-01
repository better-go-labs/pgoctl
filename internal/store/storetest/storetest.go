// Package storetest provides an in-memory Store implementation and a contract
// test suite that any Store backend must satisfy.
package storetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Option configures a Memory store.
type Option func(*memory)

// Immutable makes every published day immutable, so a later write to a published
// day returns ErrImmutable and a new write must use the next sequence. This
// mirrors the GitHub Releases and Actions Artifacts backends.
func Immutable() Option { return func(m *memory) { m.immutable = true } }

// HideRunAssets drops run profile files from the Day.Assets listing while
// ReadAsset still serves them, modelling the Actions Artifacts backend whose
// day listing does not enumerate run files. The flows must therefore copy runs
// forward from the manifest, not from the listed assets.
func HideRunAssets() Option { return func(m *memory) { m.hideRunAssets = true } }

// FailRead makes the next read of name return ErrNotFound exactly once. It
// models a transient asset read failure for the publish "asset vanishes" path.
func FailRead(name string) Option { return func(m *memory) { m.failReads[name]++ } }

// FailReadErr makes the next read of name return a non-ErrNotFound error exactly
// once, modelling a hard backend read failure that the flows must propagate
// rather than skip.
func FailReadErr(name string) Option { return func(m *memory) { m.failReadErrs[name]++ } }

// FailWrite makes the next PutAsset of name return an error exactly once, so the
// upload error path of a flow can be exercised.
func FailWrite(name string) Option { return func(m *memory) { m.failWrites[name]++ } }

// FailPublish makes the next Publish return an error exactly once, so a flow's
// publish-failure path can be exercised.
func FailPublish() Option { return func(m *memory) { m.failPublish++ } }

// FailBeginDay makes the next BeginDay return an error exactly once, so a flow's
// day-open failure path can be exercised.
func FailBeginDay() Option { return func(m *memory) { m.failBeginDay++ } }

// FailDeleteDay makes the next DeleteDay return an error exactly once, so a
// flow's cleanup-failure path can be exercised.
func FailDeleteDay() Option { return func(m *memory) { m.failDeleteDay++ } }

// FailListDaysAt makes the call-th ListDays return an error, numbered from 1, so
// a flow that lists more than once can be failed on a chosen call.
func FailListDaysAt(call int) Option { return func(m *memory) { m.failListAt = call } }

// FailListNext makes the next ListDays return an error exactly once, regardless
// of the call count so far. It pairs with Arm to inject a failure after setup.
func FailListNext() Option { return func(m *memory) { m.failListNext++ } }

// BeforeList registers fn to run at the start of each ListDays call, numbered
// from 1. It gives a test a seam to inject a competing publish between the
// flow's ListDays calls, exercising the concurrent-update path.
func BeforeList(fn func(call int)) Option { return func(m *memory) { m.beforeList = fn } }

// Arm applies additional fail-injection options to an existing store from
// NewMemory, so a test can inject a failure after a successful setup rather than
// at construction.
func Arm(s store.Store, opts ...Option) {
	m, ok := s.(*memory)
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range opts {
		o(m)
	}
}

// NewMemory returns an in-memory Store for tests.
func NewMemory(opts ...Option) store.Store {
	m := &memory{
		days:         make(map[string]*memDay),
		failReads:    make(map[string]int),
		failReadErrs: make(map[string]int),
		failWrites:   make(map[string]int),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// memAsset is one stored file with a backend handle and modification time.
type memAsset struct {
	data      []byte
	id        string
	updatedAt time.Time
}

// memDay is the mutable state of one (key, date, seq) day.
type memDay struct {
	key       store.Key
	date      store.Date
	seq       int
	draft     bool
	immutable bool
	id        string
	assets    map[string]memAsset
}

// memory implements store.Store entirely in process.
type memory struct {
	mu            sync.Mutex
	days          map[string]*memDay
	immutable     bool
	hideRunAssets bool
	failReads     map[string]int
	failReadErrs  map[string]int
	failWrites    map[string]int
	failPublish   int
	failBeginDay  int
	failDeleteDay int
	failListAt    int
	failListNext  int
	beforeList    func(call int)
	listCalls     int
	clock         int64
}

// errInjected is the generic error the fail-injection options return, distinct
// from the sentinel errors so flows treat it as a hard failure.
var errInjected = errors.New("injected failure")

func keyOf(k store.Key) string {
	return fmt.Sprintf("%s\x00%s\x00%s", k.Repo, k.Prefix, k.Service)
}

func identity(k store.Key, d store.Date, seq int) string {
	return fmt.Sprintf("%s\x00%s\x00%d", keyOf(k), d.String(), seq)
}

// toDay builds a store.Day snapshot; the caller must hold m.mu.
func (m *memory) toDay(d *memDay) store.Day {
	assets := make(map[string]store.Asset, len(d.assets))
	for name, a := range d.assets {
		if m.hideRunAssets {
			if _, ok := store.RunIDFromAsset(name); ok {
				continue
			}
		}
		assets[name] = store.Asset{ID: a.id, Name: name, Size: int64(len(a.data)), UpdatedAt: a.updatedAt}
	}
	return store.Day{
		Key:       d.key,
		Date:      d.date,
		Seq:       d.seq,
		Draft:     d.draft,
		Immutable: d.immutable,
		ID:        d.id,
		Assets:    assets,
	}
}

func (m *memory) ListDays(_ context.Context, key store.Key) ([]store.Day, error) {
	m.mu.Lock()
	m.listCalls++
	call, hook := m.listCalls, m.beforeList
	m.mu.Unlock()
	if hook != nil {
		// The hook may call back into the store, so run it without the lock held.
		hook(call)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failListAt == call {
		return nil, fmt.Errorf("listing days: %w", errInjected)
	}
	if m.failListNext > 0 {
		m.failListNext--
		return nil, fmt.Errorf("listing days: %w", errInjected)
	}
	var out []store.Day
	for _, d := range m.days {
		// Mirror a tag-based backend: a day belongs to key when the repos match
		// and key can parse the day's tag. This exercises Key.ParseTag, including
		// its prefix and service boundary, the way the Releases backend does.
		if d.key.Repo != key.Repo {
			continue
		}
		if _, _, ok := key.ParseTag(d.key.Tag(d.date, d.seq)); !ok {
			continue
		}
		out = append(out, m.toDay(d))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date.After(out[j].Date)
		}
		return out[i].Seq > out[j].Seq
	})
	return out, nil
}

func (m *memory) ReadAsset(_ context.Context, day store.Day, name string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := m.failReads[name]; n > 0 {
		m.failReads[name] = n - 1
		return nil, fmt.Errorf("transient read of %q: %w", name, store.ErrNotFound)
	}
	if n := m.failReadErrs[name]; n > 0 {
		m.failReadErrs[name] = n - 1
		return nil, fmt.Errorf("hard read of %q: %w", name, errInjected)
	}
	d, ok := m.days[identity(day.Key, day.Date, day.Seq)]
	if !ok {
		return nil, fmt.Errorf("day %s: %w", day.Date, store.ErrNotFound)
	}
	a, ok := d.assets[name]
	if !ok {
		return nil, fmt.Errorf("asset %q: %w", name, store.ErrNotFound)
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), a.data...))), nil
}

func (m *memory) BeginDay(_ context.Context, key store.Key, date store.Date, seq int) (store.Day, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failBeginDay > 0 {
		m.failBeginDay--
		return store.Day{}, fmt.Errorf("beginning day: %w", errInjected)
	}
	id := identity(key, date, seq)
	d, ok := m.days[id]
	if !ok {
		d = &memDay{key: key, date: date, seq: seq, draft: true, id: id, assets: make(map[string]memAsset)}
		m.days[id] = d
	}
	return m.toDay(d), nil
}

func (m *memory) PutAsset(_ context.Context, day store.Day, name string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading asset %q: %w", name, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := m.failWrites[name]; n > 0 {
		m.failWrites[name] = n - 1
		return fmt.Errorf("writing %q: %w", name, errInjected)
	}
	d, ok := m.days[identity(day.Key, day.Date, day.Seq)]
	if !ok {
		return fmt.Errorf("day %s: %w", day.Date, store.ErrNotFound)
	}
	if d.immutable {
		return fmt.Errorf("writing %q to immutable day %s: %w", name, day.Date, store.ErrImmutable)
	}
	m.clock++
	d.assets[name] = memAsset{
		data:      data,
		id:        fmt.Sprintf("a%d", m.clock),
		updatedAt: time.Unix(0, m.clock).UTC(),
	}
	return nil
}

func (m *memory) DeleteAsset(_ context.Context, day store.Day, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.days[identity(day.Key, day.Date, day.Seq)]
	if !ok {
		return fmt.Errorf("day %s: %w", day.Date, store.ErrNotFound)
	}
	if d.immutable {
		return fmt.Errorf("deleting %q from immutable day %s: %w", name, day.Date, store.ErrImmutable)
	}
	delete(d.assets, name)
	return nil
}

func (m *memory) Publish(_ context.Context, day store.Day) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPublish > 0 {
		m.failPublish--
		return fmt.Errorf("publishing day %s: %w", day.Date, errInjected)
	}
	d, ok := m.days[identity(day.Key, day.Date, day.Seq)]
	if !ok {
		return fmt.Errorf("day %s: %w", day.Date, store.ErrNotFound)
	}
	d.draft = false
	if m.immutable {
		d.immutable = true
	}
	return nil
}

func (m *memory) DeleteDay(_ context.Context, day store.Day) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDeleteDay > 0 {
		m.failDeleteDay--
		return fmt.Errorf("deleting day %s: %w", day.Date, errInjected)
	}
	delete(m.days, identity(day.Key, day.Date, day.Seq))
	return nil
}

// RunContract runs the shared Store contract against stores produced by
// newStore. Each subtest gets a fresh store so cases never interfere.
func RunContract(t *testing.T, newStore func() store.Store) {
	t.Helper()
	ctx := context.Background()
	key := store.Key{Repo: "o/r", Prefix: "profile"}
	mustDate := func(s string) store.Date {
		d, err := store.ParseDate(s)
		require.NoError(t, err)
		return d
	}

	t.Run("BeginDay_CreatesDraftOnce", func(t *testing.T) {
		s := newStore()
		date := mustDate("2026-01-01")
		d1, err := s.BeginDay(ctx, key, date, 0)
		require.NoError(t, err)
		assert.True(t, d1.Draft)
		require.NoError(t, s.PutAsset(ctx, d1, "x.pprof", bytes.NewReader([]byte("payload"))))

		// A second BeginDay for the same key/date/seq returns the same day.
		d2, err := s.BeginDay(ctx, key, date, 0)
		require.NoError(t, err)
		assert.True(t, d2.Has("x.pprof"), "second BeginDay must see the first handle's asset")

		rc, err := s.ReadAsset(ctx, d2, "x.pprof")
		require.NoError(t, err)
		defer func() { _ = rc.Close() }()
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, []byte("payload"), got)
	})

	t.Run("BeginDay_SequencesAreSeparate", func(t *testing.T) {
		s := newStore()
		date := mustDate("2026-02-01")
		d0, err := s.BeginDay(ctx, key, date, 0)
		require.NoError(t, err)
		require.NoError(t, s.PutAsset(ctx, d0, store.AssetDaily, bytes.NewReader([]byte("zero"))))

		d1, err := s.BeginDay(ctx, key, date, 1)
		require.NoError(t, err)
		assert.Equal(t, 1, d1.Seq)
		assert.False(t, d1.Has(store.AssetDaily), "sequence 1 must not share sequence 0 assets")

		_, err = s.ReadAsset(ctx, d1, store.AssetDaily)
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Publish_MakesDayVisible", func(t *testing.T) {
		s := newStore()
		date := mustDate("2026-03-01")
		d, err := s.BeginDay(ctx, key, date, 0)
		require.NoError(t, err)
		require.NoError(t, s.PutAsset(ctx, d, store.AssetManifest, bytes.NewReader([]byte("{}"))))

		days, err := s.ListDays(ctx, key)
		require.NoError(t, err)
		require.Len(t, days, 1)
		assert.True(t, days[0].Draft, "drafts are listed before publish")

		require.NoError(t, s.Publish(ctx, d))
		days, err = s.ListDays(ctx, key)
		require.NoError(t, err)
		require.Len(t, days, 1)
		assert.False(t, days[0].Draft, "published day must not be a draft")
	})

	t.Run("ListDays_NewestFirst", func(t *testing.T) {
		s := newStore()
		for _, ds := range []string{"2026-05-01", "2026-05-03", "2026-05-02"} {
			d, err := s.BeginDay(ctx, key, mustDate(ds), 0)
			require.NoError(t, err)
			require.NoError(t, s.Publish(ctx, d))
		}
		days, err := s.ListDays(ctx, key)
		require.NoError(t, err)
		require.Len(t, days, 3)
		assert.Equal(t, "2026-05-03", days[0].Date.String())
		assert.Equal(t, "2026-05-02", days[1].Date.String())
		assert.Equal(t, "2026-05-01", days[2].Date.String())
	})

	t.Run("DeleteDay_RemovesDay", func(t *testing.T) {
		s := newStore()
		date := mustDate("2026-04-01")
		d, err := s.BeginDay(ctx, key, date, 0)
		require.NoError(t, err)
		require.NoError(t, s.Publish(ctx, d))
		require.NoError(t, s.DeleteDay(ctx, d))
		days, err := s.ListDays(ctx, key)
		require.NoError(t, err)
		assert.Empty(t, days)
	})

	t.Run("ReadAsset_NotFound", func(t *testing.T) {
		s := newStore()
		d, err := s.BeginDay(ctx, key, mustDate("2026-06-01"), 0)
		require.NoError(t, err)
		_, err = s.ReadAsset(ctx, d, "missing")
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Immutable_RejectsWrite", func(t *testing.T) {
		s := newStore()
		date := mustDate("2026-07-01")
		d, err := s.BeginDay(ctx, key, date, 0)
		require.NoError(t, err)
		require.NoError(t, s.PutAsset(ctx, d, store.AssetManifest, bytes.NewReader([]byte("{}"))))
		require.NoError(t, s.Publish(ctx, d))

		days, err := s.ListDays(ctx, key)
		require.NoError(t, err)
		require.Len(t, days, 1)
		if !days[0].Immutable {
			t.Skip("store does not lock published days")
		}
		err = s.PutAsset(ctx, days[0], "x", bytes.NewReader([]byte("y")))
		require.ErrorIs(t, err, store.ErrImmutable)
	})

	t.Run("PutThenReadSameDayValue", func(t *testing.T) {
		s := newStore()
		date := mustDate("2026-08-01")
		day, err := s.BeginDay(ctx, key, date, 0)
		require.NoError(t, err)
		payload := []byte("test-asset-payload")
		require.NoError(t, s.PutAsset(ctx, day, "runs/1-1.pprof", bytes.NewReader(payload)))

		rc, err := s.ReadAsset(ctx, day, "runs/1-1.pprof")
		require.NoError(t, err)
		defer func() { _ = rc.Close() }()
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, payload, got)
	})
}

// NewProfile builds a minimal synthetic CPU pprof whose cpu/nanoseconds total is
// cpuSamples, so tests can verify normalization without capturing a real
// profile. The profile carries no TimeNanos, so merge weighting uses the
// caller-provided capture time.
func NewProfile(t *testing.T, cpuSamples int64) []byte {
	t.Helper()
	return NewProfileAt(t, cpuSamples, time.Time{})
}

// NewProfileAt is like NewProfile but stamps the profile's TimeNanos from at
// when at is non-zero, so tests can exercise CapturedAt derivation.
func NewProfileAt(t *testing.T, cpuSamples int64, at time.Time) []byte {
	t.Helper()
	fn := &profile.Function{ID: 1, Name: "test.work", Filename: "work.go"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn, Line: 1}}}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		Sample: []*profile.Sample{
			{Value: []int64{1, cpuSamples}, Location: []*profile.Location{loc}},
		},
		Function: []*profile.Function{fn},
		Location: []*profile.Location{loc},
	}
	if !at.IsZero() {
		p.TimeNanos = at.UnixNano()
	}
	var buf bytes.Buffer
	require.NoError(t, p.Write(&buf))
	return buf.Bytes()
}
