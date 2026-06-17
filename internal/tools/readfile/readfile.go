// Package readfile provides the read_file tool. ReadFileTool implements
// tools.Tool by structural typing, so this package does not import tools.
package readfile

import (
	"fmt"
	"os"

	"klauscode/internal/tools/textutil"
)

// maxBytes caps how much of a file is returned so a huge file cannot blow up the
// model's context. Larger files are truncated with a note.
const maxBytes = 64 * 1024

// ReadFileTool reads a file from disk for the model.
type ReadFileTool struct{}

// New returns a ready-to-register read_file tool.
func New() *ReadFileTool { return &ReadFileTool{} }

func (t *ReadFileTool) Name() string { return "read_file" }

func (t *ReadFileTool) Description() string {
	return "read_file(path: str): Read a file and return its contents."
}

// Call reads the file named by args (the raw path) and returns its contents,
// truncated to maxBytes. A missing or unreadable file is returned as an error so
// the model can self-correct.
func (t *ReadFileTool) Call(args string) (string, error) {
	path := args
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	return textutil.Truncate(string(data), maxBytes), nil
}
