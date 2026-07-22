package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIClientComplete(t *testing.T) {
	// given a stub server that records the request and returns a canned reply
	var gotAuth string
	var gotPath string
	var gotBody chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Final Answer: 4"}}]}`))
	}))
	defer server.Close()

	// base URL ends in /v1; the client should append /chat/completions
	client := NewOpenAIClient("test-key", "gpt-4o-mini", WithBaseURL(server.URL+"/v1"))

	// when Complete is called
	out, err := client.Complete(
		context.Background(),
		Request{
			Messages: []Message{{Role: "user", Content: "2+2"}},
			Stop:     []string{"Observation:"},
		},
	)

	// then the reply is returned and the request was well-formed
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if out.Content != "Final Answer: 4" {
		t.Errorf("out.Content = %q, want %q", out.Content, "Final Answer: 4")
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want %q", gotPath, "/v1/chat/completions")
	}
	if gotBody.Model != "gpt-4o-mini" {
		t.Errorf("model = %q, want %q", gotBody.Model, "gpt-4o-mini")
	}
	if len(gotBody.Stop) != 1 || gotBody.Stop[0] != "Observation:" {
		t.Errorf("stop = %v, want [Observation:]", gotBody.Stop)
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Content != "2+2" {
		t.Errorf("messages = %+v, want one user message", gotBody.Messages)
	}
	// the text path must not send a tools field
	if gotBody.Tools != nil {
		t.Errorf("tools = %v, want nil on the text path", gotBody.Tools)
	}
}

func TestOpenAIClientCompleteNativeTools(t *testing.T) {
	// given a server that records the request and returns a tool call
	var gotBody chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"calculate","arguments":"{\"expression\":\"2+2\"}"}}]}}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("k", "m", WithBaseURL(server.URL))

	// when Complete is called with a tool spec
	resp, err := client.Complete(context.Background(), Request{
		Messages:   []Message{{Role: "user", Content: "2+2"}},
		Tools:      []ToolSpec{{Name: "calculate", Description: "does math", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: "auto",
	})

	// then the tools field was sent and the tool call was parsed back
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if len(gotBody.Tools) != 1 || gotBody.Tools[0].Function.Name != "calculate" {
		t.Errorf("tools = %+v, want one calculate function", gotBody.Tools)
	}
	if gotBody.Tools[0].Type != "function" {
		t.Errorf("tool type = %q, want function", gotBody.Tools[0].Type)
	}
	if gotBody.ToolChoice != "auto" {
		t.Errorf("tool_choice = %q, want auto", gotBody.ToolChoice)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("resp.ToolCalls = %+v, want one", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "calculate" || tc.Arguments != `{"expression":"2+2"}` {
		t.Errorf("tool call = %+v, want id=call_1 name=calculate args={...}", tc)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", resp.FinishReason)
	}
}

func TestOpenAIClientBaseURLTrailingSlash(t *testing.T) {
	// given a base URL with a trailing slash
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("k", "m", WithBaseURL(server.URL+"/v1/"))

	// when Complete is called
	if _, err := client.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}

	// then the trailing slash is trimmed, not doubled
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want %q", gotPath, "/v1/chat/completions")
	}
}

func TestOpenAIClientNon200(t *testing.T) {
	// given a server that returns an error status
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"no tools"}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("bad", "gpt-4o-mini", WithBaseURL(server.URL))

	// when Complete is called
	_, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})

	// then a StatusError carrying the code is returned (so "auto" mode can detect it)
	if err == nil {
		t.Fatal("expected error for non-200 response, got nil")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *StatusError", err)
	}
	if se.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", se.StatusCode, http.StatusBadRequest)
	}
}

func TestOpenAIClientNoChoices(t *testing.T) {
	// given a server that returns no choices
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("k", "m", WithBaseURL(server.URL))

	// when Complete is called then it errors
	if _, err := client.Complete(context.Background(), Request{}); err == nil {
		t.Fatal("expected error for empty choices, got nil")
	}
}
