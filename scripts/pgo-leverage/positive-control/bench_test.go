package ctrl

import (
	"math"
	"testing"
)

func TestSumCalcRun(t *testing.T) {
	// Run accumulates the per-iteration term (x*K + x - 0.5*x*x) over 64
	// iterations, so each expected value is 64x the single-application result.
	tests := []struct {
		k, x, want float64
	}{
		{k: 1.5, x: 0.0, want: 0.0},
		{k: 1.5, x: 1.0, want: 64 * (1.5 + 1.0 - 0.5)},
		{k: 2.0, x: 2.0, want: 64 * (2.0*2.0 + 2.0 - 2.0*2.0*0.5)},
	}
	for _, tt := range tests {
		s := SumCalc{K: tt.k}
		got := s.Run(tt.x)
		if math.Abs(got-tt.want) > 1e-12 {
			t.Errorf("SumCalc{K:%v}.Run(%v) = %v, want %v", tt.k, tt.x, got, tt.want)
		}
	}
}

func TestHotLoop_ZeroIterations(t *testing.T) {
	d := SumCalc{K: 1.5}
	got := HotLoop(d, 0)
	if got != 0.0 {
		t.Errorf("HotLoop(d, 0) = %v, want 0.0", got)
	}
}

func TestHotLoop_AccumulatesResults(t *testing.T) {
	d := SumCalc{K: 1.0}
	got := HotLoop(d, 10)
	if got <= 0 {
		t.Errorf("HotLoop(d, 10) = %v, want positive result", got)
	}
}

func TestSumCalcImplementsDoer(t *testing.T) {
	var _ Doer = SumCalc{K: 1.0}
}

func BenchmarkControl(b *testing.B) {
	d := SumCalc{K: 1.5}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = HotLoop(d, 5000)
	}
}
