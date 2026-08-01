// baseline captures a CPU pprof profile from any net/http/pprof endpoint.
// Used in D1 to record a pre-PGO baseline from Prometheus running in kind.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	var (
		url     = flag.String("url", "http://localhost:9090/debug/pprof/profile", "pprof CPU profile endpoint")
		seconds = flag.Int("seconds", 30, "CPU profiling duration in seconds")
		out     = flag.String("out", "", "output file (default: testdata/cpu_<timestamp>.pprof)")
	)
	flag.Parse()

	if *out == "" {
		ts := time.Now().UTC().Format("20060102_150405")
		*out = filepath.Join("testdata", fmt.Sprintf("cpu_%s.pprof", ts))
	}

	endpoint := fmt.Sprintf("%s?seconds=%d", *url, *seconds)
	log.Printf("collecting %ds CPU profile: %s", *seconds, endpoint)

	client := &http.Client{Timeout: time.Duration(*seconds+30) * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		log.Fatalf("fetch: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Fatalf("unexpected status %s: %s", resp.Status, body)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("read body: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		log.Fatalf("write: %v", err)
	}

	log.Printf("saved %d bytes → %s", len(data), *out)
	log.Printf("next: pgoctl validate %s", *out)
}
