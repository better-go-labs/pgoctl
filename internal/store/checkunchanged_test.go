package store

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// listOnlyStore is a Store whose ListDays returns a fixed set of days; the other
// methods are unused by checkUnchanged.
type listOnlyStore struct{ days []Day }

func (s listOnlyStore) ListDays(context.Context, Key) ([]Day, error) { return s.days, nil }
func (listOnlyStore) ReadAsset(context.Context, Day, string) (io.ReadCloser, error) {
	return nil, ErrNotFound
}
func (listOnlyStore) BeginDay(context.Context, Key, Date, int) (Day, error) { return Day{}, nil }
func (listOnlyStore) PutAsset(context.Context, Day, string, io.Reader) error { return nil }
func (listOnlyStore) DeleteAsset(context.Context, Day, string) error        { return nil }
func (listOnlyStore) Publish(context.Context, Day) error                    { return nil }
func (listOnlyStore) DeleteDay(context.Context, Day) error                  { return nil }

func TestCheckUnchanged_DuplicateTagConflicts(t *testing.T) {
	key := Key{Repo: "o/r", Prefix: "profile"}
	d, err := ParseDate("2026-10-31")
	require.NoError(t, err)

	// Two first-run drafts share one tag: a competing writer created a parallel
	// day for this date. Neither is published, so the plain freshness check sees
	// no manifest and would allow the clobber; the guard must still reject it.
	draft := Day{Key: key, Date: d, Seq: 0, Draft: true, Assets: map[string]Asset{}}
	s := listOnlyStore{days: []Day{draft, draft}}

	err = checkUnchanged(context.Background(), s, key, d, nil)
	require.ErrorIs(t, err, ErrConcurrentUpdate)
}
