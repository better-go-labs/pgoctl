package store

import (
	"context"
	"errors"
	"io"
	"time"
)

// Sentinel errors returned by Store implementations and the flow functions.
var (
	// ErrNotFound is returned when a day or asset does not exist.
	ErrNotFound = errors.New("not found")
	// ErrImmutable is returned when a write targets a published, locked release.
	ErrImmutable = errors.New("release is immutable")
	// ErrPermission is returned when the token cannot perform the operation.
	ErrPermission = errors.New("permission denied")
	// ErrConcurrentUpdate is returned when a day changed under an in-flight publish.
	ErrConcurrentUpdate = errors.New("concurrent update")
	// ErrUnknownSchema is returned when a manifest schema version is not supported.
	ErrUnknownSchema = errors.New("unknown manifest schema")
	// ErrNoSnapshot is returned when no usable snapshot exists to restore.
	ErrNoSnapshot = errors.New("no usable snapshot")
	// ErrInvalidInput is returned when a caller passes a window that cannot
	// select anything, so the flow rejects it instead of defaulting or panicking.
	ErrInvalidInput = errors.New("invalid input")
)

// Asset describes one stored file in a day.
type Asset struct {
	ID        string // backend handle: release asset ID, or a counter in Memory
	Name      string
	Size      int64
	UpdatedAt time.Time
}

// Day is a single dated snapshot for a key at a sequence.
type Day struct {
	Key       Key
	Date      Date
	Seq       int
	Draft     bool
	Immutable bool             // the backend has locked this published release
	ID        string           // release ID or Memory key
	Assets    map[string]Asset // keyed by logical name, e.g. "runs/1-1.pprof"
}

// Has lets a flow test for an asset before it reads, copies, or rebuilds one.
func (d Day) Has(name string) bool {
	_, ok := d.Assets[name]
	return ok
}

// Store abstracts where daily profile snapshots live so Releases, Artifacts, and
// later S3 share the window, exclude, and prune logic.
//
// ListDays returns only the days of key, newest date first, drafts included.
// BeginDay returns the day with that key/date/seq, creating it as a draft if
// needed. A write to a published immutable day returns ErrImmutable.
type Store interface {
	ListDays(ctx context.Context, key Key) ([]Day, error)
	ReadAsset(ctx context.Context, day Day, name string) (io.ReadCloser, error)
	BeginDay(ctx context.Context, key Key, date Date, seq int) (Day, error)
	PutAsset(ctx context.Context, day Day, name string, r io.Reader) error
	DeleteAsset(ctx context.Context, day Day, name string) error
	Publish(ctx context.Context, day Day) error
	DeleteDay(ctx context.Context, day Day) error
}
