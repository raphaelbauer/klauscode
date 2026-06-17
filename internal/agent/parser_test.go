package agent

import "testing"

func TestParseStepAction(t *testing.T) {
	// given a turn containing a thought and an action
	output := "Thought: I should add the numbers.\nAction: calculate(2 + 3)"

	// when parsed
	step := ParseStep(output)

	// then the action is extracted
	if !step.HasAction {
		t.Fatal("expected HasAction, got false")
	}
	if step.ToolName != "calculate" {
		t.Errorf("ToolName = %q, want %q", step.ToolName, "calculate")
	}
	if step.ToolArgs != "2 + 3" {
		t.Errorf("ToolArgs = %q, want %q", step.ToolArgs, "2 + 3")
	}
	if step.HasFinal {
		t.Error("did not expect a final answer")
	}
}

func TestParseStepStripsSurroundingQuotes(t *testing.T) {
	// given turns where the model quoted the string argument
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{"double quotes", `Action: calculate("(12 * 9) + 3")`, "(12 * 9) + 3"},
		{"single quotes", `Action: calculate('12 * 9')`, "12 * 9"},
		{"no quotes unchanged", `Action: calculate(2 + 3)`, "2 + 3"},
		{"inner quotes preserved", `Action: search("a" or "b")`, `"a" or "b"`},
		{"unbalanced left untouched", `Action: calculate("12 * 9)`, `"12 * 9`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// when parsed
			step := ParseStep(tt.output)

			// then surrounding quotes are stripped, inner ones kept
			if step.ToolArgs != tt.want {
				t.Errorf("ToolArgs = %q, want %q", step.ToolArgs, tt.want)
			}
		})
	}
}

func TestParseStepJSONArgsWithParen(t *testing.T) {
	// given an action whose single-line JSON argument contains a ')' character
	output := `Action: write_file({"path":"a","content":"x)y"})`

	// when parsed
	step := ParseStep(output)

	// then the greedy match stops at the action's own closing paren and the JSON
	// passes through unquote() untouched (it does not start with a quote)
	if !step.HasAction || step.ToolName != "write_file" {
		t.Fatalf("expected write_file action, got %+v", step)
	}
	if step.ToolArgs != `{"path":"a","content":"x)y"}` {
		t.Errorf("ToolArgs = %q, want %q", step.ToolArgs, `{"path":"a","content":"x)y"}`)
	}
}

func TestParseStepFinalAnswer(t *testing.T) {
	// given a turn with a final answer
	output := "Thought: I have found the answer.\nFinal Answer: The result is 111."

	// when parsed
	step := ParseStep(output)

	// then the final answer is extracted and no action is reported
	if !step.HasFinal {
		t.Fatal("expected HasFinal, got false")
	}
	if step.FinalAnswer != "The result is 111." {
		t.Errorf("FinalAnswer = %q, want %q", step.FinalAnswer, "The result is 111.")
	}
	if step.HasAction {
		t.Error("did not expect an action alongside a final answer")
	}
}

func TestParseStepFinalTakesPrecedence(t *testing.T) {
	// given a turn with both an action and a final answer
	output := "Action: calculate(1+1)\nFinal Answer: done"

	// when parsed
	step := ParseStep(output)

	// then the final answer wins
	if !step.HasFinal || step.HasAction {
		t.Errorf("expected final answer to take precedence, got %+v", step)
	}
}

func TestParseStepLastActionWins(t *testing.T) {
	// given a turn with multiple action lines
	output := "Action: calculate(1+1)\nObservation: 2\nAction: calculate(2+2)"

	// when parsed
	step := ParseStep(output)

	// then the last action is used
	if step.ToolArgs != "2+2" {
		t.Errorf("ToolArgs = %q, want %q", step.ToolArgs, "2+2")
	}
}

func TestParseStepMalformed(t *testing.T) {
	// given a turn with neither action nor final answer
	output := "Thought: hmm, I'm not sure what to do."

	// when parsed
	step := ParseStep(output)

	// then neither flag is set
	if step.HasAction || step.HasFinal {
		t.Errorf("expected no action and no final, got %+v", step)
	}
}
