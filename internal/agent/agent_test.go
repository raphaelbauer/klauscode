package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"klauscode/internal/llm"
	"klauscode/internal/tools"
	"klauscode/internal/tools/calculate"
	"klauscode/internal/tools/writefile"
)

// scriptedClient returns a queued reply per call and records the messages it
// received, so tests can assert observations were threaded back. replies scripts
// the text path (Content only); responses, when set, scripts the native path with
// full llm.Response values (tool calls and/or content).
type scriptedClient struct {
	replies   []string
	responses []llm.Response
	calls     int
	lastMsg   []llm.Message
	lastReq   llm.Request
}

func (s *scriptedClient) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	s.lastMsg = req.Messages
	s.lastReq = req
	if s.responses != nil {
		resp := s.responses[s.calls]
		s.calls++
		return resp, nil
	}
	reply := s.replies[s.calls]
	s.calls++
	return llm.Response{Content: reply}, nil
}

// newTestAgent builds an agent on the ReAct text path with a fixed label nonce so
// the injected observation labels and stop sequence are deterministic in
// assertions. The text mode is pinned here because these tests exercise the
// text-protocol behavior (parsing, nudges, implicit finals); native-path tests
// build their own agents with WithToolCalling(ToolCallingNative). A test may
// override an option by passing its own after this.
func newTestAgent(client llm.Client, opts ...Option) *Agent {
	reg := tools.NewRegistry()
	reg.Register(calculate.New())
	reg.Register(writefile.New())
	base := []Option{WithLabelNonce("TEST"), WithToolCalling(ToolCallingText)}
	return New(client, reg, append(base, opts...)...)
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
	// under the nonced Observation label
	var sawObservation bool
	for _, m := range client.lastMsg {
		if m.Role == "user" && strings.Contains(m.Content, "ObservationTEST: 111") {
			sawObservation = true
		}
	}
	if !sawObservation {
		t.Errorf("expected observation 'ObservationTEST: 111' in messages, got %+v", client.lastMsg)
	}

	// and the stop sequence sent to the model is the nonced Observation label, so
	// content quoting the bare word "Observation:" cannot truncate generation
	if len(client.lastReq.Stop) != 1 || client.lastReq.Stop[0] != "ObservationTEST:" {
		t.Errorf("stop = %v, want [ObservationTEST:]", client.lastReq.Stop)
	}
}

func TestAgentRunHappyPathWithNoncedLabels(t *testing.T) {
	// given a model that emits the nonced labels exactly as the prompt asks
	client := &scriptedClient{replies: []string{
		"ThoughtTEST: I need to compute.\nActionTEST: calculate((12 * 9) + 3)",
		"ThoughtTEST: I have found the answer.\nFinal AnswerTEST: 111",
	}}
	ag := newTestAgent(client)

	// when the agent runs the task
	answer, err := ag.Run(context.Background(), "What is (12 * 9) + 3?")

	// then the nonced Action and Final Answer are parsed and the loop completes
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "111" {
		t.Errorf("answer = %q, want %q", answer, "111")
	}
	if client.calls != 2 {
		t.Errorf("expected 2 model calls, got %d", client.calls)
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
		if strings.Contains(m.Content, "ObservationTEST: Error:") {
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

// newNativeAgent builds an agent on the native function-calling path.
func newNativeAgent(client llm.Client, mode ToolCalling, opts ...Option) *Agent {
	reg := tools.NewRegistry()
	reg.Register(calculate.New())
	reg.Register(writefile.New())
	base := []Option{WithToolCalling(mode)}
	return New(client, reg, append(base, opts...)...)
}

func TestAgentRunNativeExecutesToolThenFinishes(t *testing.T) {
	// given a model that first calls calculate, then replies with no tool calls
	client := &scriptedClient{responses: []llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "calculate", Arguments: `{"expression":"(12 * 9) + 3"}`}}},
		{Content: "The answer is 111."},
	}}
	ag := newNativeAgent(client, ToolCallingNative)

	// when the agent runs
	answer, err := ag.Run(context.Background(), "what is (12 * 9) + 3?")

	// then the tool ran and the no-tool-call turn is the final answer
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "The answer is 111." {
		t.Errorf("answer = %q, want %q", answer, "The answer is 111.")
	}
	if client.calls != 2 {
		t.Errorf("model calls = %d, want 2", client.calls)
	}

	// and the tool result was threaded back as a role:"tool" message paired by id
	var toolMsg *llm.Message
	for i := range client.lastMsg {
		if client.lastMsg[i].Role == "tool" {
			toolMsg = &client.lastMsg[i]
		}
	}
	if toolMsg == nil {
		t.Fatalf("no role:tool message threaded back, got %+v", client.lastMsg)
	}
	if toolMsg.ToolCallID != "call_1" {
		t.Errorf("tool message ToolCallID = %q, want call_1", toolMsg.ToolCallID)
	}
	if toolMsg.Content != "111" {
		t.Errorf("tool message Content = %q, want 111 (the calculate result)", toolMsg.Content)
	}

	// and the request carried the native tool specs, not a stop sequence
	if len(client.lastReq.Tools) == 0 {
		t.Error("expected native tool specs in the request, got none")
	}
	if len(client.lastReq.Stop) != 0 {
		t.Errorf("stop = %v, want none on the native path", client.lastReq.Stop)
	}
}

func TestAgentRunNativeToolErrorBecomesObservation(t *testing.T) {
	// given a tool call that will fail (bad expression), then a final reply
	client := &scriptedClient{responses: []llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "calculate", Arguments: `{"expression":"not math"}`}}},
		{Content: "Sorry, that was not a valid expression."},
	}}
	ag := newNativeAgent(client, ToolCallingNative)

	// when the agent runs
	answer, err := ag.Run(context.Background(), "compute nonsense")

	// then the run does not abort; the tool error is fed back and the model recovers
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "Sorry, that was not a valid expression." {
		t.Errorf("answer = %q", answer)
	}
	var toolMsg *llm.Message
	for i := range client.lastMsg {
		if client.lastMsg[i].Role == "tool" {
			toolMsg = &client.lastMsg[i]
		}
	}
	if toolMsg == nil || !strings.HasPrefix(toolMsg.Content, "Error:") {
		t.Errorf("expected a role:tool message beginning with 'Error:', got %+v", toolMsg)
	}
}

// erroringThenScriptedClient returns errs[i] on call i until they run out, then
// serves responses. It lets a test drive the auto-mode fallback: fail the first
// native request, then satisfy the text path.
type erroringThenScriptedClient struct {
	firstErr error
	replies  []string
	calls    int
	lastReq  llm.Request
}

func (c *erroringThenScriptedClient) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	c.lastReq = req
	c.calls++
	if c.calls == 1 && c.firstErr != nil {
		return llm.Response{}, c.firstErr
	}
	reply := c.replies[c.calls-1]
	return llm.Response{Content: reply}, nil
}

func TestAgentRunAutoFallsBackToTextOnStatusError(t *testing.T) {
	// given a server that rejects the native request (a StatusError), then serves
	// a text-path final answer on the retry
	client := &erroringThenScriptedClient{
		firstErr: &llm.StatusError{StatusCode: 400, Body: "unknown field tools"},
		replies:  []string{"", "Final Answer: 42"},
	}
	ag := newNativeAgent(client, ToolCallingAuto, WithLabelNonce("TEST"))

	// when the agent runs in auto mode
	answer, err := ag.Run(context.Background(), "the answer?")

	// then it fell back to the text path and returned the parsed final answer
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "42" {
		t.Errorf("answer = %q, want %q", answer, "42")
	}
	// the retry used the text path: a stop sequence is present, tools are not
	if len(client.lastReq.Stop) == 0 {
		t.Error("expected the text-path stop sequence after fallback, got none")
	}
	if len(client.lastReq.Tools) != 0 {
		t.Errorf("expected no tool specs on the text fallback, got %+v", client.lastReq.Tools)
	}
}

func TestAgentRunNativeDoesNotFallBackOnTransportError(t *testing.T) {
	// given a non-status (transport) error on the first native call
	client := &erroringThenScriptedClient{
		firstErr: errors.New("dial tcp: connection refused"),
		replies:  []string{"", "unused"},
	}
	ag := newNativeAgent(client, ToolCallingAuto)

	// when the agent runs in auto mode
	_, err := ag.Run(context.Background(), "hi")

	// then the error is surfaced, not masked by a text-path retry
	if err == nil {
		t.Fatal("expected the transport error to surface, got nil")
	}
	if client.calls != 1 {
		t.Errorf("model calls = %d, want 1 (no fallback retry)", client.calls)
	}
}
