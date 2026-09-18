#!/usr/bin/env bash
# collect-profiles.sh — collect CPU profiles from real open-source Go repos
# for pgoctl leverage-check validation.
#
# Usage: ./collect-profiles.sh [outdir]
#
# Creates <outdir>/<repo>/profile.pprof for each repo.
# Requires: go, git, curl

set -euo pipefail

OUTDIR="${1:-$(dirname "$0")/results/profiles}"
PGOCTL="${PGOCTL:-pgoctl}"
BENCH_TIME="${BENCH_TIME:-30s}"

mkdir -p "$OUTDIR"

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

# collect_bench <name> <module_path> <bench_file_content>
collect_bench() {
  local name="$1"
  local module="$2"
  local bench_content="$3"
  local workdir="$OUTDIR/$name"

  log "--- $name: setting up bench module ---"
  mkdir -p "$workdir/bench"
  pushd "$workdir/bench" > /dev/null

  if [[ ! -f go.mod ]]; then
    go mod init "bench_${name}"
    printf '%s\n' "$bench_content" > bench_test.go
    go get "${module}@latest"
    go mod tidy
  fi

  log "$name: running benchmark to collect profile (benchtime=$BENCH_TIME)..."
  go test -bench=. -benchtime="$BENCH_TIME" -cpuprofile="../profile.pprof" . 2>&1 | tee "../bench.log"

  popd > /dev/null
  if [[ -f "$workdir/profile.pprof" ]]; then
    log "$name: profile collected OK ($(du -h "$workdir/profile.pprof" | cut -f1))"
  else
    log "$name: WARNING — no profile collected"
  fi
}

# ── 1. goldmark — Markdown parser (HIGH expected: interface-heavy node walking) ──────────────
collect_bench "goldmark" "github.com/yuin/goldmark" 'package bench_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var markdownDoc = []byte(strings.Repeat(
	"# Heading\n\nParagraph with **bold**, *italic*, and `code` inline.\n\n"+
		"- list item one\n- list item two\n- list item three\n\n"+
		"```go\nfunc main() {\n\tfmt.Println(\"hello, pgo\")\n}\n```\n\n"+
		"> blockquote text here\n\n",
	200,
))

func BenchmarkGoldmark(b *testing.B) {
	engine := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		if err := engine.Convert(markdownDoc, &buf); err != nil {
			b.Fatal(err)
		}
	}
}'

# ── 2. go-json — JSON encoder/decoder (HIGH expected: reflection+interface-heavy) ─────────
collect_bench "go-json" "github.com/goccy/go-json" 'package bench_test

import (
	"testing"

	gojson "github.com/goccy/go-json"
)

type Record struct {
	Name   string            `json:"name"`
	Value  int               `json:"value"`
	Tags   []string          `json:"tags"`
	Extra  map[string]string `json:"extra"`
	Nested struct {
		Score float64 `json:"score"`
		Label string  `json:"label"`
	} `json:"nested"`
}

var records []Record

func init() {
	for i := 0; i < 100; i++ {
		records = append(records, Record{
			Name:  "item",
			Value: i,
			Tags:  []string{"alpha", "beta", "gamma"},
			Extra: map[string]string{"k1": "v1", "k2": "v2"},
			Nested: struct {
				Score float64 `json:"score"`
				Label string  `json:"label"`
			}{float64(i) * 1.5, "label"},
		})
	}
}

func BenchmarkGoJSON(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, rec := range records {
			buf, _ := gojson.Marshal(rec)
			var out Record
			gojson.Unmarshal(buf, &out) //nolint:errcheck
		}
	}
}'

# ── 3. gjson — JSON path queries (HIGH expected: CPU-bound text parsing) ─────────────────
collect_bench "gjson" "github.com/tidwall/gjson" 'package bench_test

import (
	"testing"

	"github.com/tidwall/gjson"
)

const jsonDoc = `{"store":{"book":[{"category":"reference","author":"Nigel Rees","title":"Sayings of the Century","price":8.95},{"category":"fiction","author":"Evelyn Waugh","title":"Sword of Honour","price":12.99},{"category":"fiction","author":"Herman Melville","title":"Moby Dick","price":8.99},{"category":"fiction","author":"J. R. R. Tolkien","title":"The Lord of the Rings","price":22.99}],"bicycle":{"color":"red","price":19.95}},"expensive":10}`

func BenchmarkGJSON(b *testing.B) {
	queries := []string{
		"store.book.#.author",
		"store.book.#[price<10].title",
		"store.bicycle.color",
		"store.book.#.price",
		"store.book.0.author",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, q := range queries {
			gjson.Get(jsonDoc, q)
		}
	}
}'

# ── 4. fasthttp — HTTP request parsing (LOW/NONE expected: partly I/O bound) ────────────
collect_bench "fasthttp" "github.com/valyala/fasthttp" 'package bench_test

import (
	"testing"

	"github.com/valyala/fasthttp"
)

var rawRequest = []byte(
	"GET /api/v1/users?page=1&limit=100&sort=created_at HTTP/1.1\r\n" +
		"Host: api.example.com\r\n" +
		"Content-Type: application/json; charset=utf-8\r\n" +
		"Accept: application/json\r\n" +
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.test.signature\r\n" +
		"X-Request-ID: abc-123-def-456\r\n" +
		"User-Agent: bench/1.0\r\n" +
		"\r\n",
)

func BenchmarkFastHTTPParsing(b *testing.B) {
	var req fasthttp.Request
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req.Reset()
		req.Header.Parse(rawRequest)
		_ = string(req.Header.RequestURI())
		_ = string(req.Header.Method())
		_ = req.URI().QueryArgs().String()
	}
}'

# ── 5. snappy — Compression (NONE/LOW expected: C-extension or assembly hot-path) ────────
collect_bench "snappy" "github.com/golang/snappy" 'package bench_test

import (
	"strings"
	"testing"

	"github.com/golang/snappy"
)

var compressData = []byte(strings.Repeat(
	"The quick brown fox jumps over the lazy dog. "+
		"Pack my box with five dozen liquor jugs. "+
		"How vexingly quick daft zebras jump! 0123456789. ",
	500,
))

func BenchmarkSnappy(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		compressed := snappy.Encode(nil, compressData)
		if _, err := snappy.Decode(nil, compressed); err != nil {
			b.Fatal(err)
		}
	}
}'

log "=== Profile collection complete. Results in $OUTDIR ==="
for d in "$OUTDIR"/*/; do
  name=$(basename "$d")
  pprof="$d/profile.pprof"
  if [[ -f "$pprof" ]]; then
    printf '  %-12s  %s\n' "$name" "$(du -h "$pprof" | cut -f1)"
  else
    printf '  %-12s  MISSING\n' "$name"
  fi
done
