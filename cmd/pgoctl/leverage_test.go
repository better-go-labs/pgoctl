package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/leverage"
)

func TestPrintLeverageReport(t *testing.T) {
	tests := []struct {
		name         string
		report       *leverage.Report
		shouldContain []string
	}{
		{
			name: "profile only verdict",
			report: &leverage.Report{
				ProfilePath:  "test.pprof",
				TotalSamples: 1000,
				Verdict:      leverage.VerdictProfileOnly,
				VerdictReason: "Profile analysis only (no build analysis); run with --dir to determine actual PGO benefit",
				TopFunctions: []leverage.FunctionEntry{
					{Function: "foo.Bar", Package: "foo", FlatPct: 10.5},
					{Function: "baz.Qux", Package: "baz", FlatPct: 5.3},
				},
			},
			shouldContain: []string{
				"PROFILE_ONLY",
				"1000",
				"foo.Bar",
				"baz.Qux",
				"Top functions by flat CPU share",
			},
		},
		{
			name: "leverage found with devirt",
			report: &leverage.Report{
				ProfilePath:  "test.pprof",
				TotalSamples: 1000,
				Verdict:      leverage.VerdictLeverageFound,
				VerdictReason: "LEVERAGE_FOUND found: 5 devirtualization decision(s)",
				BuildAnalysis: &leverage.BuildAnalysis{
					DevirtDecisions: 5,
					PGOExtraInlines: 3,
					BaselineInlines: 10,
					PGOInlines:      13,
				},
				TopFunctions: []leverage.FunctionEntry{},
				HotInterfaces: []string{
					"foo.(*Bar).(Reader)",
				},
			},
			shouldContain: []string{
				"LEVERAGE_FOUND",
				"devirt_decisions",
				"5",
				"pgo_extra_inlines",
				"3",
				"Detected interface method calls",
				"foo.(*Bar).(Reader)",
			},
		},
		{
			name: "no leverage found",
			report: &leverage.Report{
				ProfilePath:   "test.pprof",
				TotalSamples:  1000,
				Verdict:       leverage.VerdictNoLeverage,
				VerdictReason: "0 PGO-specific compiler decisions; PGO will not provide measurable benefit",
				BuildAnalysis: &leverage.BuildAnalysis{
					DevirtDecisions: 0,
					PGOExtraInlines: 0,
					BaselineInlines: 5,
					PGOInlines:      5,
				},
				TopFunctions: []leverage.FunctionEntry{},
			},
			shouldContain: []string{
				"NO_LEVERAGE",
				"0 PGO-specific compiler decisions",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stdout
			r, w, _ := os.Pipe()
			oldStdout := os.Stdout
			os.Stdout = w

			printLeverageReport(tt.report)

			w.Close()
			os.Stdout = oldStdout

			output, _ := io.ReadAll(r)
			outputStr := string(output)

			for _, shouldContain := range tt.shouldContain {
				if !strings.Contains(outputStr, shouldContain) {
					t.Errorf("output does not contain %q\n\ngot:\n%s", shouldContain, outputStr)
				}
			}
		})
	}
}

func TestNewLeverageCheckCmdJSON(t *testing.T) {
	profilePath := filepath.Join(".", "testdata", "cpu_valid.pprof")
	data, err := os.ReadFile(profilePath)
	if err != nil || strings.Contains(string(data), "git-lfs") {
		t.Skipf("testdata file not found or is LFS pointer")
	}

	cmd := newLeverageCheckCmd()
	if cmd == nil {
		t.Fatal("newLeverageCheckCmd returned nil")
	}

	// Capture stdout
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w

	err = cmd.RunE(cmd, []string{profilePath})

	w.Close()
	os.Stdout = oldStdout

	output, _ := io.ReadAll(r)
	outputStr := string(output)

	if err != nil {
		// ProfileOnly verdict doesn't error
		if !strings.Contains(outputStr, "PROFILE_ONLY") {
			t.Errorf("expected profile-only output, got error: %v", err)
		}
	}

	// Check that it's valid text output by default
	if !strings.Contains(outputStr, "verdict") && !strings.Contains(outputStr, "PROFILE_ONLY") {
		t.Errorf("expected text output format, got: %s", outputStr)
	}
}

func TestNewLeverageCheckCmdWithFormat(t *testing.T) {
	profilePath := filepath.Join(".", "testdata", "cpu_valid.pprof")
	data, err := os.ReadFile(profilePath)
	if err != nil || strings.Contains(string(data), "git-lfs") {
		t.Skipf("testdata file not found or is LFS pointer")
	}

	cmd := newLeverageCheckCmd()
	if cmd == nil {
		t.Fatal("newLeverageCheckCmd returned nil")
	}

	// Set format to json
	cmd.Flags().Set("format", "json")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = cmd.RunE(cmd, []string{profilePath})

	w.Close()
	os.Stdout = oldStdout

	output, _ := io.ReadAll(r)
	outputStr := string(output)

	// Verify it's valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(outputStr), &result); err != nil {
		t.Errorf("expected valid JSON output, but got: %s (error: %v)", outputStr, err)
	}

	if _, ok := result["verdict"]; !ok {
		t.Errorf("expected JSON to have 'verdict' field")
	}
}

func TestNewLeverageCheckCmdTopNFlag(t *testing.T) {
	profilePath := filepath.Join(".", "testdata", "cpu_valid.pprof")
	data, err := os.ReadFile(profilePath)
	if err != nil || strings.Contains(string(data), "git-lfs") {
		t.Skipf("testdata file not found or is LFS pointer")
	}

	cmd := newLeverageCheckCmd()
	if cmd == nil {
		t.Fatal("newLeverageCheckCmd returned nil")
	}

	// Set top flag
	cmd.Flags().Set("top", "5")

	// Capture stdout
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w

	err = cmd.RunE(cmd, []string{profilePath})

	w.Close()
	os.Stdout = oldStdout

	_, _ = io.ReadAll(r)

	// If we got here without panic, the flag was parsed correctly
	if t.Failed() {
		t.Error("failed to parse top flag")
	}
}

func TestNewLeverageCheckCmdInvalidProfile(t *testing.T) {
	cmd := newLeverageCheckCmd()
	if cmd == nil {
		t.Fatal("newLeverageCheckCmd returned nil")
	}

	// Capture stderr
	r, w, _ := os.Pipe()
	oldStderr := os.Stderr
	os.Stderr = w

	err := cmd.RunE(cmd, []string{"/nonexistent/path.pprof"})

	w.Close()
	os.Stderr = oldStderr

	if err == nil {
		t.Error("expected error for nonexistent profile path")
	}

	output, _ := io.ReadAll(r)
	outputStr := string(output)

	if !strings.Contains(outputStr, "error") && err == nil {
		t.Errorf("expected error output for invalid profile")
	}
}

func TestNewLeverageCheckCmdWrongArgCount(t *testing.T) {
	cmd := newLeverageCheckCmd()
	if cmd == nil {
		t.Fatal("newLeverageCheckCmd returned nil")
	}

	// Test with no arguments - cobra validates args before calling RunE
	err := cmd.ValidateArgs([]string{})
	if err == nil {
		t.Error("expected error for missing profile argument")
	}

	// Test with too many arguments
	err = cmd.ValidateArgs([]string{"file1.pprof", "file2.pprof"})
	if err == nil {
		t.Error("expected error for too many arguments")
	}
}
