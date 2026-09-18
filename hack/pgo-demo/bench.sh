#!/usr/bin/env bash
# bench.sh <binary> <url> <label>
# Measures throughput (RPS) and mean latency. Runs 5 independent trials.
set -euo pipefail

BINARY="$1"
URL="$2"
LABEL="$3"
CONCURRENCY="${4:-8}"
DURATION="${5:-20}"

cat > /tmp/benchdriver.go << 'GOEOF'
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8080/bench", "target URL")
	dur := flag.Duration("dur", 20*time.Second, "duration")
	conc := flag.Int("c", 8, "concurrency")
	flag.Parse()

	var reqs int64
	var totalNs int64

	start := time.Now()
	stop := time.After(*dur)
	stopped := make(chan struct{})
	go func() { <-stop; close(stopped) }()

	client := &http.Client{Timeout: 5 * time.Second}
	var wg sync.WaitGroup
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopped:
					return
				default:
				}
				t0 := time.Now()
				resp, err := client.Get(*url)
				if err != nil {
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				atomic.AddInt64(&totalNs, time.Since(t0).Nanoseconds())
				atomic.AddInt64(&reqs, 1)
			}
		}()
	}
	wg.Wait()

	r := atomic.LoadInt64(&reqs)
	elapsed := time.Since(start).Seconds()
	meanMs := float64(atomic.LoadInt64(&totalNs)) / float64(r) / 1e6
	fmt.Printf("rps=%.1f mean_latency_ms=%.2f requests=%d elapsed=%.1fs\n",
		float64(r)/elapsed, meanMs, r, elapsed)
}
GOEOF

echo "=== $LABEL ==="
for trial in 1 2 3 4 5; do
	# start fresh binary
	"$BINARY" &
	SVC_PID=$!
	sleep 1

	echo -n "  trial $trial: "
	cd /tmp && go run benchdriver.go -url="$URL" -dur="${DURATION}s" -c="$CONCURRENCY"

	kill $SVC_PID 2>/dev/null; wait $SVC_PID 2>/dev/null || true
	sleep 0.5
done
