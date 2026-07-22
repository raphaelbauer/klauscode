package tools

import (
	"encoding/json"
	"testing"
)

// stubTool is a minimal Tool used to exercise the registry without depending on
// any concrete tool implementation. It deliberately does NOT implement Schematic,
// so it also stands in for a tool with no native schema.
type stubTool struct {
	name string
}

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return s.name + ": stub tool." }
func (s stubTool) Call(args string) (string, error) {
	return args, nil
}

// schematicStub is a Tool that also implements Schematic, for native-path tests.
type schematicStub struct {
	name   string
	schema string
}

func (s schematicStub) Name() string                     { return s.name }
func (s schematicStub) Description() string              { return s.name + ": schematic stub." }
func (s schematicStub) Call(args string) (string, error) { return args, nil }
func (s schematicStub) Parameters() json.RawMessage      { return json.RawMessage(s.schema) }

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

func TestRegistrySchema(t *testing.T) {
	// given a schematic tool and a plain tool
	reg := NewRegistry()
	reg.Register(schematicStub{name: "calc", schema: `{"type":"object","properties":{"expression":{"type":"string"}}}`})
	reg.Register(stubTool{name: "plain"})

	// when Schema is queried
	if _, ok := reg.Schema("calc"); !ok {
		t.Error("Schema(calc) ok = false, want true")
	}
	// then a tool without Schematic reports no schema
	if _, ok := reg.Schema("plain"); ok {
		t.Error("Schema(plain) ok = true, want false")
	}
	// and an unknown tool reports no schema
	if _, ok := reg.Schema("nope"); ok {
		t.Error("Schema(nope) ok = true, want false")
	}
}

func TestRegistryMapToolCallArgs(t *testing.T) {
	// given a single-property tool, a multi-property tool, and a plain tool
	reg := NewRegistry()
	reg.Register(schematicStub{name: "calc", schema: `{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"]}`})
	reg.Register(schematicStub{name: "write", schema: `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}`})
	reg.Register(stubTool{name: "plain"})

	tests := []struct {
		name string
		tool string
		args string
		want string
	}{
		// a single-property schema unwraps to the bare string value
		{"single property unwrapped", "calc", `{"expression":"(12 * 9) + 3"}`, "(12 * 9) + 3"},
		// a multi-property schema is passed through unchanged for the tool to decode
		{"multi property passthrough", "write", `{"path":"a.txt","content":"hi"}`, `{"path":"a.txt","content":"hi"}`},
		// a tool without a schema is passed through unchanged
		{"no schema passthrough", "plain", `{"anything":"here"}`, `{"anything":"here"}`},
		// malformed JSON for a single-prop tool falls through so the tool errors
		{"malformed falls through", "calc", `{not json`, `{not json`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// when the native arguments are mapped
			got := reg.MapToolCallArgs(tt.tool, tt.args)
			// then the expected Call argument is produced
			if got != tt.want {
				t.Errorf("MapToolCallArgs(%q, %q) = %q, want %q", tt.tool, tt.args, got, tt.want)
			}
		})
	}
}
