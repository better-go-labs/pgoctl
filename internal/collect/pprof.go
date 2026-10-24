package collect

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
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

// pprofDefaultSeconds is net/http/pprof's duration when the URL has no seconds.
const pprofDefaultSeconds = 30

// FromPprof fetches a CPU profile from a standard Go pprof HTTP endpoint.
// opts.URL is the full URL, optionally with ?seconds=N. An explicit window
// (opts.WindowSet) replaces seconds; otherwise the URL's seconds is kept, so a
// default window cannot silently stretch a 30s request to minutes.
func FromPprof(opts Options) (*Result, error) {
	u, err := url.Parse(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("collect pprof: parse url: %w", err)
	}
	q := u.Query()
	seconds := pprofDefaultSeconds
	switch {
	case opts.WindowSet && opts.Window > 0:
		seconds = int(math.Round(opts.Window.Seconds()))
		q.Set("seconds", fmt.Sprintf("%d", seconds))
		u.RawQuery = q.Encode()
	case q.Get("seconds") != "":
		n, err := strconv.Atoi(q.Get("seconds"))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("collect pprof: seconds=%q in --url is not a positive integer", q.Get("seconds"))
		}
		seconds = n
	}
	rawURL := u.String()

	// Derive the timeout from the duration actually requested.
	topts := opts
	topts.Window = time.Duration(seconds) * time.Second
	timeout := deriveTimeout(topts)

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
		Seconds:   seconds,
	}, nil
}
