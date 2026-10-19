package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkCapturePgoctlCPU exercises validate, explain, and merge on a real pprof
// so a captured CPU profile reflects pgoctl code paths (not Prometheus).
//
// Capture (from repo root):
//
//	PGOCTL_WORKLOAD_PROFILE=testdata/prometheus_cpu.pprof \
//	  go test ./cmd/pgoctl -run='^$' -bench=BenchmarkCapturePgoctlCPU \
//	  -benchtime=15s -cpuprofile=testdata/pgoctl_cpu.pprof
func BenchmarkCapturePgoctlCPU(b *testing.B) {
	prof := os.Getenv("PGOCTL_WORKLOAD_PROFILE")
	if prof == "" {
		prof = filepath.Join("..", "..", "testdata", "prometheus_cpu.pprof")
	}
	if _, err := os.Stat(prof); err != nil {
		b.Skipf("workload profile not found (%s): %v", prof, err)
	}

	merged := filepath.Join(b.TempDir(), "merged.pgo")
	workloads := [][]string{
		{"validate", "--min-samples=1", "--min-duration=0", "--min-score=0", "--min-stack-depth=0", prof},
		{"explain", "--top=30", prof},
		{"merge", prof, "--out=" + merged},
		{"leverage-check", prof},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, args := range workloads {
			root := newRootCmd()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(args)
			_ = root.Execute()
		}
	}
}
