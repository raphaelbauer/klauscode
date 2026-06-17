package bash

import (
	"strings"
	"testing"
	"time"
)

func TestBashCall(t *testing.T) {
	// given a bash tool
	tool := New()

	// when a simple command runs
	got, err := tool.Call("echo hi")

	// then its stdout comes back
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if strings.TrimSpace(got) != "hi" {
		t.Errorf("Call = %q, want %q", got, "hi")
	}
}

func TestBashCombinesStdoutAndStderr(t *testing.T) {
	// given a command that writes to both streams
	tool := New()

	// when run
	got, err := tool.Call("echo out; echo err 1>&2")

	// then both streams are present in the combined output
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if !strings.Contains(got, "out") || !strings.Contains(got, "err") {
		t.Errorf("expected both streams, got %q", got)
	}
}

func TestBashNonZeroExit(t *testing.T) {
	// given a command that exits non-zero
	tool := New()

	// when run
	got, err := tool.Call("echo boom; exit 3")

	// then output plus an exit-code note is returned as a normal result
	if err != nil {
		t.Fatalf("expected nil error for non-zero exit, got %v", err)
	}
	if !strings.Contains(got, "boom") || !strings.Contains(got, "[exit code: 3]") {
		t.Errorf("expected output and exit note, got %q", got)
	}
}

func TestBashTimeout(t *testing.T) {
	// given a tool with a very short timeout
	tool := New().WithTimeout(50 * time.Millisecond)

	// when a slow command runs
	got, err := tool.Call("sleep 5")

	// then it is reported as timed out, not as a hang or error
	if err != nil {
		t.Fatalf("expected nil error for timeout, got %v", err)
	}
	if !strings.Contains(got, "timed out") {
		t.Errorf("expected timeout note, got %q", got)
	}
}
