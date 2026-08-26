package main

import "errors"

// exitError carries a process exit code out of a RunE without calling os.Exit
// inside the command, so the tree can be exercised in-process by tests.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

// isExitError returns true and the exit code if err is an *exitError.
func isExitError(err error) (int, bool) {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code, true
	}
	return 0, false
}
