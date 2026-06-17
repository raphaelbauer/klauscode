package agent

import (
	"context"
	"strings"
	"testing"

	"klauscode/internal/llm"
	"klauscode/internal/tools"
	"klauscode/internal/tools/calculate"
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
	return New(client, reg, opts...)
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
