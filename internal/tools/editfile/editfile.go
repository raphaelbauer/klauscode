// Package editfile provides the edit_file tool. EditFileTool implements
// tools.Tool by structural typing, so this package does not import tools.
package editfile

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

// editArgs is the JSON shape the model passes inside the parentheses.
type editArgs struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// Call decodes single-line JSON, then replaces the single occurrence of old with
// new. It errors if old is missing or appears more than once so the model fixes
// its input rather than making an ambiguous change.
func (t *EditFileTool) Call(args string) (string, error) {
	var a editArgs
	if err := json.NewDecoder(strings.NewReader(args)).Decode(&a); err != nil {
		return "", fmt.Errorf("edit_file: invalid JSON args: %w", err)
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
