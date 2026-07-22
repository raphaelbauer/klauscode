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
	"strings"
	"time"
)

// Message is a single chat turn. Role is one of "system", "user", "assistant"
// or "tool". The tool-calling fields are optional and only used on the native
// path: an assistant turn may carry ToolCalls (the calls the model requested),
// and a "tool" turn carries ToolCallID (which call it answers) plus Name.
type Message struct {
	Role    string
	Content string

	ToolCalls  []ToolCall // assistant turn: tool calls the model requested
	ToolCallID string     // tool turn: the id of the call this message answers
	Name       string     // tool turn: the tool's name (optional)
}

// ToolSpec describes a tool the model may call natively. Parameters is a JSON
// Schema object for the tool's arguments.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is a single native tool invocation the model requested. Arguments is
// the raw JSON string the provider returned for the call.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Request is a single completion request. Stop is used by the text path;
// Tools/ToolChoice are used by the native function-calling path. A request sets
// one or the other, never both.
type Request struct {
	Messages   []Message
	Stop       []string
	Tools      []ToolSpec
	ToolChoice string // "", "auto", "none", "required"; "" lets the provider default
}

// Response is a single completion result. On the native path ToolCalls is
// populated when the model requested tools; an empty ToolCalls means the model
// replied directly and Content is the final answer.
type Response struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
}

// Client talks to a language model.
type Client interface {
	// Complete sends the conversation and returns the assistant's reply. On the
	// text path req.Stop halts generation (e.g. "Observation:") so the agent
	// regains control; on the native path req.Tools offers function calls and the
	// reply carries structured ToolCalls.
	Complete(ctx context.Context, req Request) (Response, error)
}

// StatusError is returned when the provider responds with a non-200 status. The
// agent's "auto" mode uses it to tell a request the server rejected (e.g. a
// server that does not implement the tools field) from a transport failure, so
// it can fall back to the text path.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("openai returned %d: %s", e.StatusCode, e.Body)
}

// defaultBaseURL is the OpenAI API base URL. The chat completions path is
// appended to it when a request is built.
const defaultBaseURL = "https://api.openai.com/v1"

// OpenAIClient is a Client backed by the OpenAI chat completions API.
type OpenAIClient struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// Option configures an OpenAIClient.
type Option func(*OpenAIClient)

// WithBaseURL overrides the API base URL (the part ending in /v1), e.g. a local
// OpenAI-compatible server such as LM Studio, or an httptest server in tests.
// The /chat/completions path is appended automatically.
func WithBaseURL(url string) Option {
	return func(c *OpenAIClient) { c.baseURL = url }
}

// WithHTTPClient injects a custom *http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *OpenAIClient) { c.httpClient = h }
}

// WithTimeout overrides the HTTP client's request timeout. Useful for slow local
// servers (e.g. a large model on LM Studio) whose first token can take minutes.
// A non-positive duration disables the timeout entirely (waits indefinitely).
func WithTimeout(d time.Duration) Option {
	return func(c *OpenAIClient) {
		if d < 0 {
			d = 0
		}
		c.httpClient.Timeout = d
	}
}

// NewOpenAIClient builds a client for the given key and model.
func NewOpenAIClient(apiKey, model string, opts ...Option) *OpenAIClient {
	c := &OpenAIClient{
		apiKey:     apiKey,
		model:      model,
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
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
	Tools       []chatTool    `json:"tools,omitempty"`
	ToolChoice  string        `json:"tool_choice,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`

	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

// chatTool and chatFunction mirror OpenAI's tools[] request shape.
type chatTool struct {
	Type     string       `json:"type"` // always "function"
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// chatToolCall and chatFunctionCall mirror OpenAI's tool_calls[] shape, used
// both when the assistant requests calls and when we echo them back in history.
type chatToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"` // always "function"
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatResponse is the subset of the OpenAI response we care about.
type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

// Complete implements Client against the OpenAI chat completions API.
func (c *OpenAIClient) Complete(ctx context.Context, req Request) (Response, error) {
	reqBody := chatRequest{
		Model:       c.model,
		Messages:    toChatMessages(req.Messages),
		Stop:        req.Stop,
		Temperature: 0, // deterministic tool use
		Tools:       toChatTools(req.Tools),
		ToolChoice:  req.ToolChoice,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return Response{}, fmt.Errorf("encode request: %w", err)
	}

	endpoint := strings.TrimRight(c.baseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("call openai: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return Response{}, &StatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("openai returned no choices")
	}
	choice := parsed.Choices[0]
	return Response{
		Content:      choice.Message.Content,
		ToolCalls:    fromChatToolCalls(choice.Message.ToolCalls),
		FinishReason: choice.FinishReason,
	}, nil
}

func toChatMessages(messages []Message) []chatMessage {
	out := make([]chatMessage, len(messages))
	for i, m := range messages {
		out[i] = chatMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
			ToolCalls:  toChatToolCalls(m.ToolCalls),
		}
	}
	return out
}

func toChatTools(specs []ToolSpec) []chatTool {
	if len(specs) == 0 {
		return nil
	}
	out := make([]chatTool, len(specs))
	for i, s := range specs {
		out[i] = chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  s.Parameters,
			},
		}
	}
	return out
}

func toChatToolCalls(calls []ToolCall) []chatToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]chatToolCall, len(calls))
	for i, c := range calls {
		out[i] = chatToolCall{
			ID:       c.ID,
			Type:     "function",
			Function: chatFunctionCall{Name: c.Name, Arguments: c.Arguments},
		}
	}
	return out
}

func fromChatToolCalls(calls []chatToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, len(calls))
	for i, c := range calls {
		out[i] = ToolCall{
			ID:        c.ID,
			Name:      c.Function.Name,
			Arguments: c.Function.Arguments,
		}
	}
	return out
}
