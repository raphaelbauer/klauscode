package editfile

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return path
}

func TestEditFileCall(t *testing.T) {
	// given a file with a unique snippet
	path := writeTemp(t, "package main\nfunc old() {}\n")
	tool := New()

	// when the snippet is replaced
	_, err := tool.Call(`{"path":"` + path + `","old":"old","new":"renamed"}`)

	// then the file reflects the edit
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "package main\nfunc renamed() {}\n" {
		t.Errorf("file = %q", string(data))
	}
}

func TestEditFileNotFound(t *testing.T) {
	// given a file that does not contain the old text
	path := writeTemp(t, "hello world")
	tool := New()

	// when an absent snippet is edited
	_, err := tool.Call(`{"path":"` + path + `","old":"missing","new":"x"}`)

	// then an error is returned
	if err == nil {
		t.Error("expected error for missing old text, got nil")
	}
}

func TestEditFileAmbiguous(t *testing.T) {
	// given a file where the old text appears more than once
	path := writeTemp(t, "a a a")
	tool := New()

	// when edited
	_, err := tool.Call(`{"path":"` + path + `","old":"a","new":"b"}`)

	// then an ambiguity error is returned and the file is untouched
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "a a a" {
		t.Errorf("file should be unchanged, got %q", string(data))
	}
}
