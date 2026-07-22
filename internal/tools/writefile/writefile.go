// Package writefile provides the write_file tool. WriteFileTool implements
// tools.Tool by structural typing, so this package does not import tools.
package writefile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"klauscode/internal/tools/textutil"
)

// WriteFileTool creates or overwrites a file with content supplied by the model.
type WriteFileTool struct{}

// New returns a ready-to-register write_file tool.
func New() *WriteFileTool { return &WriteFileTool{} }

func (t *WriteFileTool) Name() string { return "write_file" }

func (t *WriteFileTool) Description() string {
	return `write_file({"path": str, "content": str}): Create or overwrite a file. Single-line JSON; escape newlines in content as \n.`
}

// Parameters is the JSON Schema for native function-calling. It has more than one
// property, so the registry passes the arguments JSON straight to Call, which
// decodes it into writeArgs — the same path used on the text side.
func (t *WriteFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"The path of the file to create or overwrite"},"content":{"type":"string","description":"The full file content to write"}},"required":["path","content"]}`)
}

// writeArgs is the JSON shape the model passes inside the parentheses.
type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Call decodes the JSON object argument and writes content to path, creating
// parent directories as needed. The JSON may span multiple lines or be fenced
// (textutil.DecodeJSONArgs normalizes both); newlines inside content are escaped
// as \n by the model and unescaped on decode.
func (t *WriteFileTool) Call(args string) (string, error) {
	var a writeArgs
	if err := textutil.DecodeJSONArgs(args, &a); err != nil {
		return "", fmt.Errorf(`write_file: invalid JSON args, expected {"path": str, "content": str} on the Action line (escape newlines in content as \n): %w`, err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("write_file: path is required")
	}
	if dir := filepath.Dir(a.Path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("write_file: %w", err)
		}
	}
	if err := os.WriteFile(a.Path, []byte(a.Content), 0o644); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path), nil
}
