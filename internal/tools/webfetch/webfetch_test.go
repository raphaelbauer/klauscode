package webfetch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const pageFixture = `<html><head><style>.x{color:red}</style>
<script>steal()</script></head>
<body><h1>Docs</h1><p>Use context.WithTimeout &amp; cancel.</p></body></html>`

func TestWebFetchStripsHTML(t *testing.T) {
	// given a server returning an HTML page
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(pageFixture))
	}))
	defer server.Close()
	tool := New()

	// when fetched
	got, err := tool.Call(server.URL)

	// then scripts/styles/tags are gone, entities decoded, content wrapped
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if strings.Contains(got, "steal") || strings.Contains(got, "color:red") {
		t.Errorf("script/style not stripped: %q", got)
	}
	if strings.Contains(got, "<h1>") || strings.Contains(got, "<p>") {
		t.Errorf("tags not stripped: %q", got)
	}
	if !strings.Contains(got, "Use context.WithTimeout & cancel.") {
		t.Errorf("expected decoded body text, got %q", got)
	}
	if !strings.Contains(got, "UNTRUSTED WEB CONTENT") {
		t.Errorf("expected untrusted-content wrapper, got %q", got)
	}
	if !strings.Contains(gotUA, "Mozilla") {
		t.Errorf("expected browser User-Agent, got %q", gotUA)
	}
}

func TestWebFetchNon200(t *testing.T) {
	// given a server returning 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	tool := New()

	// when fetched
	_, err := tool.Call(server.URL)

	// then an error is returned for the model to recover from
	if err == nil {
		t.Error("expected error for 404, got nil")
	}
}
