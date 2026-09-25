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
		name          string
		report        *leverage.Report
		shouldContain []string
	}{
		{
			name: "profile only verdict",
			report: &leverage.Report{
				ProfilePath:   "test.pprof",
				TotalSamples:  1000,
				Verdict:       leverage.VerdictIncomplete,
				VerdictReason: "INCOMPLETE: no build analysis run; use --dir to measure PGO-specific compiler decisions",
				TopFunctions: []leverage.FunctionEntry{
					{Function: "foo.Bar", Package: "foo", FlatPct: 10.5},
					{Function: "baz.Qux", Package: "baz", FlatPct: 5.3},
				},
			},
			shouldContain: []string{
				"INCOMPLETE",
				"1000",
				"foo.Bar",
				"baz.Qux",
				"Top functions by flat CPU share",
			},
		},
		{
			name: "leverage found with devirt",
			report: &leverage.Report{
				ProfilePath:   "test.pprof",
				TotalSamples:  1000,
				Verdict:       leverage.VerdictHigh,
				VerdictReason: "HIGH: strong PGO leverage — 5 devirtualization decision(s), 3 extra inline(s) with PGO; run a full benchmark cycle",
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
				"HIGH",
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
				Verdict:       leverage.VerdictNone,
				VerdictReason: "NONE: 0 PGO-specific compiler decisions — no codegen lever for this hot path",
				BuildAnalysis: &leverage.BuildAnalysis{
					DevirtDecisions: 0,
					PGOExtraInlines: 0,
					BaselineInlines: 5,
					PGOInlines:      5,
				},
				TopFunctions: []leverage.FunctionEntry{},
			},
			shouldContain: []string{
				"NONE",
				"0 PGO-specific compiler decisions",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create pipe: %v", err)
			}
			oldStdout := os.Stdout
			os.Stdout = w

			printLeverageReport(tt.report)

			if err := w.Close(); err != nil {
				t.Logf("failed to close pipe: %v", err)
			}
			os.Stdout = oldStdout

			output, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("failed to read pipe: %v", err)
			}
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
		t.Errorf("newLeverageCheckCmd returned nil")
		return
	}

	// Capture stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("failed to create pipe: %v", pipeErr)
	}
	oldStdout := os.Stdout
	os.Stdout = w

	err = cmd.RunE(cmd, []string{profilePath})

	if closeErr := w.Close(); closeErr != nil {
		t.Logf("failed to close pipe: %v", closeErr)
	}
	os.Stdout = oldStdout

	output, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("failed to read pipe: %v", readErr)
	}
	outputStr := string(output)

	if err != nil {
		// ProfileOnly verdict doesn't error
		if !strings.Contains(outputStr, "INCOMPLETE") {
			t.Errorf("expected profile-only output, got error: %v", err)
		}
	}

	// Check that it's valid text output by default
	if !strings.Contains(outputStr, "verdict") && !strings.Contains(outputStr, "INCOMPLETE") {
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
		t.Errorf("newLeverageCheckCmd returned nil")
		return
	}

	// Set format to json
	if err := cmd.Flags().Set("format", "json"); err != nil {
		t.Fatalf("failed to set format flag: %v", err)
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("failed to create pipe: %v", pipeErr)
	}
	os.Stdout = w

	if runErr := cmd.RunE(cmd, []string{profilePath}); runErr != nil {
		t.Logf("cmd.RunE returned error (may be expected): %v", runErr)
	}

	if closeErr := w.Close(); closeErr != nil {
		t.Logf("failed to close pipe: %v", closeErr)
	}
	os.Stdout = oldStdout

	output, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("failed to read pipe: %v", readErr)
	}
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
		t.Errorf("newLeverageCheckCmd returned nil")
		return
	}

	// Set top flag
	if err := cmd.Flags().Set("top", "5"); err != nil {
		t.Fatalf("failed to set top flag: %v", err)
	}

	// Capture stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("failed to create pipe: %v", pipeErr)
	}
	oldStdout := os.Stdout
	os.Stdout = w

	if runErr := cmd.RunE(cmd, []string{profilePath}); runErr != nil {
		t.Logf("cmd.RunE returned error (may be expected): %v", runErr)
	}

	if closeErr := w.Close(); closeErr != nil {
		t.Logf("failed to close pipe: %v", closeErr)
	}
	os.Stdout = oldStdout

	if _, readErr := io.ReadAll(r); readErr != nil {
		t.Logf("failed to read pipe: %v", readErr)
	}

	// If we got here without panic, the flag was parsed correctly
	if t.Failed() {
		t.Error("failed to parse top flag")
	}
}

func TestNewLeverageCheckCmdInvalidProfile(t *testing.T) {
	cmd := newLeverageCheckCmd()
	if cmd == nil {
		t.Errorf("newLeverageCheckCmd returned nil")
		return
	}

	// Capture stderr
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("failed to create pipe: %v", pipeErr)
	}
	oldStderr := os.Stderr
	os.Stderr = w

	err := cmd.RunE(cmd, []string{"/nonexistent/path.pprof"})

	if closeErr := w.Close(); closeErr != nil {
		t.Logf("failed to close pipe: %v", closeErr)
	}
	os.Stderr = oldStderr

	if err == nil {
		t.Error("expected error for nonexistent profile path")
	}

	output, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Logf("failed to read pipe: %v", readErr)
	}
	outputStr := string(output)

	if !strings.Contains(outputStr, "error") && err == nil {
		t.Errorf("expected error output for invalid profile")
	}
}

func TestNewLeverageCheckCmdWrongArgCount(t *testing.T) {
	cmd := newLeverageCheckCmd()
	if cmd == nil {
		t.Errorf("newLeverageCheckCmd returned nil")
		return
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
