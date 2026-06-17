package websearch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ddgFixture mimics the relevant parts of DuckDuckGo's html/ result markup,
// including a wrapped uddg redirect link and <b> highlighting in the title.
const ddgFixture = `<html><body>
<div class="result results_links results_links_deep web-result">
  <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fgo&amp;rut=abc">The <b>Go</b> Example</a>
  <a class="result__snippet" href="//duckduckgo.com/l/?uddg=x">A snippet about Go &amp; tools.</a>
</div>
</body></html>`

const anomalyFixture = `<html><body><div class="anomaly-modal">Unfortunately, bots use DuckDuckGo too.</div></body></html>`

func newServer(t *testing.T, html string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestWebSearchParsesResults(t *testing.T) {
	// given a stub DDG server returning one result
	server := newServer(t, ddgFixture)
	tool := New(WithEndpoint(server.URL))

	// when searching
	got, err := tool.Call("golang")

	// then the redirect URL is decoded, tags are stripped and it is wrapped
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if !strings.Contains(got, "The Go Example") {
		t.Errorf("expected decoded title, got %q", got)
	}
	if !strings.Contains(got, "https://example.com/go") {
		t.Errorf("expected decoded url, got %q", got)
	}
	if !strings.Contains(got, "A snippet about Go & tools.") {
		t.Errorf("expected decoded snippet, got %q", got)
	}
	if !strings.Contains(got, "UNTRUSTED WEB CONTENT") {
		t.Errorf("expected untrusted-content wrapper, got %q", got)
	}
}

func TestWebSearchSendsUserAgent(t *testing.T) {
	// given a server that records the User-Agent header
	var gotUA string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(ddgFixture))
	}))
	defer s.Close()
	tool := New(WithEndpoint(s.URL))

	// when searching
	if _, err := tool.Call("q"); err != nil {
		t.Fatalf("Call returned error: %v", err)
	}

	// then a browser-like User-Agent was sent
	if !strings.Contains(gotUA, "Mozilla") {
		t.Errorf("User-Agent = %q, want a browser-like value", gotUA)
	}
}

func TestWebSearchAnomalyFallback(t *testing.T) {
	// given a server returning a bot-check page
	server := newServer(t, anomalyFixture)
	tool := New(WithEndpoint(server.URL))

	// when searching
	got, err := tool.Call("golang")

	// then a graceful no-results message is returned (not an error)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !strings.Contains(got, "no results") {
		t.Errorf("expected no-results message, got %q", got)
	}
}
