package main

import (
	"errors"
	"testing"
)

func TestExitError_Error(t *testing.T) {
	e := &exitError{code: 2, err: errors.New("something failed")}
	if e.Error() != "something failed" {
		t.Fatalf("want %q got %q", "something failed", e.Error())
	}
	e2 := &exitError{code: 1}
	if e2.Error() != "" {
		t.Fatalf("want empty string, got %q", e2.Error())
	}
}

func TestIsExitError(t *testing.T) {
	ee := &exitError{code: 2, err: errors.New("x")}
	code, ok := isExitError(ee)
	if !ok || code != 2 {
		t.Fatalf("want code=2 ok=true, got code=%d ok=%v", code, ok)
	}
	_, ok2 := isExitError(errors.New("plain"))
	if ok2 {
		t.Fatal("plain error should not be exitError")
	}
}
