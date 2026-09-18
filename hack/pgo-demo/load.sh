#!/usr/bin/env bash
# load.sh <target_url> <duration_secs> <concurrency>
# Drives load against a service using Go's net/http via a small inline driver.
set -euo pipefail

TARGET="${1:-http://localhost:8080/bench}"
DURATION="${2:-30}"
CONCURRENCY="${3:-8}"

cat > /tmp/loaddriver.go << 'GOEOF'
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
	dur := flag.Duration("dur", 30*time.Second, "duration")
	conc := flag.Int("c", 8, "concurrency")
	flag.Parse()

	var reqs, errs int64
	start := time.Now()
	var wg sync.WaitGroup
	stop := time.After(*dur)
	stopped := make(chan struct{})

	go func() {
		<-stop
		close(stopped)
	}()

	client := &http.Client{Timeout: 5 * time.Second}

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
				resp, err := client.Get(*url)
				if err != nil {
					atomic.AddInt64(&errs, 1)
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				atomic.AddInt64(&reqs, 1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start).Seconds()
	r := atomic.LoadInt64(&reqs)
	e := atomic.LoadInt64(&errs)
	fmt.Printf("requests=%d errors=%d elapsed=%.1fs rps=%.1f\n", r, e, elapsed, float64(r)/elapsed)
}
GOEOF

cd /tmp && go run loaddriver.go -url="$TARGET" -dur="${DURATION}s" -c="$CONCURRENCY"
