// baseline captures a CPU pprof profile from any net/http/pprof endpoint.
// Used in D1 to record a pre-PGO baseline from Prometheus running in kind.
//
// The capture is considered successful only if the resulting profile parses
// AND passes the sample-count gate (--min-samples). A truncated download
// (server died mid-capture, connection reset, short read) produces a thin
// profile, which here is a hard failure — never silently saved.
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

	"github.com/Better-Go-Labs/pgoctl/internal/validate"
)

func main() {
	var (
		url        = flag.String("url", "http://localhost:9090/debug/pprof/profile", "pprof CPU profile endpoint")
		seconds    = flag.Int("seconds", 30, "CPU profiling duration in seconds")
		out        = flag.String("out", "", "output file (default: testdata/cpu_<timestamp>.pprof)")
		minSamples = flag.Int("min-samples", 1000, "minimum sample count; capture fails loudly below this")
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
		// Includes connection reset / unexpected EOF: the server died
		// mid-capture. Do not save a partial profile.
		log.Fatalf("read body (capture truncated?): %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		log.Fatalf("write: %v", err)
	}

	// Gate the result before declaring success: parse + sample-count check.
	// Uses the same validation pipeline as `pgoctl validate`.
	report, err := validate.ValidateFile(*out, validate.Options{MinSamples: int64(*minSamples)})
	if err != nil {
		os.Remove(*out) // never leave a broken artifact behind
		log.Fatalf("validate captured profile: %v", err)
	}
	if len(report.Errors) > 0 {
		os.Remove(*out)
		log.Fatalf("captured profile rejected (%d bytes): %v", len(data), report.Errors)
	}

	log.Printf("saved %d bytes → %s (%d samples, %d unique stacks, score %.3f)",
		len(data), *out, report.Samples, report.UniqueStacks, report.QualityScore)
	log.Printf("next: pgoctl validate %s", *out)
}
