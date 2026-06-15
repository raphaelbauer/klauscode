// Package tools is the action boundary for the harness. A Tool is something the
// model can invoke by name; the Registry holds the available tools and executes
// them on the model's behalf.
package tools

import "fmt"

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
