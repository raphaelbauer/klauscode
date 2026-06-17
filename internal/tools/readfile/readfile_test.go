package readfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileCall(t *testing.T) {
	// given a file with known contents
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello\nworld"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	tool := New()

	// when read
	got, err := tool.Call(path)

	// then the contents come back verbatim
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if got != "hello\nworld" {
		t.Errorf("Call = %q, want %q", got, "hello\nworld")
	}
}

func TestReadFileMissing(t *testing.T) {
	// given a path that does not exist
	tool := New()

	// when read
	_, err := tool.Call(filepath.Join(t.TempDir(), "nope.txt"))

	// then an error is returned for the model to recover from
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestReadFileTruncates(t *testing.T) {
	// given a file larger than the cap
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", maxBytes+10)), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	tool := New()

	// when read
	got, err := tool.Call(path)

	// then it is truncated with a note
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("expected truncation note, got %d bytes", len(got))
	}
}
