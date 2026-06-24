package skill

import (
	"strings"
	"testing"

	"klauscode/internal/skills"
)

func TestCallReturnsBodyForKnownSkill(t *testing.T) {
	// given a skill tool built from one skill
	tool := New([]skills.Skill{
		{Name: "greet", Description: "Greet.", Body: "Say hello loudly."},
	})

	// when calling it by name
	got, err := tool.Call("greet")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// then the framed body is returned
	if !strings.Contains(got, "--- skill: greet ---") {
		t.Errorf("missing skill header in output: %q", got)
	}
	if !strings.Contains(got, "Say hello loudly.") {
		t.Errorf("missing skill body in output: %q", got)
	}
}

func TestCallTrimsWhitespaceAroundName(t *testing.T) {
	// given a skill tool
	tool := New([]skills.Skill{{Name: "greet", Description: "Greet.", Body: "body"}})

	// when calling with surrounding whitespace
	if _, err := tool.Call("  greet  "); err != nil {
		t.Errorf("Call with padded name failed: %v", err)
	}
}

func TestCallUnknownSkillErrors(t *testing.T) {
	// given an empty skill tool
	tool := New(nil)

	// when calling an unknown skill
	_, err := tool.Call("missing")

	// then an error is returned for the model to self-correct
	if err == nil {
		t.Fatal("expected error for unknown skill, got nil")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error %q should name the unknown skill", err.Error())
	}
}
