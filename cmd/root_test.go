package cmd

import (
	"errors"
	"testing"
)

func TestExitCode(t *testing.T) {
	if got := ExitCode(ExitError{Code: 130, Err: errors.New("cancelled")}); got != 130 {
		t.Fatalf("ExitCode() = %d, want 130", got)
	}
	if got := ExitCode(errors.New("failed")); got != 1 {
		t.Fatalf("ExitCode() = %d, want 1", got)
	}
}
