package collect

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

// deriveTimeout returns opts.Timeout if set, else window + buffer (opts.TimeoutBuffer, default 30s), floor 120s.
func deriveTimeout(opts Options) time.Duration {
	if opts.Timeout > 0 {
		return opts.Timeout
	}
	buffer := opts.TimeoutBuffer
	if buffer <= 0 {
		buffer = 30 * time.Second
	}
	t := opts.Window + buffer
	if t < 120*time.Second {
		t = 120 * time.Second
	}
	return t
}

// FromPprof fetches a CPU profile from a standard Go pprof HTTP endpoint.
// opts.URL must be the full URL including query params (e.g. /debug/pprof/profile?seconds=30).
func FromPprof(opts Options) (*Result, error) {
	timeout := deriveTimeout(opts)

	rawURL := opts.URL
	if opts.Window > 0 {
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("collect pprof: parse url: %w", err)
		}
		q := u.Query()
		q.Set("seconds", fmt.Sprintf("%d", int(math.Round(opts.Window.Seconds()))))
		u.RawQuery = q.Encode()
		rawURL = u.String()
	}

	client := &http.Client{Timeout: timeout}
	start := time.Now()
	resp, err := client.Get(rawURL) //nolint:gosec // URL is caller-supplied by design (--url flag)
	if err != nil {
		return nil, fmt.Errorf("collect pprof: get %s: %w", opts.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("collect pprof: server returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("collect pprof: read response: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("collect pprof: endpoint returned empty profile")
	}
	return &Result{
		Bytes:     data,
		Source:    SourcePprof,
		Start:     start,
		End:       time.Now(),
		SizeBytes: len(data),
	}, nil
}
