// Package leverage assesses PGO benefit for a Go module before running the full optimization pipeline.
package leverage

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Better-Go-Labs/pgoctl/internal/pprofutil"

	"github.com/google/pprof/profile"
)

// Verdict classifies the PGO leverage result for a build target.
type Verdict string

const (
	// VerdictNone indicates zero PGO-specific compiler decisions; optimization will not help.
	VerdictNone Verdict = "NONE"
	// VerdictLow indicates modest PGO leverage: 1–4 devirt decisions or 5–29 extra inlines.
	VerdictLow Verdict = "LOW"
	// VerdictHigh indicates strong PGO leverage: ≥5 devirt decisions or ≥30 extra inlines.
	VerdictHigh Verdict = "HIGH"
	// VerdictIncomplete indicates build analysis was not run (no --dir provided).
	VerdictIncomplete Verdict = "INCOMPLETE"
)

// FunctionEntry holds the name and CPU share for a hot function.
type FunctionEntry struct {
	Function string  `json:"function"`
	Package  string  `json:"package"`
	FlatPct  float64 `json:"flat_pct"`
	CumPct   float64 `json:"cum_pct"`
}

// BuildAnalysis holds the raw counts from comparing a PGO build against a baseline build.
type BuildAnalysis struct {
	DevirtDecisions     int      `json:"devirt_decisions"`
	PGOExtraInlines     int      `json:"pgo_extra_inlines"`      // module-scoped set diff (verdict-driving)
	PGOExtraInlinesDeps int      `json:"pgo_extra_inlines_deps"` // std/deps set diff (informational)
	BaselineInlines     int      `json:"baseline_inlines"`
	PGOInlines          int      `json:"pgo_inlines"`
	PGODevirts          int      `json:"pgo_devirts"`
	DevirtSites         []string `json:"devirt_sites,omitempty"`
}

// Report is the output of CheckFile: a structured summary of PGO leverage for a build target.
type Report struct {
	ProfilePath   string          `json:"profile_path"`
	TotalSamples  int64           `json:"total_samples"`
	TopFunctions  []FunctionEntry `json:"top_functions"`
	BuildAnalysis *BuildAnalysis  `json:"build_analysis,omitempty"`
	Verdict       Verdict         `json:"verdict"`
	VerdictReason string          `json:"verdict_reason"`
}

// Options controls CheckFile behaviour.
type Options struct {
	// TopN limits TopFunctions; 0 means all.
	TopN    int
	Dir     string
	Package string
}

// CheckFile parses the pprof at profilePath, identifies hot functions, and optionally
// runs a PGO build (when opts.Dir is set) to count devirtualization and extra-inline decisions.
func CheckFile(profilePath string, opts Options) (*Report, error) {
	data, err := os.ReadFile(profilePath)
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	p, err := profile.ParseData(data)
	if err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}

	if opts.Package == "" {
		opts.Package = "./..."
	}

	idx, ok := cpuSampleIndex(p)
	total := 0.0
	flatCounts := make(map[string]float64)
	cumCounts := make(map[string]float64)

	if ok {
		for _, s := range p.Sample {
			if len(s.Location) == 0 || len(s.Value) <= idx {
				continue
			}
			v := float64(s.Value[idx])
			total += v
			loc := s.Location[0]
			if len(loc.Line) > 0 && loc.Line[0].Function != nil {
				flatCounts[loc.Line[0].Function.Name] += v
			}
			seen := make(map[string]bool)
			for _, loc := range s.Location {
				for _, line := range loc.Line {
					if line.Function != nil && !seen[line.Function.Name] {
						cumCounts[line.Function.Name] += v
						seen[line.Function.Name] = true
					}
				}
			}
		}
	}

	entries := make([]FunctionEntry, 0, len(flatCounts))
	for fn, c := range flatCounts {
		flatPct := 0.0
		cumPct := 0.0
		if total > 0 {
			flatPct = math.Round(100.0*c/total*100) / 100
			cumPct = math.Round(100.0*cumCounts[fn]/total*100) / 100
		}
		entries = append(entries, FunctionEntry{
			Function: fn,
			Package:  pprofutil.PackageOf(fn),
			FlatPct:  flatPct,
			CumPct:   cumPct,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].FlatPct != entries[j].FlatPct {
			return entries[i].FlatPct > entries[j].FlatPct
		}
		return entries[i].Function < entries[j].Function
	})

	top := opts.TopN
	if top <= 0 || top > len(entries) {
		top = len(entries)
	}
	topEntries := entries[:top]

	var buildAnalysis *BuildAnalysis
	var verdict Verdict
	var verdictReason string

	if opts.Dir != "" {
		ba, err := runBuildAnalysis(opts.Dir, opts.Package, profilePath)
		if err != nil {
			return nil, fmt.Errorf("build analysis: %w", err)
		}
		buildAnalysis = ba

		verdict, verdictReason = verdictForAnalysis(ba, topEntries)
	} else {
		verdict = VerdictIncomplete
		verdictReason = "INCOMPLETE: no build analysis run; use --dir to measure PGO-specific compiler decisions"
	}

	return &Report{
		ProfilePath:   profilePath,
		TotalSamples:  pprofutil.SampleCount(p),
		TopFunctions:  topEntries,
		BuildAnalysis: buildAnalysis,
		Verdict:       verdict,
		VerdictReason: verdictReason,
	}, nil
}

// verdictForAnalysis maps build-analysis counts to a Verdict + human-readable reason.
func verdictForAnalysis(ba *BuildAnalysis, topEntries []FunctionEntry) (Verdict, string) {
	switch {
	case ba.DevirtDecisions >= 5 || ba.PGOExtraInlines >= 30:
		parts := []string{}
		if ba.DevirtDecisions > 0 {
			parts = append(parts, fmt.Sprintf("%d devirtualization decision(s)", ba.DevirtDecisions))
		}
		if ba.PGOExtraInlines > 0 {
			parts = append(parts, fmt.Sprintf("%d extra inline(s) with PGO", ba.PGOExtraInlines))
		}
		return VerdictHigh, fmt.Sprintf("HIGH: strong PGO leverage — %s; run a full benchmark cycle", strings.Join(parts, ", "))
	case ba.DevirtDecisions >= 1 || ba.PGOExtraInlines >= 5:
		parts := []string{}
		if ba.DevirtDecisions > 0 {
			parts = append(parts, fmt.Sprintf("%d devirtualization decision(s)", ba.DevirtDecisions))
		}
		if ba.PGOExtraInlines > 0 {
			parts = append(parts, fmt.Sprintf("%d extra inline(s) with PGO", ba.PGOExtraInlines))
		}
		return VerdictLow, fmt.Sprintf("LOW: marginal PGO leverage — %s; benchmark may show modest gains", strings.Join(parts, ", "))
	default:
		topFn := ""
		if len(topEntries) > 0 {
			topFn = fmt.Sprintf("; top hot function %s", topEntries[0].Function)
		}
		return VerdictNone, fmt.Sprintf("NONE: 0 PGO-specific compiler decisions — no codegen lever for this hot path%s", topFn)
	}
}

func runBuildAnalysis(dir, pkgPattern, profilePath string) (*BuildAnalysis, error) {
	absProfilePath, err := filepath.Abs(profilePath)
	if err != nil {
		return nil, fmt.Errorf("get absolute path: %w", err)
	}

	pgoOutput, err := runBuild(dir, pkgPattern, absProfilePath, true)
	if err != nil {
		return nil, fmt.Errorf("PGO build: %w", err)
	}

	baselineOutput, err := runBuild(dir, pkgPattern, "", false)
	if err != nil {
		return nil, fmt.Errorf("baseline build: %w", err)
	}

	// Count only the PGO-specific speculative devirt marker; plain "devirtualizing" lines
	// are static/type-based and appear in both builds regardless of the profile.
	devirtSites := matchingLines(pgoOutput, "pgo devirtualizing")
	pgoDevirts := len(devirtSites)

	pgoInlines := countLines(pgoOutput, "inlining call")
	baselineInlines := countLines(baselineOutput, "inlining call")

	// Set diff: new inline sites in PGO build, partitioned by origin.
	// Lines starting with "./" are the user's module; absolute paths are std/deps.
	// Using a set diff rather than a net count prevents std/runtime inlines from
	// inflating the verdict (e.g. 594 runtime inlines masking 0 module inlines).
	pgoSites := collectInlineSites(pgoOutput)
	baselineSites := collectInlineSites(baselineOutput)
	moduleExtra, depsExtra := extraInlinesByOrigin(pgoSites, baselineSites)

	return &BuildAnalysis{
		DevirtDecisions:     pgoDevirts,
		PGOExtraInlines:     moduleExtra,
		PGOExtraInlinesDeps: depsExtra,
		BaselineInlines:     baselineInlines,
		PGOInlines:          pgoInlines,
		PGODevirts:          pgoDevirts,
		DevirtSites:         devirtSites,
	}, nil
}

// buildArgs builds the go build argv. The baseline uses -pgo=off so a
// default.pgo in the module is not silently picked up.
func buildArgs(pkgPattern, profilePath string, withPGO bool) []string {
	args := []string{"build", "-gcflags=all=-m=2", "-o", os.DevNull}
	if withPGO {
		args = append(args, fmt.Sprintf("-pgo=%s", profilePath))
	} else {
		args = append(args, "-pgo=off")
	}
	return append(args, pkgPattern)
}

func runBuild(dir, pkgPattern, profilePath string, withPGO bool) (string, error) {
	args := buildArgs(pkgPattern, profilePath, withPGO)

	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("go build: %w\noutput: %s", err, out)
	}
	return string(out), nil
}

// matchingLines returns the trimmed lines of output containing pattern, case-insensitively.
func matchingLines(output, pattern string) []string {
	var lines []string
	want := strings.ToLower(pattern)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		if strings.Contains(strings.ToLower(scanner.Text()), want) {
			lines = append(lines, strings.TrimSpace(scanner.Text()))
		}
	}
	return lines
}

func countLines(output, pattern string) int {
	return len(matchingLines(output, pattern))
}

// collectInlineSites returns a set of unique "inlining call" lines from compiler output.
func collectInlineSites(output string) map[string]bool {
	sites := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(strings.ToLower(line), "inlining call") {
			sites[line] = true
		}
	}
	return sites
}

// extraInlinesByOrigin counts PGO-only inline sites (present in pgo but not baseline),
// split by origin: module (relative "./" paths) vs std/deps (absolute paths).
func extraInlinesByOrigin(pgo, baseline map[string]bool) (module, deps int) {
	for line := range pgo {
		if !baseline[line] {
			if strings.HasPrefix(line, "./") {
				module++
			} else {
				deps++
			}
		}
	}
	return
}

func cpuSampleIndex(p *profile.Profile) (int, bool) {
	preferred, fallback := -1, -1
	for i, st := range p.SampleType {
		switch {
		case st.Type == "cpu" && st.Unit == "nanoseconds":
			preferred = i
		case st.Type == "samples" && st.Unit == "count":
			fallback = i
		}
	}
	if preferred >= 0 {
		return preferred, true
	}
	if fallback >= 0 {
		return fallback, true
	}
	return -1, false
}
