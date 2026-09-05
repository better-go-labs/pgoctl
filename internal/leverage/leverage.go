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

	"github.com/google/pprof/profile"
)

// Verdict classifies the PGO leverage result for a build target.
type Verdict string

const (
	// VerdictLeverageFound indicates PGO-specific compiler decisions were detected.
	VerdictLeverageFound Verdict = "LEVERAGE_FOUND"
	// VerdictNoLeverage indicates zero PGO-specific decisions; optimization unlikely to help.
	VerdictNoLeverage Verdict = "NO_LEVERAGE"
	// VerdictProfileOnly indicates analysis was profile-only (no build was run).
	VerdictProfileOnly Verdict = "PROFILE_ONLY"
)

// FunctionEntry holds the name and CPU share for a hot function.
type FunctionEntry struct {
	Function string  `json:"function"`
	Package  string  `json:"package"`
	FlatPct  float64 `json:"flat_pct"`
}

// BuildAnalysis holds the raw counts from comparing a PGO build against a baseline build.
type BuildAnalysis struct {
	DevirtDecisions int `json:"devirt_decisions"`
	PGOExtraInlines int `json:"pgo_extra_inlines"`
	BaselineInlines int `json:"baseline_inlines"`
	PGOInlines      int `json:"pgo_inlines"`
}

// Report is the output of CheckFile: a structured summary of PGO leverage for a build target.
type Report struct {
	ProfilePath   string          `json:"profile_path"`
	TotalSamples  int64           `json:"total_samples"`
	TopFunctions  []FunctionEntry `json:"top_functions"`
	HotInterfaces []string        `json:"hot_interfaces,omitempty"`
	BuildAnalysis *BuildAnalysis  `json:"build_analysis,omitempty"`
	Verdict       Verdict         `json:"verdict"`
	VerdictReason string          `json:"verdict_reason"`
}

// Options controls CheckFile behaviour.
type Options struct {
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

	if opts.TopN <= 0 {
		opts.TopN = 20
	}
	if opts.Package == "" {
		opts.Package = "./..."
	}

	idx, ok := cpuSampleIndex(p)
	total := 0.0
	counts := make(map[string]float64)

	if ok {
		for _, s := range p.Sample {
			if len(s.Location) == 0 || len(s.Value) <= idx {
				continue
			}
			v := float64(s.Value[idx])
			total += v
			loc := s.Location[0]
			if len(loc.Line) > 0 && loc.Line[0].Function != nil {
				counts[loc.Line[0].Function.Name] += v
			}
		}
	}

	entries := make([]FunctionEntry, 0, len(counts))
	for fn, c := range counts {
		pct := 0.0
		if total > 0 {
			pct = math.Round(100.0*c/total*100) / 100
		}
		entries = append(entries, FunctionEntry{
			Function: fn,
			Package:  packageFromFunction(fn),
			FlatPct:  pct,
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

	hotInterfaces := detectHotInterfaces(topEntries)

	var buildAnalysis *BuildAnalysis
	var verdict Verdict
	var verdictReason string

	if opts.Dir != "" {
		ba, err := runBuildAnalysis(opts.Dir, opts.Package, profilePath)
		if err != nil {
			return nil, fmt.Errorf("build analysis: %w", err)
		}
		buildAnalysis = ba

		if ba.DevirtDecisions > 0 || ba.PGOExtraInlines > 0 {
			verdict = VerdictLeverageFound
			parts := []string{}
			if ba.DevirtDecisions > 0 {
				parts = append(parts, fmt.Sprintf("%d devirtualization decision(s)", ba.DevirtDecisions))
			}
			if ba.PGOExtraInlines > 0 {
				parts = append(parts, fmt.Sprintf("%d extra inline(s) with PGO", ba.PGOExtraInlines))
			}
			verdictReason = fmt.Sprintf("%s found: %s", verdict, strings.Join(parts, ", "))
		} else {
			verdict = VerdictNoLeverage
			topFn := ""
			if len(topEntries) > 0 {
				topFn = fmt.Sprintf(" top hot function %s", topEntries[0].Function)
			}
			verdictReason = fmt.Sprintf("0 PGO-specific compiler decisions; PGO will not provide measurable benefit%s", topFn)
		}
	} else {
		verdict = VerdictProfileOnly
		verdictReason = "Profile analysis only (no build analysis); run with --dir to determine actual PGO benefit"
	}

	return &Report{
		ProfilePath:   profilePath,
		TotalSamples:  int64(len(p.Sample)),
		TopFunctions:  topEntries,
		HotInterfaces: hotInterfaces,
		BuildAnalysis: buildAnalysis,
		Verdict:       verdict,
		VerdictReason: verdictReason,
	}, nil
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

	devirtDecisions := countLines(pgoOutput, "devirtualizing")
	pgoInlines := countLines(pgoOutput, "inlining call")
	baselineInlines := countLines(baselineOutput, "inlining call")

	pgoExtraInlines := pgoInlines - baselineInlines
	if pgoExtraInlines < 0 {
		pgoExtraInlines = 0
	}

	return &BuildAnalysis{
		DevirtDecisions: devirtDecisions,
		PGOExtraInlines: pgoExtraInlines,
		BaselineInlines: baselineInlines,
		PGOInlines:      pgoInlines,
	}, nil
}

func runBuild(dir, pkgPattern, profilePath string, withPGO bool) (string, error) {
	args := []string{"build", "-gcflags=all=-m=2", "-o", "/dev/null"}
	if withPGO {
		args = append(args, fmt.Sprintf("-pgo=%s", profilePath))
	}
	args = append(args, pkgPattern)

	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("go build: %w\noutput: %s", err, out)
	}
	return string(out), nil
}

func countLines(output, pattern string) int {
	count := 0
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		if strings.Contains(strings.ToLower(scanner.Text()), strings.ToLower(pattern)) {
			count++
		}
	}
	return count
}

func detectHotInterfaces(entries []FunctionEntry) []string {
	var result []string
	seen := make(map[string]bool)
	for _, e := range entries {
		if strings.Contains(e.Function, ".(") && !seen[e.Function] {
			result = append(result, e.Function)
			seen[e.Function] = true
		}
	}
	return result
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

func packageFromFunction(name string) string {
	slash := strings.LastIndex(name, "/")
	start := slash + 1
	rest := name[start:]
	cut := len(rest)
	if i := strings.IndexAny(rest, "(."); i >= 0 {
		cut = i
	}
	pkg := name[:start+cut]
	if pkg == "" {
		return name
	}
	return pkg
}
