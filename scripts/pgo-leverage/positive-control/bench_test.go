package ctrl_test

import "testing"

// Dispatch — always SumCalc in practice.
// PGO devirtualizes + inlines Run(), eliminating the indirect interface
// call and enabling the compiler to optimize the tight inner loop body.
type Dispatch interface {
	Run(x float64) float64
}

type SumCalc struct{ K float64 }

func (s SumCalc) Run(x float64) float64 {
	return x*s.K + x - x*x*0.5
}

// hotLoop is marked noinline to keep the interface dispatch site visible
// as an indirect call; without PGO the compiler cannot devirtualize it.
//
//go:noinline
func hotLoop(d Dispatch, n int) float64 {
	acc := 0.0
	for i := 0; i < n; i++ {
		acc += d.Run(float64(i) * 1e-4)
	}
	return acc
}

func BenchmarkControl(b *testing.B) {
	d := SumCalc{K: 1.5}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hotLoop(d, 5000)
	}
}
