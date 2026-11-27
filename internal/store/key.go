package store

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DefaultPrefix is the tag prefix used when none is configured.
const DefaultPrefix = "profile"

// Well-known logical asset names within a day.
const (
	AssetDaily    = "daily.pprof"
	AssetDefault  = "default.pgo"
	AssetManifest = "manifest.json"
)

// runAssetPrefix and runAssetSuffix bracket a run's raw profile asset name.
const (
	runAssetPrefix = "runs/"
	runAssetSuffix = ".pprof"
)

// runIDPattern matches a GitHub Actions run ID with attempt: "<digits>-<digits>".
var runIDPattern = regexp.MustCompile(`^[0-9]+-[0-9]+$`)

// Key identifies a profile line within a repository: an optional service under a
// tag prefix. Days for different keys never share tags, so one key's listing
// never reads another key's releases.
type Key struct {
	Repo    string // "owner/name"
	Prefix  string
	Service string
}

// Validate returns an input error for a malformed Key so a flow rejects it
// before any backend call.
func (k Key) Validate() error {
	if !strings.Contains(k.Repo, "/") {
		return fmt.Errorf("key repo %q must be owner/name", k.Repo)
	}
	if k.Prefix == "" {
		return fmt.Errorf("key prefix must not be empty")
	}
	if strings.Contains(k.Service, "/") {
		return fmt.Errorf("key service %q must not contain /", k.Service)
	}
	return nil
}

// Tag returns the release tag for date d at sequence seq, in the form
// "<prefix>/[<service>/]<yyyy-mm-dd>.<seq>".
func (k Key) Tag(d Date, seq int) string {
	var b strings.Builder
	b.WriteString(k.Prefix)
	b.WriteByte('/')
	if k.Service != "" {
		b.WriteString(k.Service)
		b.WriteByte('/')
	}
	b.WriteString(d.String())
	b.WriteByte('.')
	b.WriteString(strconv.Itoa(seq))
	return b.String()
}

// ParseTag parses a tag produced by Tag for this key. It returns ok=false when
// the tag belongs to a different prefix or service, so a plain key never reads a
// service key's tags and vice versa.
func (k Key) ParseTag(tag string) (d Date, seq int, ok bool) {
	prefix := k.Prefix + "/"
	if !strings.HasPrefix(tag, prefix) {
		return Date{}, 0, false
	}
	rest := tag[len(prefix):]
	if k.Service != "" {
		svc := k.Service + "/"
		if !strings.HasPrefix(rest, svc) {
			return Date{}, 0, false
		}
		rest = rest[len(svc):]
	}
	// rest must now be "<yyyy-mm-dd>.<seq>" with no further path segment.
	if strings.Contains(rest, "/") {
		return Date{}, 0, false
	}
	dot := strings.LastIndex(rest, ".")
	if dot < 0 {
		return Date{}, 0, false
	}
	parsed, err := ParseDate(rest[:dot])
	if err != nil {
		return Date{}, 0, false
	}
	seqStr := rest[dot+1:]
	n, err := strconv.Atoi(seqStr)
	// strconv.Atoi accepts a sign and leading zeros; the tag form is a canonical
	// non-negative decimal, so reject anything Itoa would not reproduce.
	if err != nil || n < 0 || strconv.Itoa(n) != seqStr {
		return Date{}, 0, false
	}
	return parsed, n, true
}

// RunAsset returns the logical asset name for a run's raw profile.
func RunAsset(runID string) string { return runAssetPrefix + runID + runAssetSuffix }

// RunIDFromAsset returns the run ID encoded in a "runs/<id>.pprof" asset name.
func RunIDFromAsset(name string) (string, bool) {
	if !strings.HasPrefix(name, runAssetPrefix) || !strings.HasSuffix(name, runAssetSuffix) {
		return "", false
	}
	id := name[len(runAssetPrefix) : len(name)-len(runAssetSuffix)]
	if !ValidRunID(id) {
		return "", false
	}
	return id, true
}

// ValidRunID guards a run ID before it becomes a "runs/<id>.pprof" asset name,
// keeping that name parseable back to the ID.
func ValidRunID(id string) bool { return runIDPattern.MatchString(id) }
