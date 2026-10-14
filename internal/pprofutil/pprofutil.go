// Package pprofutil holds small helpers shared by the profile analyzers.
package pprofutil

import (
	"regexp"
	"strings"
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
