package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/collect"
	"github.com/spf13/cobra"
)

func newCollectCmd() *cobra.Command {
	var source string
	var url string
	var durationSec int
	var out string

	cmd := &cobra.Command{
		Use:   "collect",
		Short: "Collect a CPU profile from a running service",
		Long: `Fetch a CPU profile from a running service and write it as a pprof file.

Supported sources:
  parca   Fetches /debug/pprof/profile from a Parca instance (pprof-compatible
          HTTP endpoint). The profile is validated as a parseable pprof file
          before being written.

Example:
  pgoctl collect --source=parca --url=http://parca:7070 --duration=30 --out=cpu.pprof
  pgoctl validate cpu.pprof`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if url == "" {
				fmt.Fprintln(os.Stderr, "error: --url is required")
				os.Exit(2)
			}

			opts := collect.Options{
				Source:   collect.Source(source),
				URL:      url,
				Duration: time.Duration(durationSec) * time.Second,
			}

			fmt.Fprintf(os.Stderr, "collecting %ds CPU profile from %s (source: %s)...\n",
				durationSec, url, source)

			data, err := collect.Collect(context.Background(), opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(2)
			}

			var dst *os.File
			if out == "-" {
				dst = os.Stdout
			} else {
				f, err := os.Create(out)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: create %s: %s\n", out, err)
					os.Exit(2)
				}
				defer f.Close()
				dst = f
			}

			if _, err := dst.Write(data); err != nil {
				fmt.Fprintf(os.Stderr, "error: write: %s\n", err)
				os.Exit(2)
			}

			if out != "-" {
				fmt.Fprintf(os.Stderr, "wrote %d bytes → %s\n", len(data), out)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&source, "source", "parca", "profiling source: parca")
	cmd.Flags().StringVar(&url, "url", "", "base URL of the profiling service (required)")
	cmd.Flags().IntVar(&durationSec, "duration", 30, "collection duration in seconds")
	cmd.Flags().StringVar(&out, "out", "cpu.pprof", "output path (- for stdout)")
	return cmd
}
