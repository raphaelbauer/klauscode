// Package calculate provides the calculate tool and its supporting arithmetic
// evaluator. CalculateTool implements tools.Tool by structural typing, so this
// package does not need to import tools.
package calculate

import (
	"encoding/json"
	"strconv"
)

// CalculateTool evaluates mathematical expressions for the model.
type CalculateTool struct{}

// New returns a ready-to-register calculate tool.
func New() *CalculateTool { return &CalculateTool{} }

func (t *CalculateTool) Name() string { return "calculate" }

func (t *CalculateTool) Description() string {
	return "calculate(<expression>): Evaluate a math expression. Put it directly inside the parentheses, e.g. calculate((12 * 9) + 3)."
}

// Parameters is the JSON Schema for native function-calling: a single required
// string, so the registry maps a native call's argument straight to Call.
func (t *CalculateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string","description":"The arithmetic expression to evaluate, e.g. (12 * 9) + 3"}},"required":["expression"]}`)
}

// Call evaluates args as an arithmetic expression and returns the result
// formatted compactly. Evaluation errors are returned as the error value so the
// agent can surface them to the model as an observation to recover from.
func (t *CalculateTool) Call(args string) (string, error) {
	value, err := Eval(args)
	if err != nil {
		return "", err
	}
	return strconv.FormatFloat(value, 'g', -1, 64), nil
}
