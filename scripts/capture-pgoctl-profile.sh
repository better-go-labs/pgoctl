#!/usr/bin/env bash
# capture-pgoctl-profile.sh: record a CPU profile of pgoctl itself (Go 1.20+ / PGO-ready).
#
# Uses in-process benchmarks in cmd/pgoctl so symbols match ./cmd/pgoctl when you run:
#   go build -pgo=testdata/pgoctl_cpu.pprof ./cmd/pgoctl
#   pgoctl leverage-check testdata/pgoctl_cpu.pprof --dir . --package ./cmd/pgoctl
#
# Usage:
#   ./scripts/capture-pgoctl-profile.sh
#   ./scripts/capture-pgoctl-profile.sh /path/to/out.pprof
#   BENCHTIME=30s ./scripts/capture-pgoctl-profile.sh

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT/testdata/pgoctl_cpu.pprof}"
BENCHTIME="${BENCHTIME:-15s}"
WORKLOAD="${PGOCTL_WORKLOAD_PROFILE:-$ROOT/testdata/prometheus_cpu.pprof}"

if [[ ! -f "$WORKLOAD" ]]; then
  echo "error: workload pprof not found: $WORKLOAD" >&2
  echo "  (pgoctl reads this file repeatedly while recording CPU; it is not the output profile.)" >&2
  exit 1
fi

mkdir -p "$(dirname "$OUT")"
rm -f "$OUT"

echo "Recording pgoctl CPU profile -> $OUT"
echo "  benchtime: $BENCHTIME"
echo "  workload:  $WORKLOAD"

cd "$ROOT"
PGOCTL_WORKLOAD_PROFILE="$WORKLOAD" go test ./cmd/pgoctl \
  -run='^$' \
  -bench=BenchmarkCapturePgoctlCPU \
  -benchtime="$BENCHTIME" \
  -cpuprofile="$OUT" \
  -benchmem=false

if [[ ! -s "$OUT" ]]; then
  echo "error: profile was not written or is empty" >&2
  exit 1
fi

echo "Wrote $(wc -c <"$OUT" | tr -d ' ') bytes"

echo -n "Checking go build -pgo=… ./cmd/pgoctl … "
if go build -pgo="$OUT" -o /dev/null ./cmd/pgoctl; then
  echo "OK"
else
  echo "FAILED" >&2
  exit 1
fi

if [[ -x "$ROOT/bin/pgoctl" ]]; then
  echo ""
  echo "Sample leverage-check (profile-only):"
  "$ROOT/bin/pgoctl" leverage-check "$OUT" --top 5 || true
fi

echo ""
echo "Full leverage-check with build analysis:"
echo "  ./bin/pgoctl leverage-check $OUT --dir . --package ./cmd/pgoctl"
