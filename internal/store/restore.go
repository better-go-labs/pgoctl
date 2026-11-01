package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
)

// Restore writes the newest usable default.pgo for key to w and returns the
// matching manifest. It considers each date newest first, taking only the
// highest published sequence of that date, and skips to the next date when an
// asset is missing or its sha256 does not match the manifest. A lower sequence
// of the same date is never a fallback, because it can hold a run that a later
// exclude removed. It returns ErrNoSnapshot when no date is usable.
func Restore(ctx context.Context, s Store, key Key, w io.Writer) (*Manifest, error) {
	days, err := s.ListDays(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("listing days: %w", err)
	}
	byDate := highestPublishedPerDate(days)
	dates := make([]Date, 0, len(byDate))
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].After(dates[j]) })

	for _, d := range dates {
		day := byDate[d]
		if !usable(day) {
			continue
		}
		m, err := readManifest(ctx, s, day)
		if err != nil {
			if errors.Is(err, ErrUnknownSchema) || errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("reading manifest of %s: %w", key.Tag(d, day.Seq), err)
		}
		data, err := readAll(ctx, s, day, AssetDefault)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("reading default of %s: %w", key.Tag(d, day.Seq), err)
		}
		// A manifest with no recorded sha for default.pgo cannot be verified, so
		// treat it as unusable rather than trusting the bytes blindly.
		if sum := m.Assets[AssetDefault]; sum == "" || sha256Hex(data) != sum {
			continue
		}
		if _, err := w.Write(data); err != nil {
			return nil, fmt.Errorf("writing default: %w", err)
		}
		return m, nil
	}
	return nil, ErrNoSnapshot
}

// usable reports whether day is a published snapshot carrying both a default.pgo
// and a manifest.
func usable(d Day) bool {
	return !d.Draft && d.Has(AssetDefault) && d.Has(AssetManifest)
}
