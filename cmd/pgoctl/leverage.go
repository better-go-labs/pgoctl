package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/Better-Go-Labs/pgoctl/internal/leverage"
	"github.com/spf13/cobra"
)

const (
	formatJSON = "json"
	formatText = "text"
)

func newLeverageCheckCmd() *cobra.Command {
	var topN int
	var dir string
	var pkg string
	var format string

	cmd := &cobra.Command{
		Use:   "leverage-check [--dir DIR] [--package PKG] [--top N] [--format json|text] <profile.pprof>",
		Short: "Check whether a Go module will benefit from PGO before optimizing",
		Long: `Check if a CPU profile provides actionable PGO opportunities.

This command analyzes a pprof file and determines whether running the full
PGO optimization pipeline would provide measurable benefit. It helps avoid
"running blindly" when PGO won't help (e.g., hot functions are too large to inline).

Without --dir, only analyzes the profile statically (hot functions, interfaces).
With --dir, compiles the target package twice using 'go build -gcflags=all=-m=2':
once with '-pgo=<profile>' and once as a baseline (no PGO). The compiler's
optimization decisions are diffed to count:
  - PGO-attributed devirtualizations: hot interface.Method calls rewritten to
    direct calls by the compiler only when the PGO profile is present. Only
    sites in the module's own packages drive the verdict; std/deps sites are
    reported separately.
  - PGO-only extra inlines: functions inlined in the PGO build but not the
    baseline, indicating the profile guided the inliner beyond its defaults.
    Only sites in the module's own packages drive the verdict; std/deps sites
    are reported separately.

Requires a buildable Go module at --dir. Without --dir the verdict is INCOMPLETE.

Verdict taxonomy:
  HIGH        5+ devirt decisions or 30+ extra inlines: run a full benchmark
  LOW         1-4 devirt decisions or 5-29 extra inlines: benchmark, gains likely modest
  NONE        0 devirt decisions and fewer than 5 extra inlines: no codegen lever,
              skip the benchmark (exit 3)
  INCOMPLETE  no --dir given: rerun with --dir for build analysis

Exit codes: 0 = HIGH/LOW/INCOMPLETE, 3 = NONE (CI gate), 2 = usage/parse error, 1 = error`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if topN < 0 {
				err := fmt.Errorf("--top must be >= 0, got %d", topN)
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				return &exitError{2, err}
			}
			opts := leverage.Options{
				TopN:    topN,
				Dir:     dir,
				Package: pkg,
			}

			rpt, err := leverage.CheckFile(args[0], opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				return &exitError{1, err}
			}

			if format == formatJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(rpt); err != nil {
					return &exitError{1, err}
				}
			} else {
				printLeverageReport(rpt)
			}

			// NONE exits 3 regardless of format so CI gates work with --format json.
			if rpt.Verdict == leverage.VerdictNone {
				return &exitError{code: 3}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&topN, "top", 20, "number of top functions to show (0 = all)")
	cmd.Flags().StringVar(&dir, "dir", "", "directory of the Go module to build-analyze (optional)")
	cmd.Flags().StringVar(&pkg, "package", "./...", "package pattern to build")
	cmd.Flags().StringVar(&format, "format", formatText, "output format: text|json")
	return cmd
}

func printLeverageReport(rpt *leverage.Report) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	_, _ = fmt.Fprintf(w, "verdict\t%s\n", rpt.Verdict)
	_, _ = fmt.Fprintf(w, "total_samples\t%d\n", rpt.TotalSamples)

	if rpt.BuildAnalysis != nil {
		_, _ = fmt.Fprintf(w, "devirt_decisions\t%d\n", rpt.BuildAnalysis.DevirtDecisions)
		if rpt.BuildAnalysis.PGODevirtsDeps > 0 {
			_, _ = fmt.Fprintf(w, "pgo_devirts_deps\t%d\n", rpt.BuildAnalysis.PGODevirtsDeps)
		}
		_, _ = fmt.Fprintf(w, "pgo_extra_inlines\t%d\n", rpt.BuildAnalysis.PGOExtraInlines)
		if rpt.BuildAnalysis.PGOExtraInlinesDeps > 0 {
			_, _ = fmt.Fprintf(w, "pgo_extra_inlines_deps\t%d\n", rpt.BuildAnalysis.PGOExtraInlinesDeps)
		}
	}

	_ = w.Flush()

	_, _ = fmt.Fprintln(os.Stdout)
	_, _ = fmt.Fprintln(os.Stdout, rpt.VerdictReason)

	if len(rpt.TopFunctions) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "── Top functions by flat CPU share ──")
		w2 := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintf(w2, "rank\tfunction\tflat%%\n")
		for i, f := range rpt.TopFunctions {
			_, _ = fmt.Fprintf(w2, "%d\t%s\t%.2f\n", i+1, f.Function, f.FlatPct)
		}
		_ = w2.Flush()
	}

	if rpt.BuildAnalysis != nil && len(rpt.BuildAnalysis.DevirtSites) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "── PGO devirtualized call sites ──")
		for _, site := range rpt.BuildAnalysis.DevirtSites {
			_, _ = fmt.Fprintln(os.Stdout, site)
		}
	}
}
