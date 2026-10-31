package store

import (
	"context"
	"fmt"
)

// Restore selects the newest valid day in the window and returns the day
// identifier and the contents of its default.pgo. It tries each window day
// newest first; a day is skipped when its manifest is missing, the default.pgo
// asset is missing, or the SHA-256 does not match the manifest's record.
//
// The returned Day is used by Prune as the protected day that must not be
// deleted.
func Restore(ctx context.Context, s Store, key string, opts WindowOpts) (Day, []byte, error) {
	allDays, err := s.ListDays(ctx, key)
	if err != nil {
		return Day{}, nil, fmt.Errorf("listing days: %w", err)
	}

	window := SelectWindow(allDays, opts)

	for _, d := range window {
		m, err := s.GetManifest(ctx, key, d)
		if err != nil {
			continue
		}

		data, err := s.GetAsset(ctx, key, d, "default.pgo")
		if err != nil {
			continue
		}

		// Verify SHA-256 when the manifest records one for default.pgo.
		if expected, ok := m.SHA256["default.pgo"]; ok && expected != "" {
			if sha256Hex(data) != expected {
				continue
			}
		}

		return d, data, nil
	}

	return Day{}, nil, fmt.Errorf("no valid snapshot in window for key %q: %w", key, ErrNotFound)
}
