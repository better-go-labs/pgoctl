package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/Better-Go-Labs/pgoctl/internal/leverage"
	"github.com/spf13/cobra"
)

func newLeverageCheckCmd() *cobra.Command {
	var topN int
	var dir string
	var pkg string
	var format string

	cmd := &cobra.Command{
		Use:   "leverage-check <profile.pprof>",
		Short: "Check whether a Go module will benefit from PGO before optimizing",
		Long: `Check if a CPU profile provides actionable PGO opportunities.

This command analyzes a pprof file and determines whether running the full
PGO optimization pipeline would provide measurable benefit. It helps avoid
"running blindly" when PGO won't help (e.g., hot functions are too large to inline).

Without --dir, only analyzes the profile statically (hot functions, interfaces).
With --dir, runs build analysis to measure PGO-specific compiler decisions:
  - Interface devirtualization (hot interface.Method calls → direct calls)
  - Extra inlining decisions (functions inlined only with PGO profile guidance)

If either is detected, PGO will likely provide benefit. If neither is found,
PGO won't measurably improve this workload.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rpt)
			}

			printLeverageReport(rpt)

			if rpt.Verdict == leverage.VerdictNoLeverage {
				return &exitError{code: 2}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&topN, "top", 20, "number of top functions to show")
	cmd.Flags().StringVar(&dir, "dir", "", "directory of the Go module to build-analyze (optional)")
	cmd.Flags().StringVar(&pkg, "package", "./...", "package pattern to build")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text|json")
	return cmd
}

func printLeverageReport(rpt *leverage.Report) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	_, _ = fmt.Fprintf(w, "verdict\t%s\n", rpt.Verdict)
	_, _ = fmt.Fprintf(w, "total_samples\t%d\n", rpt.TotalSamples)

	if rpt.BuildAnalysis != nil {
		_, _ = fmt.Fprintf(w, "devirt_decisions\t%d\n", rpt.BuildAnalysis.DevirtDecisions)
		_, _ = fmt.Fprintf(w, "pgo_extra_inlines\t%d\n", rpt.BuildAnalysis.PGOExtraInlines)
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

	if len(rpt.HotInterfaces) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "── Detected interface method calls in hot functions ──")
		for _, iface := range rpt.HotInterfaces {
			_, _ = fmt.Fprintf(os.Stdout, "%s\n", iface)
		}
	}
}
