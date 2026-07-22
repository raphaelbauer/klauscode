// Package editfile provides the edit_file tool. EditFileTool implements
// tools.Tool by structural typing, so this package does not import tools.
package editfile

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"klauscode/internal/tools/textutil"
)

// EditFileTool replaces a unique snippet of text in a file. Requiring the old
// text to be unique keeps edits unambiguous and forces the model to include
// enough surrounding context.
type EditFileTool struct{}

// New returns a ready-to-register edit_file tool.
func New() *EditFileTool { return &EditFileTool{} }

func (t *EditFileTool) Name() string { return "edit_file" }

func (t *EditFileTool) Description() string {
	return `edit_file({"path": str, "old": str, "new": str}): Replace the unique occurrence of old with new in a file. Single-line JSON.`
}

// Parameters is the JSON Schema for native function-calling. It has more than one
// property, so the registry passes the arguments JSON straight to Call, which
// decodes it into editArgs — the same path used on the text side.
func (t *EditFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"The path of the file to edit"},"old":{"type":"string","description":"The exact, unique text to replace"},"new":{"type":"string","description":"The replacement text"}},"required":["path","old","new"]}`)
}

// editArgs is the JSON shape the model passes inside the parentheses.
type editArgs struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// Call decodes the JSON object argument (which may span multiple lines or be
// fenced; textutil.DecodeJSONArgs normalizes both), then replaces the single
// occurrence of old with new. It errors if old is missing or appears more than
// once so the model fixes its input rather than making an ambiguous change.
func (t *EditFileTool) Call(args string) (string, error) {
	var a editArgs
	if err := textutil.DecodeJSONArgs(args, &a); err != nil {
		return "", fmt.Errorf(`edit_file: invalid JSON args, expected {"path": str, "old": str, "new": str} on the Action line (escape newlines as \n): %w`, err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("edit_file: path is required")
	}
	if a.Old == "" {
		return "", fmt.Errorf("edit_file: old is required")
	}

	data, err := os.ReadFile(a.Path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	content := string(data)

	switch n := strings.Count(content, a.Old); {
	case n == 0:
		return "", fmt.Errorf("edit_file: old text not found in %s", a.Path)
	case n > 1:
		return "", fmt.Errorf("edit_file: old text is ambiguous (%d matches) in %s; include more surrounding context", n, a.Path)
	}

	updated := strings.Replace(content, a.Old, a.New, 1)
	if err := os.WriteFile(a.Path, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	return fmt.Sprintf("edited %s", a.Path), nil
}
