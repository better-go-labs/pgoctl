#!/usr/bin/env bash
# collect-and-eval.sh — collect CPU profiles from real open-source Go libraries
# and run pgoctl leverage-check against each library's own source directory.
#
# Usage: ./collect-and-eval.sh [outdir]
#
# Requires: go, pgoctl (in PATH or PGOCTL env var)
#
# Key note: --dir must point to the library's own module directory, not a
# wrapper benchmark module. A wrapper module with only *_test.go files produces
# zero go-build inline output (test files are excluded from go build ./...),
# yielding incorrect NONE verdicts. The Go module cache provides read-only but
# buildable source at $(go env GOMODCACHE)/<module@version>.

set -euo pipefail

OUTDIR="${1:-$(dirname "$0")/results/v3}"
PGOCTL="${PGOCTL:-pgoctl}"
BENCH_TIME="${BENCH_TIME:-10s}"
GOMODCACHE="$(go env GOMODCACHE)"

mkdir -p "$OUTDIR"
log() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

# run_repo <name> <module> <version> <bench_function> <bench_file_content>
run_repo() {
  local name="$1"
  local module="$2"
  local version="$3"
  local bench_fn="$4"
  local bench_content="$5"

  local workdir="$OUTDIR/$name"
  local lib_dir="$GOMODCACHE/${module}@${version}"

  log "=== $name ==="
  mkdir -p "$workdir"

  # ── 1. Set up benchmark wrapper module ──────────────────────────────────
  if [[ ! -f "$workdir/go.mod" ]]; then
    pushd "$workdir" > /dev/null
    go mod init "bench_${name}"
    printf '%s\n' "$bench_content" > bench_test.go
    go get "${module}@${version}"
    go mod tidy
    popd > /dev/null
  fi

  # ── 2. Collect CPU profile ───────────────────────────────────────────────
  if [[ ! -s "$workdir/profile.pprof" ]]; then
    log "$name: collecting profile (benchtime=$BENCH_TIME)..."
    pushd "$workdir" > /dev/null
    go test -bench="$bench_fn" -run='^$' -count=1 \
        -benchtime="$BENCH_TIME" -cpuprofile=profile.pprof . 2>&1 | tail -3
    popd > /dev/null
  else
    log "$name: reusing existing profile"
  fi

  # ── 3. Run leverage-check (correct --dir = library source) ───────────────
  log "$name: running leverage-check with --dir=$lib_dir"
  "$PGOCTL" leverage-check "$workdir/profile.pprof" \
      --dir "$lib_dir" \
      --format json > "$workdir/leverage.json" 2>&1

  # Summary line
  verdict=$(python3 -c "import json,sys; d=json.load(open('$workdir/leverage.json')); ba=d.get('build_analysis',{}); print(d['verdict'], ba.get('devirt_decisions','-'), ba.get('pgo_extra_inlines','-'))" 2>/dev/null || echo "ERROR")
  log "$name: $verdict (verdict devirt extra_inlines)"

  # ── 4. Bench comparison: baseline vs PGO ─────────────────────────────────
  log "$name: baseline benchmarks..."
  pushd "$workdir" > /dev/null
  go test -bench="$bench_fn" -run='^$' -count=5 -benchtime=5s > baseline.txt 2>&1
  log "$name: PGO benchmarks (-pgo=profile.pprof)..."
  go test -bench="$bench_fn" -run='^$' -count=5 -benchtime=5s \
      -pgo=profile.pprof > pgo.txt 2>&1
  popd > /dev/null

  # Print comparison
  log "$name: benchstat comparison:"
  benchstat "$workdir/baseline.txt" "$workdir/pgo.txt" 2>/dev/null \
    | grep -v '^$' | sed "s/^/  [$name] /"
}

# ── Define repositories ────────────────────────────────────────────────────

GOLDMARK_BENCH='package bench_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
)

var md = []byte(strings.Repeat("# Hello\n\nThis is **bold** and *italic* text with `code` and [links](http://example.com).\n\n- item 1\n- item 2\n- item 3\n\n```go\nfunc main() { fmt.Println(\"hello\") }\n```\n\n", 100))

func BenchmarkGoldmark(b *testing.B) {
	engine := goldmark.New()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		engine.Convert(md, &buf)
	}
}'

GJSON_BENCH='package bench_test

import (
	"testing"

	"github.com/tidwall/gjson"
)

var jsonDoc = `{"name":{"first":"Tom","last":"Smith"},"age":37,"friends":[{"name":"Alice","age":30},{"name":"Bob","age":35}],"scores":[1,2,3,4,5]}`

func BenchmarkGJSON(b *testing.B) {
	for i := 0; i < b.N; i++ {
		gjson.Get(jsonDoc, "name.first")
		gjson.Get(jsonDoc, "friends.#.name")
		gjson.Get(jsonDoc, "scores.@sum")
	}
}'

GOJSON_BENCH='package bench_test

import (
	"testing"

	gojson "github.com/goccy/go-json"
)

type Person struct {
	Name    string   `json:"name"`
	Age     int      `json:"age"`
	Friends []string `json:"friends"`
}

var sample = Person{Name: "Alice", Age: 30, Friends: []string{"Bob", "Carol", "Dave"}}

func BenchmarkGoJSON(b *testing.B) {
	for i := 0; i < b.N; i++ {
		data, _ := gojson.Marshal(sample)
		var out Person
		gojson.Unmarshal(data, &out)
	}
}'

SNAPPY_BENCH='package bench_test

import (
	"strings"
	"testing"

	"github.com/golang/snappy"
)

var data = []byte(strings.Repeat("Hello, World! This is test data for compression benchmarking. 1234567890 abcdefghijklmnopqrstuvwxyz. ", 1000))

func BenchmarkSnappy(b *testing.B) {
	for i := 0; i < b.N; i++ {
		compressed := snappy.Encode(nil, data)
		_, _ = snappy.Decode(nil, compressed)
	}
}'

FASTHTTP_BENCH='package bench_test

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/valyala/fasthttp"
)

var rawReq = []byte("GET /path?foo=bar&baz=qux HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\nAccept: */*\r\nUser-Agent: bench/1.0\r\n\r\n")

func BenchmarkFastHTTP(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var req fasthttp.Request
		br := bufio.NewReader(bytes.NewReader(rawReq))
		req.Read(br)
		_ = req.URI()
	}
}'

# ── Run evaluations ────────────────────────────────────────────────────────

run_repo "goldmark"  "github.com/yuin/goldmark"    "v1.8.6"  "BenchmarkGoldmark" "$GOLDMARK_BENCH"
run_repo "gjson"     "github.com/tidwall/gjson"    "v1.19.0" "BenchmarkGJSON"    "$GJSON_BENCH"
run_repo "go-json"   "github.com/goccy/go-json"    "v0.10.6" "BenchmarkGoJSON"   "$GOJSON_BENCH"
run_repo "snappy"    "github.com/golang/snappy"    "v1.0.0"  "BenchmarkSnappy"   "$SNAPPY_BENCH"
run_repo "fasthttp"  "github.com/valyala/fasthttp" "v1.74.0" "BenchmarkFastHTTP" "$FASTHTTP_BENCH"

log "Done. Results in $OUTDIR"
