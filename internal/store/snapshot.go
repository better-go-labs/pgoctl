package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/merge"
	"github.com/Better-Go-Labs/pgoctl/internal/pprofutil"
	"github.com/google/pprof/profile"
)

// This file holds the unexported helpers shared by Publish and Exclude. The
// store call order of each is fixed by the issue #62 design so the GitHub
// Releases and Actions Artifacts backends behave the same.

// sha256Hex returns the lowercase hex sha256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// cpuTotal returns the sum of the CPU sample values of p, or 0 when p carries
// no CPU sample type.
func cpuTotal(p *profile.Profile) int64 {
	idx, ok := pprofutil.CPUSampleIndex(p)
	if !ok {
		return 0
	}
	var total int64
	for _, s := range p.Sample {
		if idx < len(s.Value) {
			total += s.Value[idx]
		}
	}
	return total
}

// newManifest returns an empty manifest for key at date d and sequence seq.
func newManifest(key Key, d Date, seq int) *Manifest {
	return &Manifest{
		SchemaVersion: SchemaVersion,
		Key:           ManifestKey{Service: key.Service},
		Date:          d.String(),
		Tag:           key.Tag(d, seq),
		Sequence:      seq,
		Merge:         MergeInfoFrom(merge.DefaultOptions()),
		Assets:        map[string]string{},
	}
}

// datesToStrings renders dates as "2006-01-02" strings, preserving order.
func datesToStrings(ds []Date) []string {
	if len(ds) == 0 {
		return nil
	}
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.String()
	}
	return out
}

// findRun returns the index of the run with id in m.Runs, or -1.
func findRun(m *Manifest, id string) int {
	for i := range m.Runs {
		if m.Runs[i].ID == id {
			return i
		}
	}
	return -1
}

// upsertRun records run in m, replacing an existing entry with the same ID so a
// retried publish does not duplicate the run.
func upsertRun(m *Manifest, run RunInfo, sha string) {
	r := Run{
		ID:           run.ID,
		Commit:       run.Commit,
		CapturedAt:   run.CapturedAt,
		Samples:      run.Samples,
		QualityScore: run.QualityScore,
		SHA256:       sha,
	}
	if i := findRun(m, run.ID); i >= 0 {
		// Preserve an existing exclusion so a retry cannot resurrect a bad run.
		r.Excluded = m.Runs[i].Excluded
		r.ExcludedAt = m.Runs[i].ExcludedAt
		r.ExcludedBy = m.Runs[i].ExcludedBy
		r.Reason = m.Runs[i].Reason
		m.Runs[i] = r
		return
	}
	m.Runs = append(m.Runs, r)
}

// highestSeqForDate returns the day of date d with the highest sequence, drafts
// included.
func highestSeqForDate(days []Day, d Date) (Day, bool) {
	var best Day
	found := false
	for _, day := range days {
		if day.Date != d {
			continue
		}
		if !found || day.Seq > best.Seq {
			best, found = day, true
		}
	}
	return best, found
}

// highestPublishedForDate returns the published day of date d with the highest
// sequence. Drafts are ignored.
func highestPublishedForDate(days []Day, d Date) (Day, bool) {
	var best Day
	found := false
	for _, day := range days {
		if day.Draft || day.Date != d {
			continue
		}
		if !found || day.Seq > best.Seq {
			best, found = day, true
		}
	}
	return best, found
}

// highestPublishedPerDate maps each date to its highest published sequence day.
func highestPublishedPerDate(days []Day) map[Date]Day {
	out := make(map[Date]Day)
	for _, day := range days {
		if day.Draft {
			continue
		}
		if cur, ok := out[day.Date]; !ok || day.Seq > cur.Seq {
			out[day.Date] = day
		}
	}
	return out
}

// manifestAssetForDate returns the manifest asset of the highest published
// sequence of date d, or nil when that day has no manifest yet.
func manifestAssetForDate(days []Day, d Date) *Asset {
	cur, ok := highestPublishedForDate(days, d)
	if !ok {
		return nil
	}
	if a, has := cur.Assets[AssetManifest]; has {
		a := a
		return &a
	}
	return nil
}

// readManifest reads and decodes the manifest asset of day.
func readManifest(ctx context.Context, s Store, day Day) (*Manifest, error) {
	rc, err := s.ReadAsset(ctx, day, AssetManifest)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return DecodeManifest(rc)
}

// readAll reads the named asset of day fully, closing the reader.
func readAll(ctx context.Context, s Store, day Day, name string) ([]byte, error) {
	rc, err := s.ReadAsset(ctx, day, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// copyRuns copies every run profile asset from one day to another.
func copyRuns(ctx context.Context, s Store, from, to Day) error {
	for name := range from.Assets {
		if _, ok := RunIDFromAsset(name); !ok {
			continue
		}
		data, err := readAll(ctx, s, from, name)
		if err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}
		if err := s.PutAsset(ctx, to, name, bytes.NewReader(data)); err != nil {
			return fmt.Errorf("copying %s: %w", name, err)
		}
	}
	return nil
}

// openForWrite resolves the day to write for date d and returns its manifest.
// It creates a draft when no day exists, writes in place to a draft or a
// mutable published day, and opens the next sequence when the current day is a
// published immutable release.
func openForWrite(ctx context.Context, s Store, key Key, days []Day, d Date) (Day, *Manifest, error) {
	cur, ok := highestSeqForDate(days, d)
	if !ok {
		day, err := s.BeginDay(ctx, key, d, 0)
		if err != nil {
			return Day{}, nil, fmt.Errorf("beginning %s: %w", key.Tag(d, 0), err)
		}
		return day, newManifest(key, d, 0), nil
	}

	if cur.Immutable {
		seq := cur.Seq + 1
		day, err := s.BeginDay(ctx, key, d, seq)
		if err != nil {
			return Day{}, nil, fmt.Errorf("beginning %s: %w", key.Tag(d, seq), err)
		}
		m, err := readManifest(ctx, s, cur)
		if err != nil {
			return Day{}, nil, fmt.Errorf("reading manifest of %s: %w", key.Tag(d, cur.Seq), err)
		}
		if err := copyRuns(ctx, s, cur, day); err != nil {
			return Day{}, nil, err
		}
		m.Sequence = seq
		m.Tag = key.Tag(d, seq)
		m.Supersedes = append(m.Supersedes, key.Tag(d, cur.Seq))
		return day, m, nil
	}

	if cur.Has(AssetManifest) {
		m, err := readManifest(ctx, s, cur)
		if err != nil {
			return Day{}, nil, fmt.Errorf("reading manifest of %s: %w", key.Tag(d, cur.Seq), err)
		}
		return cur, m, nil
	}

	// An empty draft: seed its runs and manifest from the highest published
	// lower sequence of the same date, if one exists.
	m := newManifest(key, d, cur.Seq)
	if prev, ok := highestPublishedForDate(days, d); ok && prev.Seq < cur.Seq {
		if pm, err := readManifest(ctx, s, prev); err == nil {
			if err := copyRuns(ctx, s, prev, cur); err != nil {
				return Day{}, nil, err
			}
			pm.Sequence = cur.Seq
			pm.Tag = key.Tag(d, cur.Seq)
			pm.Supersedes = append(pm.Supersedes, key.Tag(d, prev.Seq))
			m = pm
		}
	}
	return cur, m, nil
}

// selectDaily reads every run of day, marks the missing ones, and returns the
// bytes and ID of the non-excluded run with the highest quality score (newer
// capture time breaks a tie). It returns nil, "" when no run is usable.
func selectDaily(ctx context.Context, s Store, day Day, m *Manifest) ([]byte, string, error) {
	var (
		best     *Run
		bestData []byte
	)
	for i := range m.Runs {
		r := &m.Runs[i]
		data, err := readAll(ctx, s, day, RunAsset(r.ID))
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				r.Missing = true
				continue
			}
			return nil, "", fmt.Errorf("reading run %s: %w", r.ID, err)
		}
		r.Missing = false
		if r.Excluded {
			continue
		}
		if best == nil || r.QualityScore > best.QualityScore ||
			(r.QualityScore == best.QualityScore && r.CapturedAt.After(best.CapturedAt)) {
			best = r
			bestData = data
		}
	}
	if best == nil {
		return nil, "", nil
	}
	return bestData, best.ID, nil
}

// normalize scales each profile so its CPU total equals the maximum CPU total
// across the inputs. Scale factors are at least 1, so no count is driven to 0.
func normalize(ps []*profile.Profile) {
	totals := make([]int64, len(ps))
	var maxTotal int64
	for i, p := range ps {
		totals[i] = cpuTotal(p)
		if totals[i] > maxTotal {
			maxTotal = totals[i]
		}
	}
	if maxTotal == 0 {
		return
	}
	for i, p := range ps {
		if totals[i] > 0 && totals[i] != maxTotal {
			p.Scale(float64(maxTotal) / float64(totals[i]))
		}
	}
}

// capturedAt returns the capture time of a window day for merge weighting: the
// selected run's capture time from the manifest, falling back to the date's
// midnight so a profile without TimeNanos is not treated as ancient.
func capturedAt(ctx context.Context, s Store, day Day, d Date) time.Time {
	if m, err := readManifest(ctx, s, day); err == nil && m.DailyRun != "" {
		if i := findRun(m, m.DailyRun); i >= 0 && !m.Runs[i].CapturedAt.IsZero() {
			return m.Runs[i].CapturedAt
		}
	}
	return d.Time()
}

// buildDefault merges the window dailies into default.pgo. The day at date at
// contributes own directly; the other window dates are read from the store.
// It returns nil, nil when own is nil (no selected run for at).
func buildDefault(ctx context.Context, s Store, key Key, days []Day, at Date, own []byte, lookbackDays, maxProfiles int) ([]byte, []Date, error) {
	_ = key
	if own == nil {
		return nil, nil, nil
	}
	byDate := highestPublishedPerDate(days)
	candidates := []Date{at}
	for d := range byDate {
		candidates = append(candidates, d)
	}
	window := SelectWindow(candidates, at, lookbackDays, maxProfiles)

	var (
		inputs []merge.Input
		used   []Date
	)
	for _, d := range window {
		if d == at {
			inputs = append(inputs, merge.Input{Data: own, CapturedAt: at.Time()})
			used = append(used, d)
			continue
		}
		day, ok := byDate[d]
		if !ok {
			continue
		}
		data, err := readAll(ctx, s, day, AssetDaily)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, nil, fmt.Errorf("reading daily of %s: %w", d, err)
		}
		inputs = append(inputs, merge.Input{Data: data, CapturedAt: capturedAt(ctx, s, day, d)})
		used = append(used, d)
	}

	profiles := make([]*profile.Profile, len(inputs))
	for i, in := range inputs {
		p, err := profile.ParseData(in.Data)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing window profile %d: %w", i, err)
		}
		profiles[i] = p
	}
	normalize(profiles)

	norm := make([]merge.Input, len(profiles))
	for i, p := range profiles {
		var buf bytes.Buffer
		if err := p.Write(&buf); err != nil {
			return nil, nil, fmt.Errorf("writing normalized profile %d: %w", i, err)
		}
		norm[i] = merge.Input{Data: buf.Bytes(), CapturedAt: inputs[i].CapturedAt}
	}

	var out bytes.Buffer
	if err := merge.Profiles(norm, merge.DefaultOptions(), &out); err != nil {
		return nil, nil, fmt.Errorf("merging default.pgo: %w", err)
	}
	return out.Bytes(), used, nil
}

// checkUnchanged verifies that the manifest of date d still matches want, the
// asset seen when the flow started. It guards against a competing run for the
// same key publishing between the flow's reads and its write.
func checkUnchanged(ctx context.Context, s Store, key Key, d Date, want *Asset) error {
	days, err := s.ListDays(ctx, key)
	if err != nil {
		return fmt.Errorf("listing days: %w", err)
	}
	cur, ok := highestPublishedForDate(days, d)
	var got *Asset
	seq := 0
	if ok {
		seq = cur.Seq
		if a, has := cur.Assets[AssetManifest]; has {
			a := a
			got = &a
		}
	}
	switch {
	case want == nil && got != nil:
		return conflictError(key, d, seq)
	case want == nil:
		return nil
	case got == nil || got.ID != want.ID || !got.UpdatedAt.Equal(want.UpdatedAt):
		return conflictError(key, d, seq)
	default:
		return nil
	}
}

// conflictError wraps ErrConcurrentUpdate with the tag and the concurrency hint.
func conflictError(key Key, d Date, seq int) error {
	label := key.Prefix
	if key.Service != "" {
		label += "-" + key.Service
	}
	return fmt.Errorf(
		"manifest of %s changed during publish; another run for this key is active (set concurrency: pgo-profile-%s): %w",
		key.Tag(d, seq), label, ErrConcurrentUpdate,
	)
}

// writeSnapshot writes daily.pprof, default.pgo and manifest.json, in that
// order, so a partial write never exposes a stale manifest. A nil daily or def
// deletes that asset.
func writeSnapshot(ctx context.Context, s Store, day Day, m *Manifest, daily, def []byte) error {
	if m.Assets == nil {
		m.Assets = map[string]string{}
	}
	if err := putOrDelete(ctx, s, day, AssetDaily, daily, m); err != nil {
		return err
	}
	if err := putOrDelete(ctx, s, day, AssetDefault, def, m); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := m.Encode(&buf); err != nil {
		return fmt.Errorf("encoding manifest: %w", err)
	}
	if err := s.PutAsset(ctx, day, AssetManifest, bytes.NewReader(buf.Bytes())); err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}
	return nil
}

// putOrDelete writes data under name and records its sha256 in m.Assets, or
// deletes the asset and drops its sha when data is nil.
func putOrDelete(ctx context.Context, s Store, day Day, name string, data []byte, m *Manifest) error {
	if data == nil {
		delete(m.Assets, name)
		if err := s.DeleteAsset(ctx, day, name); err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("deleting %s: %w", name, err)
		}
		return nil
	}
	if err := s.PutAsset(ctx, day, name, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	m.Assets[name] = sha256Hex(data)
	return nil
}
