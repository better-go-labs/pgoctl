#!/usr/bin/env bash
# run-pgo-cycle.sh - run full collect → build → benchmark PGO cycle for a repo.
#
# Usage: ./run-pgo-cycle.sh <name> <profile.pprof> <module_dir>
#
# Builds the module twice (baseline, PGO) and runs benchmarks to compare.
# Records ns/op before and after; outputs a JSON result.

set -euo pipefail

NAME="${1:?usage: $0 <name> <profile.pprof> <module_dir>}"
PROFILE="${2:?}"
MODULE_DIR="${3:?}"
OUTDIR="${4:-$(dirname "$0")/results}"
BENCH_TIME="${BENCH_TIME:-10s}"
TRIALS="${TRIALS:-3}"

mkdir -p "$OUTDIR"

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

if [[ ! -f "$PROFILE" ]]; then
  echo "ERROR: profile not found: $PROFILE" >&2
  exit 1
fi

cd "$MODULE_DIR"

log "$NAME: baseline benchmark ($TRIALS trials × $BENCH_TIME)..."
baseline_results=()
for i in $(seq 1 "$TRIALS"); do
  result=$(go test -bench=. -benchtime="$BENCH_TIME" -count=3 . 2>&1 | grep -E '^Bench' | awk '{print $3}' | head -1)
  baseline_results+=("$result")
  log "  baseline trial $i: $result ns/op"
done

log "$NAME: building with PGO profile..."
cp "$PROFILE" default.pgo
trap 'rm -f default.pgo' EXIT

log "$NAME: PGO benchmark ($TRIALS trials × $BENCH_TIME)..."
pgo_results=()
for i in $(seq 1 "$TRIALS"); do
  result=$(go test -bench=. -benchtime="$BENCH_TIME" -count=3 -pgo=default.pgo . 2>&1 | grep -E '^Bench' | awk '{print $3}' | head -1)
  pgo_results+=("$result")
  log "  pgo trial $i: $result ns/op"
done

# compute mean ns/op
mean() {
  local sum=0 count=0
  for v in "$@"; do
    [[ -n "$v" ]] || continue
    sum=$((sum + v))
    count=$((count + 1))
  done
  [[ $count -gt 0 ]] && echo $((sum / count)) || echo 0
}

# compute coefficient of variation (stddev/mean * 100) as a string "X.X%"
cv_pct() {
  local m="$1"; shift
  local values=("$@")
  local count=${#values[@]}
  [[ $count -lt 2 || $m -eq 0 ]] && echo "N/A" && return
  printf '%s\n' "${values[@]}" | awk -v m="$m" -v n="$count" '
    { sum += ($1 - m)^2 }
    END { printf "%.1f%%", (sqrt(sum/(n-1)) / m) * 100 }
  '
}

baseline_mean=$(mean "${baseline_results[@]}")
pgo_mean=$(mean "${pgo_results[@]}")
baseline_cv=$(cv_pct "$baseline_mean" "${baseline_results[@]}")
pgo_cv=$(cv_pct "$pgo_mean" "${pgo_results[@]}")

if [[ $baseline_mean -gt 0 && $pgo_mean -gt 0 ]]; then
  # delta% = (pgo - baseline) / baseline * 100  (negative = improvement)
  delta_pct=$(awk "BEGIN { printf \"%.1f\", ($pgo_mean - $baseline_mean) / $baseline_mean * 100 }")
else
  delta_pct="N/A"
fi

log "$NAME: baseline_mean=$baseline_mean ns/op (CV=$baseline_cv)  pgo_mean=$pgo_mean ns/op (CV=$pgo_cv)  delta=$delta_pct%"

cat > "$OUTDIR/${NAME}-cycle.json" << EOF
{
  "repo": "$NAME",
  "profile": "$PROFILE",
  "baseline_trials_nsop": $(printf '%s\n' "${baseline_results[@]}" | jq -R . | jq -s .),
  "pgo_trials_nsop": $(printf '%s\n' "${pgo_results[@]}" | jq -R . | jq -s .),
  "baseline_mean_nsop": $baseline_mean,
  "pgo_mean_nsop": $pgo_mean,
  "baseline_cv": "$baseline_cv",
  "pgo_cv": "$pgo_cv",
  "delta_pct": "$delta_pct"
}
EOF

echo "$OUTDIR/${NAME}-cycle.json"
