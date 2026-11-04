package validate

import (
	"math"
	"testing"
)

func TestGrade_Boundaries(t *testing.T) {
	lo, hi := 10.0, 1000.0

	if got := grade(lo, lo, hi); got != 0 {
		t.Errorf("grade(lo,lo,hi): want 0, got %v", got)
	}
	if got := grade(hi, lo, hi); got != 1 {
		t.Errorf("grade(hi,lo,hi): want 1, got %v", got)
	}
	got := grade(100, 10, 1000)
	want := 0.5
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("grade(100,10,1000): want %v, got %v", want, got)
	}
}

func TestGrade_LogDiff(t *testing.T) {
	lo, hi := 10.0, 1000.0
	diff := grade(40, lo, hi) - grade(20, lo, hi)
	want := math.Log(2) / math.Log(hi/lo)
	if math.Abs(diff-want) > 1e-9 {
		t.Errorf("log diff: want %v, got %v", want, diff)
	}
}
