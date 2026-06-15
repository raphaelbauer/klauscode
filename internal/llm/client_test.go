package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIClientComplete(t *testing.T) {
	// given a stub server that records the request and returns a canned reply
	var gotAuth string
	var gotBody chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Final Answer: 4"}}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("test-key", "gpt-4o-mini", WithBaseURL(server.URL))

	// when Complete is called
	out, err := client.Complete(
		context.Background(),
		[]Message{{Role: "user", Content: "2+2"}},
		[]string{"Observation:"},
	)

	// then the reply is returned and the request was well-formed
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if out != "Final Answer: 4" {
		t.Errorf("out = %q, want %q", out, "Final Answer: 4")
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
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
}

func TestOpenAIClientNon200(t *testing.T) {
	// given a server that returns an error status
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("bad", "gpt-4o-mini", WithBaseURL(server.URL))

	// when Complete is called
	_, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)

	// then an error is returned
	if err == nil {
		t.Fatal("expected error for non-200 response, got nil")
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
	if _, err := client.Complete(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error for empty choices, got nil")
	}
}
