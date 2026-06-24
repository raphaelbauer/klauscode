package agent

import (
	"context"
	"strings"
	"testing"

	"klauscode/internal/llm"
	"klauscode/internal/tools"
	"klauscode/internal/tools/calculate"
	"klauscode/internal/tools/writefile"
)

// scriptedClient returns a queued reply per call and records the messages it
// received, so tests can assert observations were threaded back.
type scriptedClient struct {
	replies []string
	calls   int
	lastMsg []llm.Message
}

func (s *scriptedClient) Complete(_ context.Context, messages []llm.Message, _ []string) (string, error) {
	s.lastMsg = messages
	reply := s.replies[s.calls]
	s.calls++
	return reply, nil
}

func newTestAgent(client llm.Client, opts ...Option) *Agent {
	reg := tools.NewRegistry()
	reg.Register(calculate.New())
	reg.Register(writefile.New())
	return New(client, reg, opts...)
}

func TestAgentWithInstructionsReachesSystemPrompt(t *testing.T) {
	// given an agent built with user/project instructions
	client := &scriptedClient{replies: []string{"Final Answer: done"}}
	ag := newTestAgent(client, WithInstructions("always answer in French"))

	// when it runs (options are applied before the system prompt is built)
	if _, err := ag.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// then the system message carries the injected instructions
	var sawInstructions bool
	for _, m := range client.lastMsg {
		if m.Role == "system" && strings.Contains(m.Content, "always answer in French") {
			sawInstructions = true
		}
	}
	if !sawInstructions {
		t.Error("instructions were not injected into the system prompt")
	}
}

func TestAgentRunHappyPath(t *testing.T) {
	// given a model that asks for a calculation, then answers
	client := &scriptedClient{replies: []string{
		"Thought: I need to compute.\nAction: calculate((12 * 9) + 3)",
		"Thought: I have found the answer.\nFinal Answer: 111",
	}}
	ag := newTestAgent(client)

	// when the agent runs the task
	answer, err := ag.Run(context.Background(), "What is (12 * 9) + 3?")

	// then it returns the final answer after running the tool
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "111" {
		t.Errorf("answer = %q, want %q", answer, "111")
	}
	if client.calls != 2 {
		t.Errorf("expected 2 model calls, got %d", client.calls)
	}

	// and the observation (the tool result) was fed back into the conversation
	var sawObservation bool
	for _, m := range client.lastMsg {
		if m.Role == "user" && strings.Contains(m.Content, "Observation: 111") {
			sawObservation = true
		}
	}
	if !sawObservation {
		t.Errorf("expected observation 'Observation: 111' in messages, got %+v", client.lastMsg)
	}
}

func TestAgentRunToolErrorIsObservation(t *testing.T) {
	// given a model that triggers a tool error, then recovers
	client := &scriptedClient{replies: []string{
		"Action: calculate(1 / 0)",
		"Final Answer: cannot divide by zero",
	}}
	ag := newTestAgent(client)

	// when the agent runs
	answer, err := ag.Run(context.Background(), "divide by zero")

	// then the run completes and the error was passed back as an observation
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "cannot divide by zero" {
		t.Errorf("answer = %q", answer)
	}
	var sawError bool
	for _, m := range client.lastMsg {
		if strings.Contains(m.Content, "Observation: Error:") {
			sawError = true
		}
	}
	if !sawError {
		t.Errorf("expected tool error fed back as observation, got %+v", client.lastMsg)
	}
}

func TestAgentRunMalformedThenRecovers(t *testing.T) {
	// given a model that first breaks format, then recovers
	client := &scriptedClient{replies: []string{
		"Thought: I'm just thinking out loud.",
		"Final Answer: ok",
	}}
	ag := newTestAgent(client)

	// when the agent runs
	answer, err := ag.Run(context.Background(), "test")

	// then it nudges and still finishes
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "ok" {
		t.Errorf("answer = %q, want %q", answer, "ok")
	}
}

func TestAgentRunImplicitFinalAfterSecondMiss(t *testing.T) {
	// given a model (e.g. a small local model) that never writes the
	// "Final Answer:" prefix and just keeps replying in prose
	client := &scriptedClient{replies: []string{
		"Thought: I think the capital of France is Paris.",
		"Thought: The capital of France is Paris.",
	}}
	ag := newTestAgent(client)

	// when the agent runs
	answer, err := ag.Run(context.Background(), "What is the capital of France?")

	// then it nudges once, and on the second prose turn returns that turn as the
	// final answer (with the "Thought:" scaffolding stripped) rather than looping
	// to the step limit
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "The capital of France is Paris." {
		t.Errorf("answer = %q, want %q", answer, "The capital of France is Paris.")
	}
	if client.calls != 2 {
		t.Errorf("expected 2 model calls, got %d", client.calls)
	}
}

func TestAgentRunEmptyTurnDoesNotDiscardProseAnswer(t *testing.T) {
	// given a model that produces a complete prose answer with no "Final Answer:"
	// prefix and then, after being nudged, produces an empty turn because it
	// considers itself done and has nothing to add (the observed Gemma trace)
	answerText := "This project, called Klaus Code, is a minimalist AI agent harness written in Go."
	client := &scriptedClient{replies: []string{
		answerText,
		"",
	}}
	ag := newTestAgent(client)

	// when the agent runs
	answer, err := ag.Run(context.Background(), "Summarize this project.")

	// then the harness returns the substantive prose answer, NOT the empty
	// follow-up turn (regression: an empty turn must not overwrite a good answer
	// nor be returned as the final answer)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != answerText {
		t.Errorf("answer = %q, want %q", answer, answerText)
	}
}

func TestAgentRunMalformedActionGetsFormatNudge(t *testing.T) {
	// given a turn that carries an "Action:" token but malforms the call (an
	// unterminated write_file with no closing paren), then recovers
	client := &scriptedClient{replies: []string{
		"Thought: writing the file.\nAction: write_file(\nI forgot to close the call.",
		"Final Answer: done",
	}}
	ag := newTestAgent(client)

	// when the agent runs
	answer, err := ag.Run(context.Background(), "create a file")

	// then it finishes, and the nudge fed back was the format-specific one
	// (teaching the JSON-argument shape) rather than the generic message
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "done" {
		t.Errorf("answer = %q, want %q", answer, "done")
	}
	var sawFormatNudge bool
	for _, m := range client.lastMsg {
		if strings.Contains(m.Content, `write_file({"path"`) {
			sawFormatNudge = true
		}
	}
	if !sawFormatNudge {
		t.Errorf("expected the format-specific nudge in messages, got %+v", client.lastMsg)
	}
}

func TestAgentRunMalformedActionOnSecondMissDoesNotBecomeAnswer(t *testing.T) {
	// given two malformed write_file turns in a row (the 2nd has no closing
	// paren — "}}" instead of "})"), then a clean final answer
	client := &scriptedClient{replies: []string{
		"Thought: attempt one.\nAction: write_file({\n  \"path\": \"p\",\n  \"content\": \"x\"\n})\nstray prose breaks the guard",
		"Thought: attempt two.\nAction: write_file({\"path\": \"p\", \"content\": \"x\"}}",
		"Final Answer: wrote it",
	}}
	ag := newTestAgent(client)

	// when the agent runs
	answer, err := ag.Run(context.Background(), "write a file")

	// then the malformed action is NOT returned as the answer; the model is
	// nudged again and its real final answer is returned
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if strings.Contains(answer, "Action:") || strings.Contains(answer, "write_file") {
		t.Errorf("malformed action leaked into the answer: %q", answer)
	}
	if answer != "wrote it" {
		t.Errorf("answer = %q, want %q", answer, "wrote it")
	}
	if client.calls != 3 {
		t.Errorf("expected 3 model calls, got %d", client.calls)
	}
}

func TestAgentRunStepLimit(t *testing.T) {
	// given a model that never produces a final answer
	client := &scriptedClient{replies: []string{
		"Action: calculate(1+1)",
		"Action: calculate(1+1)",
	}}
	ag := newTestAgent(client, WithMaxSteps(2))

	// when the agent runs
	_, err := ag.Run(context.Background(), "loop forever")

	// then it stops at the step limit with an error
	if err == nil {
		t.Fatal("expected step-limit error, got nil")
	}
	if !strings.Contains(err.Error(), "step limit") {
		t.Errorf("error = %v, want step-limit error", err)
	}
}
