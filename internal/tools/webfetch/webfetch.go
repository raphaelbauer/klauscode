// Package webfetch provides the web_fetch tool: it downloads a URL and returns
// its readable text. WebFetchTool implements tools.Tool by structural typing,
// so this package does not import tools.
package webfetch

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"klauscode/internal/tools/textutil"
)

const (
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
	// maxBytes caps the page text returned to the model.
	maxBytes = 128 * 1024
)

// WebFetchTool fetches a URL and strips it to text.
type WebFetchTool struct {
	httpClient *http.Client
}

// Option configures a WebFetchTool.
type Option func(*WebFetchTool)

// WithHTTPClient injects a custom *http.Client (used by tests).
func WithHTTPClient(h *http.Client) Option { return func(t *WebFetchTool) { t.httpClient = h } }

// New returns a ready-to-register web_fetch tool.
func New(opts ...Option) *WebFetchTool {
	t := &WebFetchTool{httpClient: &http.Client{Timeout: 20 * time.Second}}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

func (t *WebFetchTool) Description() string {
	return "web_fetch(<url>): Fetch a URL and return its page text (HTML stripped). Put the URL directly inside the parentheses, e.g. web_fetch(https://pkg.go.dev/context)."
}

// Parameters is the JSON Schema for native function-calling: a single required
// string mapped straight to Call.
func (t *WebFetchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"The URL to fetch, e.g. https://pkg.go.dev/context"}},"required":["url"]}`)
}

// Call fetches args (the raw URL), converts HTML to text and returns it wrapped
// as untrusted content. Network/HTTP failures are returned as errors so the
// model can recover.
func (t *WebFetchTool) Call(args string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, args, nil)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("web_fetch: %s returned %d", args, resp.StatusCode)
	}

	text := textutil.Truncate(textutil.HTMLToText(string(data)), maxBytes)
	return textutil.WrapUntrusted(text), nil
}
