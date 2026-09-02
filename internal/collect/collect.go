// Package collect provides functions to collect CPU profiles from continuous profiling backends.
package collect

import "time"

// Source identifies the profiling backend to collect from.
type Source string

// Supported profile source backends.
const (
	SourceParca Source = "parca"
	SourcePprof Source = "pprof"
)

// Options configures a profile collection request.
type Options struct {
	Source        Source
	ParcaAddr     string        // e.g. "http://localhost:7070"
	Query         string        // Parca selector, e.g. "process_cpu:cpu:nanoseconds:cpu:nanoseconds"
	URL           string        // full URL for pprof HTTP endpoint
	Window        time.Duration // lookback window ending at End
	End           time.Time     // wall clock end; zero means time.Now()
	Timeout       time.Duration // HTTP timeout; zero means source-specific default
	TimeoutBuffer time.Duration // buffer added to Window when deriving Timeout; zero means 30s default
	Out           string        // output file path
}

// Result holds the collected profile bytes and metadata.
type Result struct {
	Bytes     []byte
	Source    Source
	Start     time.Time
	End       time.Time
	SizeBytes int
}
