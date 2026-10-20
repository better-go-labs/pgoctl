// Package ctrl is the positive-control module for the PGO leverage probe.
// Types live in a non-test file so that go build (used by leverage-check --dir)
// can see the interface dispatch site. bench_test.go drives the benchmark that
// captures the PGO profile fed back to leverage-check.
package ctrl

// Doer is the benchmarked interface. PGO devirtualizes its sole concrete
// implementor (SumCalc) once the benchmark profile is present.
type Doer interface {
	Run(x float64) float64
}

// SumCalc is the sole Doer implementor at runtime; the indirect call through
// Doer.Run is the site PGO rewrites to a direct call.
type SumCalc struct{ K float64 }

func (s SumCalc) Run(x float64) float64 {
	return x*s.K + x - x*x*0.5
}

// HotLoop is marked noinline so the interface-call site survives in the
// baseline build; PGO devirtualizes s.Run() with a profile in place.
//
//go:noinline
func HotLoop(d Doer, n int) float64 {
	acc := 0.0
	for i := 0; i < n; i++ {
		acc += d.Run(float64(i) * 1e-4)
	}
	return acc
}

// init() is called during go build so the interface dispatch in HotLoop
// is visible to compiler analysis, allowing PGO devirtualization detection.
func init() {
	d := SumCalc{K: 1.5}
	_ = HotLoop(d, 100)
}
