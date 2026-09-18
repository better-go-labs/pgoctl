#!/usr/bin/env bash
set -euo pipefail

OUTDIR="${1:-$(dirname "$0")/results/profiles}"
PGOCTL="${PGOCTL:-pgoctl}"
BENCH_TIME="${BENCH_TIME:-30s}"

mkdir -p "$OUTDIR"

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

# ── fasthttp — HTTP request parsing (fixed version) ────────────────────────
collect_bench_fasthttp() {
  local name="fasthttp"
  local workdir="$OUTDIR/$name"

  log "--- $name: setting up bench module ---"
  mkdir -p "$workdir/bench"
  pushd "$workdir/bench" > /dev/null

  if [[ ! -f go.mod ]]; then
    go mod init "bench_${name}"
    cat > bench_test.go << 'BENCH'
package bench_test

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
		_, _ = req.Header.Read(fasthttp.NewBufioReader(fasthttp.NewBytesReader(rawRequest)))
		_ = string(req.Header.RequestURI())
		_ = string(req.Header.Method())
		_ = req.URI().QueryArgs().String()
	}
}
BENCH
    go get "github.com/valyala/fasthttp@latest"
    go mod tidy
  fi

  log "$name: running benchmark to collect profile (benchtime=$BENCH_TIME)..."
  go test -bench=. -benchtime="$BENCH_TIME" -cpuprofile="../profile.pprof" . 2>&1 | tee "../bench.log" || true

  popd > /dev/null
  if [[ -f "$workdir/profile.pprof" ]]; then
    log "$name: profile collected OK ($(du -h "$workdir/profile.pprof" | cut -f1))"
  else
    log "$name: WARNING — no profile collected"
  fi
}

# ── snappy — Compression ──────────────────────────────────────────────────
collect_bench_snappy() {
  local name="snappy"
  local workdir="$OUTDIR/$name"

  log "--- $name: setting up bench module ---"
  mkdir -p "$workdir/bench"
  pushd "$workdir/bench" > /dev/null

  if [[ ! -f go.mod ]]; then
    go mod init "bench_${name}"
    cat > bench_test.go << 'BENCH'
package bench_test

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
}
BENCH
    go get "github.com/golang/snappy@latest"
    go mod tidy
  fi

  log "$name: running benchmark to collect profile (benchtime=$BENCH_TIME)..."
  go test -bench=. -benchtime="$BENCH_TIME" -cpuprofile="../profile.pprof" . 2>&1 | tee "../bench.log" || true

  popd > /dev/null
  if [[ -f "$workdir/profile.pprof" ]]; then
    log "$name: profile collected OK ($(du -h "$workdir/profile.pprof" | cut -f1))"
  else
    log "$name: WARNING — no profile collected"
  fi
}

collect_bench_fasthttp
collect_bench_snappy

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
