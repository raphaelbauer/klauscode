package tools

import "testing"

// stubTool is a minimal Tool used to exercise the registry without depending on
// any concrete tool implementation.
type stubTool struct {
	name string
}

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return s.name + ": stub tool." }
func (s stubTool) Call(args string) (string, error) {
	return args, nil
}

func TestRegistryExecute(t *testing.T) {
	// given a registry with a stub tool
	reg := NewRegistry()
	reg.Register(stubTool{name: "echo"})

	// when a registered tool is executed
	got, err := reg.Execute("echo", "hello")
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got != "hello" {
		t.Errorf("Execute = %q, want %q", got, "hello")
	}

	// when an unknown tool is executed then it errors
	if _, err := reg.Execute("nope", ""); err == nil {
		t.Error("expected error for unknown tool, got nil")
	}
}

func TestRegistryList(t *testing.T) {
	// given a registry with one tool
	reg := NewRegistry()
	reg.Register(stubTool{name: "echo"})

	// when listed
	list := reg.List()

	// then it contains the registered tool
	if len(list) != 1 || list[0].Name() != "echo" {
		t.Errorf("List() = %v, want one echo tool", list)
	}
}
