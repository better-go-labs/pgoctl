package ctrl

import "testing"

func BenchmarkControl(b *testing.B) {
	d := SumCalc{K: 1.5}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = HotLoop(d, 5000)
	}
}
