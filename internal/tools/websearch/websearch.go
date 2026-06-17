// Package websearch provides the web_search tool, backed by DuckDuckGo's
// keyless HTML endpoint. WebSearchTool implements tools.Tool by structural
// typing, so this package does not import tools.
//
// Scraping HTML is brittle by nature (DuckDuckGo may change its markup or serve
// a bot-check page); the tool degrades gracefully to a "no results" message so
// the model can fall back to web_fetch instead of looping.
package websearch

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"klauscode/internal/tools/textutil"
)

const (
	defaultEndpoint = "https://html.duckduckgo.com/html/"
	// userAgent mimics a browser; the keyless endpoint is less likely to serve a
	// bot-check page to a realistic UA (it can still happen on shared IPs).
	userAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
	maxResults = 5
)

var (
	resultRe  = regexp.MustCompile(`(?s)<a[^>]+class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	snippetRe = regexp.MustCompile(`(?s)<a[^>]+class="result__snippet"[^>]*>(.*?)</a>`)
	uddgRe    = regexp.MustCompile(`[?&]uddg=([^&]+)`)
)

// WebSearchTool searches the web via DuckDuckGo.
type WebSearchTool struct {
	endpoint   string
	httpClient *http.Client
}

// Option configures a WebSearchTool.
type Option func(*WebSearchTool)

// WithEndpoint overrides the search endpoint (used by tests).
func WithEndpoint(u string) Option { return func(t *WebSearchTool) { t.endpoint = u } }

// WithHTTPClient injects a custom *http.Client (used by tests).
func WithHTTPClient(h *http.Client) Option { return func(t *WebSearchTool) { t.httpClient = h } }

// New returns a ready-to-register web_search tool.
func New(opts ...Option) *WebSearchTool {
	t := &WebSearchTool{
		endpoint:   defaultEndpoint,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *WebSearchTool) Name() string { return "web_search" }

func (t *WebSearchTool) Description() string {
	return "web_search(query: str): Search the web (DuckDuckGo). Returns top results as title — url — snippet."
}

// Call runs the query and returns up to maxResults results, wrapped as untrusted
// content. Network and bot-check failures are returned as observations (errors
// or a "no results" note) so the model can recover.
func (t *WebSearchTool) Call(args string) (string, error) {
	body, err := t.fetch(args)
	if err != nil {
		return "", fmt.Errorf("web_search: %w", err)
	}

	results := parseResults(body)
	if len(results) == 0 {
		// Either a bot-check page or genuinely nothing; tell the model so it can
		// fall back to web_fetch rather than retrying the same search.
		return fmt.Sprintf("web_search: no results (DuckDuckGo returned a bot-check page or nothing for %q)", args), nil
	}
	return textutil.WrapUntrusted(strings.Join(results, "\n")), nil
}

func (t *WebSearchTool) fetch(query string) (string, error) {
	endpoint := t.endpoint + "?q=" + url.QueryEscape(query)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseResults(body string) []string {
	links := resultRe.FindAllStringSubmatch(body, -1)
	snippets := snippetRe.FindAllStringSubmatch(body, -1)

	var out []string
	for i, m := range links {
		if len(out) >= maxResults {
			break
		}
		title := textutil.HTMLToText(m[2])
		link := decodeURL(m[1])
		snippet := ""
		if i < len(snippets) {
			snippet = textutil.HTMLToText(snippets[i][1])
		}
		out = append(out, fmt.Sprintf("%s — %s — %s", title, link, snippet))
	}
	return out
}

// decodeURL unwraps DuckDuckGo's redirect links (//duckduckgo.com/l/?uddg=...)
// back to the real destination. A non-redirect href is returned as-is.
func decodeURL(href string) string {
	if m := uddgRe.FindStringSubmatch(href); m != nil {
		if decoded, err := url.QueryUnescape(m[1]); err == nil {
			return decoded
		}
	}
	return href
}
