// Package tools is the action boundary for the harness. A Tool is something the
// model can invoke by name; the Registry holds the available tools and executes
// them on the model's behalf.
package tools

import (
	"encoding/json"
	"fmt"
)

// Tool is a single capability the model can call.
type Tool interface {
	// Name is the identifier the model uses in an Action line.
	Name() string
	// Description is a one-line summary rendered into the system prompt.
	Description() string
	// Call runs the tool. args is the raw text the model wrote inside the
	// parentheses of the Action line.
	Call(args string) (string, error)
}

// Schematic is an OPTIONAL interface a Tool may also implement to expose a JSON
// Schema for its arguments, enabling native function-calling. It is kept separate
// from Tool so tool packages can implement it without importing this package
// (json.RawMessage is stdlib) — the same structural-typing property Tool relies
// on. A tool that does not implement it can still be called on the text path but
// is not offered as a native function.
type Schematic interface {
	// Parameters returns a JSON Schema "object" describing the tool's arguments.
	Parameters() json.RawMessage
}

// Registry holds the tools available to an agent.
type Registry struct {
	tools map[string]Tool
	order []string // preserves registration order for stable prompt rendering
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool. A later registration with the same name replaces the
// earlier one but keeps its position.
func (r *Registry) Register(t Tool) {
	if _, exists := r.tools[t.Name()]; !exists {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
}

// Execute runs the named tool with the given args. It returns an error if no
// tool is registered under that name.
func (r *Registry) Execute(name, args string) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	return t.Call(args)
}

// List returns the registered tools in registration order. Used to render the
// system prompt.
func (r *Registry) List() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

// Schema returns the named tool's JSON Schema for native function-calling, if the
// tool implements Schematic. ok is false for an unknown tool or one without a
// schema (such a tool is usable on the text path but not offered natively).
func (r *Registry) Schema(name string) (json.RawMessage, bool) {
	t, ok := r.tools[name]
	if !ok {
		return nil, false
	}
	s, ok := t.(Schematic)
	if !ok {
		return nil, false
	}
	return s.Parameters(), true
}

// MapToolCallArgs converts a native tool call's JSON arguments string into the
// single string the tool's Call expects, bridging structured function-calling to
// the raw-string Call contract without changing any tool.
//
//   - If the tool's schema declares exactly ONE property, the value of that
//     property is returned (a JSON string value is unquoted). So single-arg tools
//     like calculate/bash receive their raw value, exactly as on the text path.
//   - Otherwise (multiple properties, or no schema) the arguments JSON is returned
//     unchanged, so multi-arg tools like write_file decode it via
//     textutil.DecodeJSONArgs just as they already do.
//
// On any parse failure it returns the raw arguments so the tool surfaces its own
// self-correcting decode error rather than the registry swallowing it.
func (r *Registry) MapToolCallArgs(name, argumentsJSON string) string {
	schema, ok := r.Schema(name)
	if !ok {
		return argumentsJSON
	}
	prop, single := singleProperty(schema)
	if !single {
		return argumentsJSON
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(argumentsJSON), &obj); err != nil {
		return argumentsJSON
	}
	raw, ok := obj[prop]
	if !ok {
		return argumentsJSON
	}
	// A JSON string value (the common case) is unquoted to the bare value; a
	// non-string value keeps its JSON form.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// singleProperty reports the sole property name of a JSON Schema object, or
// single=false when the schema does not declare exactly one property.
func singleProperty(schema json.RawMessage) (name string, single bool) {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		return "", false
	}
	if len(s.Properties) != 1 {
		return "", false
	}
	for k := range s.Properties {
		return k, true
	}
	return "", false
}
