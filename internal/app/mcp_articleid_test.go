package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// Acceptance tests for #147: get_article rejects the empty id and ids made
// only of dots before any HTTP request, with a hint that points the agent to
// search_knowledge.

// invalidArticleIDs are the ids the issue names, plus a longer dot-only id.
var invalidArticleIDs = map[string]string{
	"empty":      "",
	"one dot":    ".",
	"two dots":   "..",
	"three dots": "...",
	"four dots":  "....",
}

// countingServer starts the real public handler behind a counter of every
// request it receives, whatever the path.
func countingServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	h, err := PublicHandler(Options{SwaggerUI: true, ContentDir: testContent, APIToken: testToken})
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestMCPGetArticleRejectsEmptyAndDotOnlyIDsWithASearchHint(t *testing.T) {
	for name, id := range invalidArticleIDs {
		t.Run(name, func(t *testing.T) {
			srv, requests := countingServer(t)
			cs := session(t, srv.URL, testToken)
			text, isErr := call(t, cs, "get_article", map[string]any{"id": id})
			if !isErr {
				t.Errorf("get_article(%q): want a tool error, got %s", id, text)
			}
			if !strings.Contains(text, "search_knowledge") {
				t.Errorf("get_article(%q): hint does not point to search_knowledge: %s", id, text)
			}
			if got := requests.Load(); got != 0 {
				t.Errorf("get_article(%q): made %d HTTP requests, want none", id, got)
			}
		})
	}
}

func TestMCPGetArticleInvalidIDErrorLeaksNothing(t *testing.T) {
	for name, id := range invalidArticleIDs {
		t.Run(name, func(t *testing.T) {
			srv, _ := countingServer(t)
			u, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			cs := session(t, srv.URL, testToken)
			text, isErr := call(t, cs, "get_article", map[string]any{"id": id})
			if !isErr {
				t.Errorf("get_article(%q): want a tool error, got %s", id, text)
			}
			for _, bad := range []string{srv.URL, u.Host, "127.0.0.1", "localhost", "http://", "https://", "/api/", testToken} {
				if strings.Contains(text, bad) {
					t.Errorf("get_article(%q): error leaks %q: %s", id, bad, text)
				}
			}
		})
	}
}

// Negative case: ids that only contain dots among other characters, or dots
// with whitespace (not trimmed), are ordinary ids. They reach the server, and
// an unknown one gets the server's 404 and hint.
func TestMCPGetArticleOrdinaryIDsWithDotsStillReachTheServer(t *testing.T) {
	for name, id := range map[string]string{
		"dots inside":          "a..b",
		"leading dot":          ".hidden",
		"leading dots":         "..x",
		"dots with spaces":     " .. ",
		"one dot leader glyph": "\u2024\u2024",
	} {
		t.Run(name, func(t *testing.T) {
			srv, requests := countingServer(t)
			cs := session(t, srv.URL, testToken)
			text, isErr := call(t, cs, "get_article", map[string]any{"id": id})
			if !isErr || !strings.Contains(text, "no such article") || !strings.Contains(text, "Search for the article") {
				t.Errorf("get_article(%q): want the server's 404 message and hint as a tool error: %v %s", id, isErr, text)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("get_article(%q): made %d HTTP requests, want 1", id, got)
			}
		})
	}
}

// Negative case: a known id is still served, through exactly one request.
func TestMCPGetArticleKnownIDMakesOneRequest(t *testing.T) {
	srv, requests := countingServer(t)
	cs := session(t, srv.URL, testToken)
	text, isErr := call(t, cs, "get_article", map[string]any{"id": "agent-loops"})
	if isErr || !strings.Contains(text, "(id: agent-loops)") {
		t.Errorf("want the article, got %v %s", isErr, text)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("made %d HTTP requests, want 1", got)
	}
}
