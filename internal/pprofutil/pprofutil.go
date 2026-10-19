// Package pprofutil holds small helpers shared by the profile analyzers.
package pprofutil

import (
	"regexp"
	"strings"

	"github.com/google/pprof/profile"
)

// versionSuffix matches a gopkg.in style ".v3" that belongs to the import path.
var versionSuffix = regexp.MustCompile(`^(\.v[0-9]+)(?:\.|$)`)

// PackageOf returns the import path of the package defining pprof function fn,
// e.g. gopkg.in/yaml.v3.(*decoder).unmarshal -> gopkg.in/yaml.v3.
func PackageOf(fn string) string {
	start := strings.LastIndex(fn, "/") + 1
	rest := fn[start:]
	cut := len(rest)
	if i := strings.IndexAny(rest, "(."); i >= 0 {
		cut = i
		if m := versionSuffix.FindStringSubmatch(rest[i:]); m != nil {
			cut = i + len(m[1])
		}
	}
	if pkg := fn[:start+cut]; pkg != "" {
		return pkg
	}
	return fn
}

// Resolver exists because a name alone cannot tell go.uuid from a method;
// a nil Resolver falls back to PackageOf.
type Resolver struct {
	paths map[string]bool
}

// NewResolver drops empty paths so a name starting with "." never matches.
func NewResolver(importPaths []string) *Resolver {
	r := &Resolver{paths: make(map[string]bool, len(importPaths))}
	for _, p := range importPaths {
		if p != "" {
			r.paths[p] = true
		}
	}
	return r
}

// PackageOf scans from the right so the longest known import path wins.
func (r *Resolver) PackageOf(fn string) string {
	if r != nil {
		for i := len(fn) - 1; i > 0; i-- {
			if fn[i] == '.' && r.paths[fn[:i]] {
				return fn[:i]
			}
		}
	}
	return PackageOf(fn)
}

// SampleCount sums the samples/count values. len(p.Sample) is the number of
// unique stacks, not samples. Falls back to the row count without that type.
func SampleCount(p *profile.Profile) int64 {
	idx := -1
	for i, st := range p.SampleType {
		if st.Type == "samples" && st.Unit == "count" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return int64(len(p.Sample))
	}
	var n int64
	for _, s := range p.Sample {
		if idx < len(s.Value) {
			n += s.Value[idx]
		}
	}
	return n
}
