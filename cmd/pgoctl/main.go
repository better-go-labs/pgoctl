package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/compare"
	"github.com/Better-Go-Labs/pgoctl/internal/merge"
	profiletypes "github.com/Better-Go-Labs/pgoctl/internal/profile"
	"github.com/Better-Go-Labs/pgoctl/internal/validate"
	"github.com/google/pprof/profile"
	"github.com/spf13/cobra"
)

const version = "0.0.1-wip"

var jsonOutput bool

func main() {
	root := &cobra.Command{
		Use:           "pgoctl",
		Short:         "PGO profile management for Go applications",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "JSON output")
	root.AddCommand(newValidateCmd())
	root.AddCommand(newMergeCmd())
	root.AddCommand(newCompareCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
}

func newValidateCmd() *cobra.Command {
	var minSamples int64
	var minDuration float64
	var minScore float64
	var targetSamples int64
	var targetDuration float64
	var minStackDepth float64
	var weightDensity, weightRichness, weightCoverage, weightDepth, richnessFactor float64
	var minPackageShare []string

	// validateConfigFlags are the flags resolved from env/config (precedence:
	// CLI > env > config file > default). json is the root persistent flag.
	validateConfigFlags := []string{
		"min-samples", "min-duration", "min-score",
		"target-samples", "target-duration", "min-stack-depth",
		"weight-density", "weight-richness", "weight-coverage", "weight-depth",
		"richness-factor", "min-package-share", "json",
	}

	cmd := &cobra.Command{
		Use:   "validate <path>",
		Short: "Score a CPU pprof for quality before merging",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := newViper(cmd)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(2)
			}
			cfg, err := resolveConfig(cmd, v, validateConfigFlags)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(2)
			}
			// Apply env/config values to flags not set explicitly on the CLI.
			var configErrs []string
			for name, val := range cfg {
				applyErr := func() error {
					switch name {
					case "json":
						b, err := strconv.ParseBool(val)
						if err != nil {
							return err
						}
						jsonOutput = b
					case "min-package-share":
						// valueFrom joins YAML lists with commas; the existing
						// parser already handles comma-separated entries.
						minPackageShare = []string{val}
					case "min-samples", "target-samples":
						n, err := strconv.ParseInt(val, 10, 64)
						if err != nil {
							return err
						}
						if name == "min-samples" {
							minSamples = n
						} else {
							targetSamples = n
						}
					default: // float64 flags
						f, err := strconv.ParseFloat(val, 64)
						if err != nil {
							return err
						}
						switch name {
						case "min-duration":
							minDuration = f
						case "min-score":
							minScore = f
						case "target-duration":
							targetDuration = f
						case "min-stack-depth":
							minStackDepth = f
						case "weight-density":
							weightDensity = f
						case "weight-richness":
							weightRichness = f
						case "weight-coverage":
							weightCoverage = f
						case "weight-depth":
							weightDepth = f
						case "richness-factor":
							richnessFactor = f
						}
					}
					return nil
				}()
				if applyErr != nil {
					configErrs = append(configErrs, fmt.Sprintf("%s: %q", name, val))
				}
			}
			if len(configErrs) > 0 {
				fmt.Fprintf(os.Stderr, "error: invalid env/config value for %s\n",
					strings.Join(configErrs, ", "))
				os.Exit(2)
			}

			gates, err := validate.ParsePackageShareGates(minPackageShare)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(2)
			}
			opts := validate.Options{
				MinSamples:         minSamples,
				MinDurationSeconds: minDuration,
				MinScore:           minScore,
				TargetSamples:      targetSamples,
				TargetDuration:     targetDuration,
				MinStackDepth:      minStackDepth,
				WeightDensity:      weightDensity,
				WeightRichness:     weightRichness,
				WeightCoverage:     weightCoverage,
				WeightDepth:        weightDepth,
				RichnessFactor:     richnessFactor,
				PackageShareGates:  gates,
			}
			report, err := validate.ValidateFile(args[0], opts)
			if err != nil {
				if jsonOutput {
					json.NewEncoder(os.Stderr).Encode(map[string]string{"error": err.Error()})
				} else {
					fmt.Fprintf(os.Stderr, "error: %s\n", err)
				}
				os.Exit(2)
			}

			printQualityReport(report, jsonOutput)

			if !report.Valid {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().Int64Var(&minSamples, "min-samples", 10000, "minimum sample count")
	cmd.Flags().Float64Var(&minDuration, "min-duration", 10.0, "minimum duration in seconds")
	cmd.Flags().Float64Var(&minScore, "min-score", 0.6, "minimum quality score (0.0-1.0)")
	cmd.Flags().Int64Var(&targetSamples, "target-samples", 50000, "target sample count for quality scoring")
	cmd.Flags().Float64Var(&targetDuration, "target-duration", 30.0, "target duration in seconds for quality scoring")
	cmd.Flags().Float64Var(&minStackDepth, "min-stack-depth", 2.0, "minimum average stack depth")
	cmd.Flags().Float64Var(&weightDensity, "weight-density", 0.40, "density score weight")
	cmd.Flags().Float64Var(&weightRichness, "weight-richness", 0.30, "richness score weight")
	cmd.Flags().Float64Var(&weightCoverage, "weight-coverage", 0.20, "coverage score weight")
	cmd.Flags().Float64Var(&weightDepth, "weight-depth", 0.10, "depth score weight")
	cmd.Flags().Float64Var(&richnessFactor, "richness-factor", 0.02, "richness scaling factor")
	cmd.Flags().StringArrayVar(&minPackageShare, "min-package-share", nil, "min combined flat CPU %% for a package prefix, e.g. github.com/prometheus/prometheus/tsdb:5 (repeatable or comma-separated; subpackages included)")
	return cmd
}

func newMergeCmd() *cobra.Command {
	var strategy string
	var recencyWeight float64
	var halfLifeH float64
	var dropInvalid bool
	var out string

	cmd := &cobra.Command{
		Use:   "merge <profile...>",
		Short: "Merge validated CPU profiles into a default.pgo artifact",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := merge.Options{
				Strategy:      merge.Strategy(strategy),
				RecencyWeight: recencyWeight,
				HalfLife:      time.Duration(halfLifeH * float64(time.Hour)),
				DropInvalid:   dropInvalid,
			}

			inputs := make([]merge.Input, len(args))
			for i, path := range args {
				data, err := os.ReadFile(path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: read %s: %s\n", path, err)
					os.Exit(2)
				}
				// Capture time comes from the profile itself (p.TimeNanos), not time.Now().
				p, err := profile.ParseData(data)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: parse %s: %s\n", path, err)
					os.Exit(2)
				}
				inputs[i] = merge.Input{Data: data, CapturedAt: time.Unix(0, p.TimeNanos)}
			}

			var buf bytes.Buffer
			if err := merge.Profiles(inputs, opts, &buf); err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
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
			if _, err := dst.Write(buf.Bytes()); err != nil {
				fmt.Fprintf(os.Stderr, "error: write: %s\n", err)
				os.Exit(2)
			}
			if out != "-" {
				fmt.Fprintf(os.Stderr, "merged %d profile(s) → %s (%d bytes)\n",
					len(args), out, buf.Len())
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&strategy, "strategy", "weighted", "merge strategy: weighted|latest|union")
	cmd.Flags().Float64Var(&recencyWeight, "recency-weight", 2.0, "multiplier for most recent profile")
	cmd.Flags().Float64Var(&halfLifeH, "half-life", 24.0, "recency decay half-life in hours")
	cmd.Flags().BoolVar(&dropInvalid, "drop-invalid", false, "skip unparseable profiles instead of failing")
	cmd.Flags().StringVar(&out, "out", "default.pgo", "output path (- for stdout)")
	return cmd
}

func newCompareCmd() *cobra.Command {
	var minImprovement float64
	var minRegression float64
	var minCPUPercent float64
	var topN int

	cmd := &cobra.Command{
		Use:   "compare <baseline.pprof> <candidate.pprof>",
		Short: "Compare two CPU profiles and emit a gate verdict",
		Long: `Compare two pprof files by CPU function attribution.

delta_pct (per function) = (base - cand) / base * 100   [relative %% change]
  base = 0 → delta_pct = -100  (new CPU cost in the candidate)
  positive = candidate uses less CPU (improvement)

SummaryCPUDelta = weighted average of relative delta_pct over functions
present in BOTH profiles (weighted by baseline share). Single-profile
functions are excluded because percentages sum to 100 on both sides, so a
baseline-share-weighted sum of percentage-point diffs cancels to ~0.

Verdict:
  promote  — SummaryCPUDelta >= --min-improvement
  rollback — SummaryCPUDelta <= -(--min-regression)  (independent threshold)
  neutral  — within both thresholds

--min-cpu-percent drops functions whose CPU share is below the given %% in
BOTH profiles before comparing (default 0 = no filtering).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			gate := compare.GateConfig{
				MinCPUImprovement: minImprovement,
				MinCPURegression:  minRegression,
				MinCPUPercent:     minCPUPercent,
				TopN:              topN,
			}
			rpt, err := compare.ProfileFiles(args[0], args[1], gate)
			if err != nil {
				if jsonOutput {
					json.NewEncoder(os.Stderr).Encode(map[string]string{"error": err.Error()})
				} else {
					fmt.Fprintf(os.Stderr, "error: %s\n", err)
				}
				os.Exit(2)
			}
			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				enc.Encode(rpt)
			} else {
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintf(w, "verdict\t%s\n", rpt.Verdict)
				fmt.Fprintf(w, "cpu_delta_pct\t%.2f\n", rpt.SummaryCPUDelta)
				fmt.Fprintf(w, "baseline_samples\t%d\n", rpt.BaselineSamples)
				fmt.Fprintf(w, "candidate_samples\t%d\n", rpt.CandidateSamples)
				if rpt.FilteredFunctions > 0 {
					fmt.Fprintf(w, "filtered_functions\t%d\n", rpt.FilteredFunctions)
				}
				if len(rpt.TopDeltas) > 0 {
					fmt.Fprintf(w, "\nfunction\tbase%%\tcand%%\tdelta%%\n")
					for _, d := range rpt.TopDeltas {
						fmt.Fprintf(w, "%s\t%.1f\t%.1f\t%+.1f\n",
							d.Function, d.BasePct, d.CandPct, d.DeltaPct)
					}
				}
				w.Flush()
			}
			if rpt.Verdict == compare.Rollback {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().Float64Var(&minImprovement, "min-improvement", 10.0, "min relative CPU %% improvement to promote")
	cmd.Flags().Float64Var(&minRegression, "min-regression", 10.0, "min relative CPU %% regression to rollback (independent of --min-improvement)")
	cmd.Flags().Float64Var(&minCPUPercent, "min-cpu-percent", 0.0, "drop functions below this CPU %% share in both profiles (0 = no filtering)")
	cmd.Flags().IntVar(&topN, "top", 10, "number of function deltas to show")
	return cmd
}

func printQualityReport(report *profiletypes.QualityReport, jsonOutput bool) {
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(report)
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "valid\t%v\n", report.Valid)
		fmt.Fprintf(w, "quality_score\t%.3f\n", report.QualityScore)
		fmt.Fprintf(w, "samples\t%d\n", report.Samples)
		fmt.Fprintf(w, "unique_stacks\t%d\n", report.UniqueStacks)
		for prefix, share := range report.PackageShares {
			fmt.Fprintf(w, "package_share\t%s\t%.2f%%\n", prefix, share)
		}
		for _, e := range report.Errors {
			fmt.Fprintf(w, "error\t%s\n", e)
		}
		for _, wn := range report.Warnings {
			fmt.Fprintf(w, "warning\t%s\n", wn)
		}
		w.Flush()
	}
}
