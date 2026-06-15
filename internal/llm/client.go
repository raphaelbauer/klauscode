// Package llm is the provider boundary for the harness. It exposes a small
// Client interface so the agent can talk to a language model without knowing
// which provider sits behind it, and ships an OpenAI implementation built only
// on the standard library.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Message is a single chat turn. Role is one of "system", "user" or
// "assistant".
type Message struct {
	Role    string
	Content string
}

// Client talks to a language model.
type Client interface {
	// Complete sends the conversation and returns the assistant's reply.
	// stop holds sequences that halt generation (e.g. "Observation:") so the
	// agent regains control before the model writes an observation itself.
	Complete(ctx context.Context, messages []Message, stop []string) (string, error)
}

// defaultBaseURL is the OpenAI chat completions endpoint.
const defaultBaseURL = "https://api.openai.com/v1/chat/completions"

// OpenAIClient is a Client backed by the OpenAI chat completions API.
type OpenAIClient struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// Option configures an OpenAIClient.
type Option func(*OpenAIClient)

// WithBaseURL overrides the API endpoint. Used by tests to point at httptest.
func WithBaseURL(url string) Option {
	return func(c *OpenAIClient) { c.baseURL = url }
}

// WithHTTPClient injects a custom *http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *OpenAIClient) { c.httpClient = h }
}

// NewOpenAIClient builds a client for the given key and model.
func NewOpenAIClient(apiKey, model string, opts ...Option) *OpenAIClient {
	c := &OpenAIClient{
		apiKey:     apiKey,
		model:      model,
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// chatRequest is the JSON body we send to OpenAI.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stop        []string      `json:"stop,omitempty"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse is the subset of the OpenAI response we care about.
type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// Complete implements Client against the OpenAI chat completions API.
func (c *OpenAIClient) Complete(ctx context.Context, messages []Message, stop []string) (string, error) {
	reqBody := chatRequest{
		Model:       c.model,
		Messages:    toChatMessages(messages),
		Stop:        stop,
		Temperature: 0, // deterministic tool use
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call openai: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai returned %d: %s", resp.StatusCode, string(body))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openai returned no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

func toChatMessages(messages []Message) []chatMessage {
	out := make([]chatMessage, len(messages))
	for i, m := range messages {
		out[i] = chatMessage{Role: m.Role, Content: m.Content}
	}
	return out
}
