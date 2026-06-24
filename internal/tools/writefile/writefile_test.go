package writefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileCall(t *testing.T) {
	// given a write_file tool and a target path inside a temp dir
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "out.txt")
	tool := New()

	// when called with single-line JSON whose content carries an escaped newline
	args := `{"path":"` + path + `","content":"hi\nthere"}`
	got, err := tool.Call(args)

	// then the file is created with the unescaped multi-line content
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(data) != "hi\nthere" {
		t.Errorf("file content = %q, want %q", string(data), "hi\nthere")
	}
	if got == "" {
		t.Error("expected a confirmation message")
	}
}

func TestWriteFileToleratesTrailingBytes(t *testing.T) {
	// given args with a stray character after the closing brace (a greedy-parser
	// artefact the Decoder should ignore)
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	tool := New()

	// when called
	_, err := tool.Call(`{"path":"` + path + `","content":"ok"} `)

	// then it still succeeds
	if err != nil {
		t.Fatalf("expected trailing bytes tolerated, got %v", err)
	}
}

func TestWriteFileMultiLineAndFencedJSON(t *testing.T) {
	// given JSON args in the multi-line and fenced shapes models commonly emit
	dir := t.TempDir()
	tool := New()
	tests := []struct {
		name string
		args string
	}{
		{"multi-line", "{\n  \"path\": \"%s\",\n  \"content\": \"ok\"\n}"},
		{"json fence", "```json\n{\"path\": \"%s\", \"content\": \"ok\"}\n```"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".txt")
			args := strings.Replace(tt.args, "%s", path, 1)

			// when called
			if _, err := tool.Call(args); err != nil {
				t.Fatalf("Call returned error: %v", err)
			}

			// then the file is written despite the multi-line / fenced wrapper
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading written file: %v", err)
			}
			if string(data) != "ok" {
				t.Errorf("content = %q, want %q", string(data), "ok")
			}
		})
	}
}

func TestWriteFileInvalidJSON(t *testing.T) {
	// given malformed JSON args
	tool := New()

	// when called
	_, err := tool.Call(`{"path": "x", "content":}`)

	// then a descriptive, self-correcting error names the expected object shape
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), `{"path": str, "content": str}`) {
		t.Errorf("error should teach the expected shape, got %v", err)
	}
}
