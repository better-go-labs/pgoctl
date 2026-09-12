package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/collect"
	"github.com/spf13/cobra"
)

func newCollectCmd() *cobra.Command {
	var source string
	var parcaAddr string
	var query string
	var url string
	var window time.Duration
	var timeout time.Duration
	var timeoutBuffer time.Duration
	var out string

	cmd := &cobra.Command{
		Use:   "collect",
		Short: "Collect a CPU pprof from a continuous profiling source",
		RunE: func(_ *cobra.Command, _ []string) error {
			if source == "" {
				return fmt.Errorf("--source is required")
			}

			opts := collect.Options{
				Source:        collect.Source(source),
				ParcaAddr:     parcaAddr,
				Query:         query,
				URL:           url,
				Window:        window,
				Timeout:       timeout,
				TimeoutBuffer: timeoutBuffer,
				Out:           out,
			}

			var result *collect.Result
			var err error

			switch collect.Source(source) {
			case collect.SourceParca:
				if query == "" {
					return fmt.Errorf("--query is required when source=parca")
				}
				result, err = collect.FromParca(opts)
			case collect.SourcePprof:
				if url == "" {
					return fmt.Errorf("--url is required when source=pprof")
				}
				result, err = collect.FromPprof(opts)
			default:
				return fmt.Errorf("unsupported source %q: supported sources are parca, pprof", source)
			}
			if err != nil {
				return err
			}

			if err := os.WriteFile(opts.Out, result.Bytes, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", opts.Out, err)
			}

			_, _ = fmt.Fprintf(os.Stderr, "collected %d bytes → %s (source=%s, window=%s)\n",
				result.SizeBytes, opts.Out, source, window)
			return nil
		},
	}

	cmd.Flags().StringVar(&source, "source", "", "profile source (required): parca | pprof")
	cmd.Flags().StringVar(&parcaAddr, "parca-addr", "http://localhost:7070", "Parca server address")
	cmd.Flags().StringVar(&query, "query", "process_cpu:cpu:nanoseconds:cpu:nanoseconds:delta", "Parca profile selector (required when source=parca)")
	cmd.Flags().StringVar(&url, "url", "", "full URL for pprof HTTP endpoint (required when source=pprof)")
	cmd.Flags().DurationVar(&window, "window", 5*time.Minute, "lookback window duration")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "HTTP request timeout (default: derived from window)")
	cmd.Flags().DurationVar(&timeoutBuffer, "timeout-buffer", 0, "buffer added to window when deriving HTTP timeout (default 30s)")
	cmd.Flags().StringVar(&out, "out", "cpu.pprof", "output file path")

	return cmd
}
