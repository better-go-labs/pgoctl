package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/Better-Go-Labs/pgoctl/internal/explain"
	"github.com/spf13/cobra"
)

func newExplainCmd() *cobra.Command {
	var topN int
	var format string

	cmd := &cobra.Command{
		Use:   "explain <path>",
		Short: "Explain a CPU profile: top functions, hot packages, PGO readiness",
		Long: `Analyse a pprof file and explain what it contains in human-readable form.

Prints the top hot functions by flat CPU share, groups them by package,
and gives a plain-English verdict on whether the profile is a good PGO
baseline (mirrors the validate gate but with prose output instead of a
score).

Designed for interactive use: pipe through less or redirect to a file.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rpt, err := explain.AnalyzeFile(args[0], topN)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				return &exitError{2, err}
			}

			if format == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rpt)
			}

			printExplainReport(cmd, rpt)
			return nil
		},
	}
	cmd.Flags().IntVar(&topN, "top", 20, "number of top functions to show")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text|json")
	return cmd
}

func printExplainReport(cmd *cobra.Command, rpt *explain.Report) {
	out := cmd.OutOrStdout()
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	_, _ = fmt.Fprintf(w, "profile\t%s\n", rpt.ProfilePath)
	_, _ = fmt.Fprintf(w, "samples\t%d\n", rpt.TotalSamples)
	_, _ = fmt.Fprintf(w, "duration_sec\t%.1f\n", rpt.DurationSec)
	_, _ = fmt.Fprintf(w, "verdict\t%s\n", rpt.Verdict)
	_ = w.Flush()

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, rpt.VerdictReason)

	if len(rpt.TopFunctions) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "── Top functions by flat CPU share ──")
		w2 := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintf(w2, "rank\tfunction\tflat%%\n")
		for i, f := range rpt.TopFunctions {
			_, _ = fmt.Fprintf(w2, "%d\t%s\t%.2f\n", i+1, f.Function, f.FlatPct)
		}
		_ = w2.Flush()
	}

	if len(rpt.PackageGroups) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "── Hot packages (from top functions) ──")
		w3 := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintf(w3, "package\ttotal%%\n")
		for _, g := range rpt.PackageGroups {
			_, _ = fmt.Fprintf(w3, "%s\t%.2f\n", g.Package, g.TotalPct)
		}
		_ = w3.Flush()
	}
}
