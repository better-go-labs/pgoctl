// Package collect fetches CPU profiles from running services.
package collect

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/pprof/profile"
)

// Source identifies the profiling backend.
type Source string

const (
	SourceParca Source = "parca"
)

// Options controls a single collection run.
type Options struct {
	Source   Source
	URL      string        // base URL of the profiling service
	Duration time.Duration // how long to profile
	Timeout  time.Duration // HTTP client timeout (0 = Duration + 30s)
}

// Collect fetches a CPU profile from the configured source and returns the raw
// pprof bytes. The bytes are validated to be a parseable pprof profile before
// returning.
func Collect(ctx context.Context, opts Options) ([]byte, error) {
	switch opts.Source {
	case SourceParca:
		return collectParca(ctx, opts)
	default:
		return nil, fmt.Errorf("unknown source %q: supported sources: parca", opts.Source)
	}
}

// collectParca fetches /debug/pprof/profile from a Parca instance.
// Parca exposes a pprof-compatible HTTP endpoint at the same path as the Go
// runtime debug/pprof server, so the standard query parameter ?seconds=N is
// used to control collection duration.
func collectParca(ctx context.Context, opts Options) ([]byte, error) {
	base, err := url.Parse(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", opts.URL, err)
	}

	profileURL := base.JoinPath("/debug/pprof/profile")
	secs := int(opts.Duration.Seconds())
	if secs <= 0 {
		secs = 30
	}
	q := profileURL.Query()
	q.Set("seconds", strconv.Itoa(secs))
	profileURL.RawQuery = q.Encode()

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = opts.Duration + 30*time.Second
	}
	client := &http.Client{Timeout: timeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, profileURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", profileURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("fetch %s: HTTP %d: %s", profileURL, resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty profile response from %s", profileURL)
	}

	// Validate that the response is a parseable pprof profile before returning.
	if _, err := profile.ParseData(data); err != nil {
		return nil, fmt.Errorf("response from %s is not a valid pprof profile: %w", profileURL, err)
	}

	return data, nil
}
