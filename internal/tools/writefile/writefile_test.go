package writefile

import (
	"os"
	"path/filepath"
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

func TestWriteFileInvalidJSON(t *testing.T) {
	// given malformed JSON args
	tool := New()

	// when called
	_, err := tool.Call(`{"path": "x", "content":}`)

	// then an error is returned for the model to retry
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}
