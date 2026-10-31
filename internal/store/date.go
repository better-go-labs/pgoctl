// Package store defines the backend interface, types, and flow logic for daily
// profile storage. The default backend is GitHub Releases; the same Store
// interface covers Actions Artifacts and future backends.
package store

import (
	"fmt"
	"time"
)

// dateLayout is the wire and display format for a Date.
const dateLayout = "2006-01-02"

// Date is a calendar day in UTC. It is kept as a struct wrapping a UTC-midnight
// time.Time so that two Dates naming the same day compare equal with ==.
type Date struct {
	t time.Time // always UTC midnight
}

// ParseDate parses a "2006-01-02" string as UTC midnight.
func ParseDate(s string) (Date, error) {
	t, err := time.ParseInLocation(dateLayout, s, time.UTC)
	if err != nil {
		return Date{}, fmt.Errorf("parsing date %q: %w", s, err)
	}
	return Date{t: t}, nil
}

// DateOf truncates t to midnight UTC, so a time anywhere in a UTC day maps to
// the same Date regardless of its original location.
func DateOf(t time.Time) Date {
	u := t.UTC()
	return Date{t: time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)}
}

// String renders the date as "2006-01-02".
func (d Date) String() string { return d.t.Format(dateLayout) }

// AddDays returns the date n days later; n may be negative.
func (d Date) AddDays(n int) Date { return Date{t: d.t.AddDate(0, 0, n)} }

// Before reports whether d is strictly before o.
func (d Date) Before(o Date) bool { return d.t.Before(o.t) }

// After reports whether d is strictly after o.
func (d Date) After(o Date) bool { return d.t.After(o.t) }

// Time returns the UTC-midnight time.Time backing d.
func (d Date) Time() time.Time { return d.t }

// IsZero reports whether d is the zero Date.
func (d Date) IsZero() bool { return d.t.IsZero() }
