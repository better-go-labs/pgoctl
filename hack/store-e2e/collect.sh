#!/usr/bin/env bash
# Collect a short CPU profile from a local pprof server into $2 using the
# pgoctl binary at $1. Used by the store e2e jobs to get a real profile to
# publish without depending on any committed fixture.
set -euo pipefail

PGOCTL="${1:?usage: collect.sh <pgoctl-bin> <out>}"
OUT="${2:?usage: collect.sh <pgoctl-bin> <out>}"

cat > /tmp/pgoctl-burn.go <<'EOF'
package main

import (
	"net/http"
	_ "net/http/pprof"
	"runtime"
)

func main() {
	for i := 0; i < runtime.NumCPU(); i++ {
		go func() {
			x := uint64(0)
			for {
				x++
			}
		}()
	}
	_ = http.ListenAndServe("127.0.0.1:6060", nil)
}
EOF

go run /tmp/pgoctl-burn.go &
for i in $(seq 1 15); do
	if curl -sf http://127.0.0.1:6060/debug/pprof/ >/dev/null 2>&1; then
		break
	fi
	if [ "$i" -eq 15 ]; then
		echo "pprof server did not become ready" >&2
		exit 1
	fi
	sleep 1
done

"$PGOCTL" collect \
	--source=pprof \
	--url=http://127.0.0.1:6060/debug/pprof/profile \
	--window=5s \
	--out="$OUT"
test -s "$OUT"
