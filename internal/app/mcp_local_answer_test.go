package app

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance tests for #236, review round 1: goldie mcp --dir words a 200
// that is not an API answer, and a redirect, as the embedded server's, with
// no address hint and nothing of the answer or the Location.

const (
	mcpLocalNotAPIAnswerWording = "the embedded server's answer isn't a goldie API answer"
	mcpLocalRedirectWording     = "the embedded server answered with a redirect, which is not followed"
)

// assertNoLocalAddressHint fails when a local-mode tool error points at an
// address, or carries the address, the token, the page or the Location.
func assertNoLocalAddressHint(t *testing.T, what, text, addr string) {
	t.Helper()
	low := strings.ToLower(text)
	for _, bad := range []string{"address", "not running", "--addr", "goldie_addr", "200 ok", "10.0.0.7", "\x1b"} {
		if strings.Contains(low, bad) {
			t.Errorf("%s: local-mode tool error carries %q: %q", what, bad, text)
		}
	}
	assertLeaksNoAddress(t, what, text, strings.TrimPrefix(addr, "http://"))
}

func TestMCPLocalModeNonJSON200IsTheEmbeddedServersAnswer(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"text/html login page":       mcpNotAPIAnswer("text/html; charset=utf-8", mcpLoginPage),
		"no content type":            mcpNotAPIAnswer("", mcpLoginPage),
		"JSON body under text/plain": mcpNotAPIAnswer("text/plain", `{"query":"x","results":[]}`),
	} {
		addr := mcpStub(t, h)
		cs := localStdioSession(t, addr)
		for tool, args := range toolsOverTheLimit {
			what := name + "/" + tool
			text, isErr := call(t, cs, tool, args)
			if !isErr || text != mcpLocalNotAPIAnswerWording {
				t.Errorf("%s: tool error %v, text %q, want %q", what, isErr, text, mcpLocalNotAPIAnswerWording)
			}
			assertNoLocalAddressHint(t, what, text, addr)
		}
	}
}

func TestMCPLocalModeRedirectIsTheEmbeddedServers(t *testing.T) {
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		target := newTokenTarget(t)
		addr := mcpStub(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", target.srv.URL+"/leak-marker-location"+r.URL.RequestURI())
			w.WriteHeader(code)
		})
		cs := localStdioSession(t, addr)
		for tool, args := range toolsOverTheLimit {
			what := http.StatusText(code) + "/" + tool
			text, isErr := call(t, cs, tool, args)
			if !isErr || text != mcpLocalRedirectWording {
				t.Errorf("%s: tool error %v, text %q, want %q", what, isErr, text, mcpLocalRedirectWording)
			}
			assertNoLocalAddressHint(t, what, text, addr)
			assertLeaksNoAddress(t, what, text, strings.TrimPrefix(target.srv.URL, "http://"))
		}
		if got := target.seen(); len(got) != 0 {
			t.Errorf("%s: goldie mcp --dir followed a redirect: the target saw %d requests", http.StatusText(code), len(got))
		}
	}
}
