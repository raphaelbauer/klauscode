package agent

import (
	"strings"
	"testing"
)

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

func TestParseStepStripsNamedArgWrapper(t *testing.T) {
	// given turns where the model imitated a name(param: type) signature and
	// wrapped the value in a keyword argument
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{"colon form", `Action: bash(command: "ls -R")`, "ls -R"},
		{"equals form", `Action: bash(command="ls -R")`, "ls -R"},
		{"single quoted value", `Action: read_file(path: 'main.go')`, "main.go"},
		// An env-var prefix has an unquoted value: it is a real command, kept verbatim.
		{"env prefix preserved", `Action: bash(FOO=bar ./script)`, "FOO=bar ./script"},
		// A plain command with no wrapper is unchanged.
		{"plain command unchanged", `Action: bash(ls -R)`, "ls -R"},
		// A quoted value that is itself a real command stays a command.
		{"quoted command unwrapped", `Action: bash("ls -R")`, "ls -R"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// when parsed
			step := ParseStep(tt.output)

			// then the keyword wrapper is removed but real commands are preserved
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

func TestParseStepMultiLineJSONAction(t *testing.T) {
	// given a write_file action whose JSON argument is pretty-printed across
	// several physical lines — the #1 real-world failure mode of the single-line
	// parser, which only saw the trailing "})" fragment
	output := "Thought: I'll create the file.\n" +
		"Action: write_file({\n" +
		`  "path": "hello.txt",` + "\n" +
		`  "content": "hi there"` + "\n" +
		"})"

	// when parsed
	step := ParseStep(output)

	// then the whole multi-line JSON object is captured as the action's args
	if !step.HasAction || step.ToolName != "write_file" {
		t.Fatalf("expected write_file action, got %+v", step)
	}
	want := "{\n  \"path\": \"hello.txt\",\n  \"content\": \"hi there\"\n}"
	if step.ToolArgs != want {
		t.Errorf("ToolArgs = %q, want %q", step.ToolArgs, want)
	}
}

func TestParseStepFencedJSONAction(t *testing.T) {
	// given a write_file action whose JSON argument is wrapped in a ```json fence,
	// the way many models emit structured arguments
	output := "Action: write_file(```json\n" +
		`{"path": "a.txt", "content": "hi"}` + "\n" +
		"```)"

	// when parsed
	step := ParseStep(output)

	// then the surrounding fence is stripped, leaving clean JSON args
	if !step.HasAction || step.ToolName != "write_file" {
		t.Fatalf("expected write_file action, got %+v", step)
	}
	if step.ToolArgs != `{"path": "a.txt", "content": "hi"}` {
		t.Errorf("ToolArgs = %q, want clean JSON", step.ToolArgs)
	}
}

func TestParseStepParensInJSONValueAcrossLines(t *testing.T) {
	// given a multi-line JSON action whose string value contains '(' and ')'
	// characters — the quote-aware scan must not treat them as paren depth
	output := "Action: write_file({\n" +
		`  "path": "main.go",` + "\n" +
		`  "content": "f(x) := (a) + (b)"` + "\n" +
		"})"

	// when parsed
	step := ParseStep(output)

	// then the closing paren of the value is ignored and the true call paren wins
	if !step.HasAction || step.ToolName != "write_file" {
		t.Fatalf("expected write_file action, got %+v", step)
	}
	if !strings.Contains(step.ToolArgs, `"content": "f(x) := (a) + (b)"`) {
		t.Errorf("ToolArgs lost paren-containing value: %q", step.ToolArgs)
	}
}

func TestParseStepMultiLineSingleArgAction(t *testing.T) {
	// given a single-arg bash action spanning multiple lines (a heredoc) — today's
	// single-line parser fails this; the generic extractor now handles it. This is
	// a deliberate, beneficial behavior change, pinned here so it stays intentional.
	output := "Thought: write a file via a heredoc.\n" +
		"Action: bash(cat <<EOF\n" +
		"hello\n" +
		"world\n" +
		"EOF\n" +
		")"

	// when parsed
	step := ParseStep(output)

	// then the full multi-line command is the action's argument
	if !step.HasAction || step.ToolName != "bash" {
		t.Fatalf("expected bash action, got %+v", step)
	}
	if step.ToolArgs != "cat <<EOF\nhello\nworld\nEOF" {
		t.Errorf("ToolArgs = %q, want the multi-line heredoc command", step.ToolArgs)
	}
}

func TestParseStepMultiLineJSONNotFalseFinal(t *testing.T) {
	// given a multi-line write_file action whose content value contains the literal
	// text "Final Answer:" (escaped, since JSON strings can't hold a raw newline).
	// finalRe runs before findActionBlock; its multiline `^` must not match the
	// label inside the escaped value.
	output := "Action: write_file({\n" +
		`  "path": "notes.txt",` + "\n" +
		`  "content": "Summary.\nFinal Answer: see the file"` + "\n" +
		"})"

	// when parsed
	step := ParseStep(output)

	// then it is treated as an action, not a final answer
	if step.HasFinal {
		t.Fatalf("expected action, not a final answer, got %+v", step)
	}
	if !step.HasAction || step.ToolName != "write_file" {
		t.Errorf("expected write_file action, got %+v", step)
	}
}

func TestParseStepActionLabelInsideArgValue(t *testing.T) {
	// given a write_file action whose content value itself contains a line that
	// begins with "Action:" — the real-world case where the model writes a plan or
	// transcript ABOUT this ReAct harness. actionOpenRe is line-anchored but not
	// string-aware, so that embedded line matches as a (bogus) later opener; the
	// genuine write_file call is the earlier opener.
	output := "Thought: I'll write the plan.\n" +
		"Action: write_file({\"path\": \"plan.md\", \"content\": \"Design notes.\n" +
		"       Action: ask(\\\"which part should I refactor?\\\")\n" +
		"done\"})"

	// when parsed
	step := ParseStep(output)

	// then the genuine write_file call wins, not the "ask" embedded in its argument
	if !step.HasAction || step.ToolName != "write_file" {
		t.Fatalf("expected write_file action, got %+v", step)
	}
	if !strings.Contains(step.ToolArgs, "Action: ask(") {
		t.Errorf("ToolArgs lost the embedded Action line: %q", step.ToolArgs)
	}
}

func TestParseStepSingleLineJSONStillParses(t *testing.T) {
	// given the original single-line JSON form (regression guard)
	output := `Action: write_file({"path":"a","content":"x"})`

	// when parsed
	step := ParseStep(output)

	// then it still parses exactly as before
	if !step.HasAction || step.ToolName != "write_file" {
		t.Fatalf("expected write_file action, got %+v", step)
	}
	if step.ToolArgs != `{"path":"a","content":"x"}` {
		t.Errorf("ToolArgs = %q", step.ToolArgs)
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

func TestParseStepFinalAnswerLenientLabels(t *testing.T) {
	// given final-answer turns whose label small local models render
	// inconsistently — different case, markdown emphasis, and extra spacing
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{"lowercase", "final answer: 42", "42"},
		{"upper", "FINAL ANSWER: 42", "42"},
		{"markdown bold around phrase", "**Final Answer:** 42", "42"},
		{"markdown bold around words", "**Final Answer**: 42", "42"},
		{"extra spaces", "Final   Answer : 42", "42"},
		{"after a thought line", "Thought: done.\nfinal answer: 42", "42"},
		{"multi-line answer kept", "Final Answer: line one\nline two", "line one\nline two"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// when parsed
			step := ParseStep(tt.output)

			// then the final answer is detected despite the label variation
			if !step.HasFinal {
				t.Fatalf("expected HasFinal for %q", tt.output)
			}
			if step.FinalAnswer != tt.want {
				t.Errorf("FinalAnswer = %q, want %q", step.FinalAnswer, tt.want)
			}
		})
	}
}

func TestParseStepNoFalseFinalMidSentence(t *testing.T) {
	// given a thought that merely mentions the phrase mid-sentence, not as a label
	output := "Thought: I will now compute the final answer: let me use calculate.\nAction: calculate(1+1)"

	// when parsed
	step := ParseStep(output)

	// then the `^`-anchored regex does not treat the mention as a final answer,
	// and the genuine action is still honored
	if step.HasFinal {
		t.Errorf("did not expect a final answer from a mid-sentence mention, got %+v", step)
	}
	if !step.HasAction || step.ToolArgs != "1+1" {
		t.Errorf("expected action calculate(1+1), got %+v", step)
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

func TestParseStepIgnoresActionInProse(t *testing.T) {
	// given a final-answer-style turn (no "Final Answer:" prefix) that only
	// *mentions* an Action as a documentation example, the way the model's
	// project overview did — the literal "Action: bash(ls -R)" sits inside
	// backticked prose, not on its own line.
	output := "This project is an AI agent harness.\n" +
		"The model requests a tool (e.g., `Action: bash(ls -R)`).\n" +
		"In short, it is a tool-using agent."

	// when parsed
	step := ParseStep(output)

	// then nothing is executed: the embedded example is not a live tool call,
	// so the loop nudges the model instead of running `ls -R)` in a shell.
	if step.HasAction {
		t.Errorf("expected no action for an Action mentioned only in prose, got %+v", step)
	}
	if step.HasFinal {
		t.Error("did not expect a final answer without a Final Answer: prefix")
	}
}

func TestParseStepActionMustBeFinalLine(t *testing.T) {
	// given an action that is followed by further prose, so it is not the
	// model's last line (a real action is always last: the loop stops at
	// "Observation:" right after the model emits it)
	output := "Action: bash(ls)\nActually, on reflection, let me reconsider."

	// when parsed
	step := ParseStep(output)

	// then the non-final action is ignored
	if step.HasAction {
		t.Errorf("expected no action when it is not the final line, got %+v", step)
	}
}

func TestParseStepActionOnFinalLineWithProseAbove(t *testing.T) {
	// given prose that mentions an example Action, followed by the genuine
	// action on the final line
	output := "An action looks like `Action: bash(pwd)`.\nAction: bash(ls)"

	// when parsed
	step := ParseStep(output)

	// then the real final-line action wins, not the example above it
	if !step.HasAction || step.ToolName != "bash" || step.ToolArgs != "ls" {
		t.Errorf("expected final-line action bash(ls), got %+v", step)
	}
}

func TestParseStepActionLinePositioning(t *testing.T) {
	// given turns where the genuine final-line action is wrapped in whitespace
	// the model commonly emits — these exercise lastNonEmptyLine, which trims
	// each line and skips trailing blank ones before the regex runs.
	tests := []struct {
		name     string
		output   string
		wantAct  bool
		wantArgs string
	}{
		// Models almost always end a turn with a newline; the action is still
		// the last *non-empty* line and must be honored.
		{"trailing blank lines", "Thought: list files.\nAction: bash(ls)\n\n   \n", true, "ls"},
		// An action indented under a thought is trimmed before matching.
		{"indented action line", "Thought: list files.\n    Action: bash(ls)", true, "ls"},
		// Nothing to parse: no action, no panic.
		{"empty output", "", false, ""},
		{"whitespace-only output", "   \n\t\n", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// when parsed
			step := ParseStep(tt.output)

			// then surrounding whitespace does not change whether the action fires
			if step.HasAction != tt.wantAct {
				t.Fatalf("HasAction = %v, want %v (step %+v)", step.HasAction, tt.wantAct, step)
			}
			if tt.wantAct && step.ToolArgs != tt.wantArgs {
				t.Errorf("ToolArgs = %q, want %q", step.ToolArgs, tt.wantArgs)
			}
		})
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
