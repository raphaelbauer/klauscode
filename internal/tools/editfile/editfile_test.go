package editfile

import (
	"os"
	"path/filepath"
	"strings"
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

func TestEditFileMultiLineAndFencedJSON(t *testing.T) {
	// given JSON args in the multi-line and fenced shapes models commonly emit
	tool := New()
	tests := []struct {
		name string
		args string
	}{
		{"multi-line", "{\n  \"path\": \"%s\",\n  \"old\": \"old\",\n  \"new\": \"new\"\n}"},
		{"json fence", "```json\n{\"path\": \"%s\", \"old\": \"old\", \"new\": \"new\"}\n```"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTemp(t, "value is old here")
			args := strings.Replace(tt.args, "%s", path, 1)

			// when called
			if _, err := tool.Call(args); err != nil {
				t.Fatalf("Call returned error: %v", err)
			}

			// then the edit is applied despite the multi-line / fenced wrapper
			data, _ := os.ReadFile(path)
			if string(data) != "value is new here" {
				t.Errorf("file = %q, want %q", string(data), "value is new here")
			}
		})
	}
}

func TestEditFileInvalidJSON(t *testing.T) {
	// given malformed JSON args
	tool := New()

	// when called
	_, err := tool.Call(`{"path": "x", "old":}`)

	// then a descriptive, self-correcting error names the expected object shape
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), `{"path": str, "old": str, "new": str}`) {
		t.Errorf("error should teach the expected shape, got %v", err)
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
