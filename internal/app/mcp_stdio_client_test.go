package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/cli"
)

// Acceptance tests for #182: goldie mcp over stdio reaches the API through
// the hardened client in contract, so a tool call words a redirect and an
// oversized answer precisely, instead of saying the server is not running.
// The cut is RunMCP, the stdio role, with the MCP client on pipes for its
// stdin and stdout. Neither message carries a URL or a Location value.

// redirectWording is the fixed text of the redirect tool error.
const redirectWording = "answered with a redirect; check the address"

// answerCap is the cap on every answer body the client reads.
const answerCap = 16 << 20

// stdioSession runs RunMCP against addr with testToken and connects an MCP
// client to its stdin and stdout.
func stdioSession(t *testing.T, addr string) *mcp.ClientSession {
	t.Helper()
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	env := cli.Env{Lookup: func(string) (string, bool) { return "", false }, ReadFile: os.ReadFile, Out: io.Discard}
	done := make(chan error, 1)
	go func() {
		done <- RunMCP(ctx, []string{"--transport", "stdio", "--addr", addr, "--token", testToken}, env, serverIn, serverOut)
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: clientIn, Writer: clientOut}, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect to RunMCP: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		cancel()
		_ = clientOut.Close()
		_ = clientIn.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("RunMCP: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("RunMCP did not return after the session closed")
		}
	})
	return cs
}

// tokenTarget records the Authorization header of every request it gets.
type tokenTarget struct {
	mu    sync.Mutex
	auths []string
	srv   *httptest.Server
}

func (tg *tokenTarget) seen() []string {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return append([]string(nil), tg.auths...)
}

func newTokenTarget(t *testing.T) *tokenTarget {
	t.Helper()
	tg := &tokenTarget{}
	tg.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tg.mu.Lock()
		tg.auths = append(tg.auths, r.Header.Get("Authorization"))
		tg.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
	}))
	t.Cleanup(tg.srv.Close)
	return tg
}

// assertLeaksNoAddress fails when text carries a URL, an address, a Location
// value or the token.
func assertLeaksNoAddress(t *testing.T, what, text string, extra ...string) {
	t.Helper()
	for _, bad := range append([]string{"http://", "https://", "127.0.0.1", "localhost", "leak-marker", "Location", testToken, "/api/"}, extra...) {
		if strings.Contains(text, bad) {
			t.Errorf("%s: the tool error leaks %q: %s", what, bad, text)
		}
	}
}

// Criterion: a tool call against a server that redirects is a tool error
// saying the server answered with a redirect and to check the address. The
// redirect is never followed, so the target never sees the token, and the
// text has neither the server's address nor the Location value.
func TestMCPToolCallAgainstARedirectingServerSaysCheckTheAddress(t *testing.T) {
	target := newTokenTarget(t)
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		from := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", target.srv.URL+"/leak-marker-location"+r.URL.RequestURI())
			w.WriteHeader(code)
		}))
		t.Cleanup(from.Close)
		cs := stdioSession(t, from.URL)
		for tool, args := range toolsOverTheLimit {
			text, isErr := call(t, cs, tool, args)
			what := http.StatusText(code) + "/" + tool
			if !isErr {
				t.Errorf("%s: want a tool error, got %s", what, text)
				continue
			}
			if !strings.Contains(text, redirectWording) {
				t.Errorf("%s: the tool error does not say %q: %s", what, redirectWording, text)
			}
			if strings.Contains(text, "not running") {
				t.Errorf("%s: the tool error calls a redirect \"not running\": %s", what, text)
			}
			assertLeaksNoAddress(t, what, text, strings.TrimPrefix(from.URL, "http://"), strings.TrimPrefix(target.srv.URL, "http://"))
		}
	}
	if got := target.seen(); len(got) != 0 {
		t.Errorf("goldie mcp followed a redirect: the target saw %d requests with Authorization %q", len(got), got)
	}
}

// paddedAnswer answers every request with status and a JSON search answer
// padded with spaces to exactly n bytes.
func paddedAnswer(t *testing.T, status, n int) string {
	t.Helper()
	head := []byte(`{"query":"x","results":[]}`)
	body := append(head, bytes.Repeat([]byte(" "), n-len(head))...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Criterion: an answer above 16 MiB gets its own fixed message: the same text
// for both tools, for any oversized size and for a 200 as for an error
// status. It is neither the redirect message nor the "not running" one an
// unreachable server gets, and it carries no address.
func TestMCPToolCallWithAnOversizedAnswerHasItsOwnFixedMessage(t *testing.T) {
	unreachable, isErr := call(t, stdioSession(t, "http://127.0.0.1:1"), "search_knowledge", map[string]any{"query": "x"})
	if !isErr {
		t.Fatalf("an unreachable server: want a tool error, got %s", unreachable)
	}
	var fixed string
	for _, c := range []struct {
		name         string
		status, size int
	}{
		{"200, 16 MiB + 1 byte", http.StatusOK, answerCap + 1},
		{"200, 17 MiB", http.StatusOK, answerCap + 1<<20},
		{"404, 16 MiB + 1 byte", http.StatusNotFound, answerCap + 1},
	} {
		addr := paddedAnswer(t, c.status, c.size)
		cs := stdioSession(t, addr)
		for tool, args := range toolsOverTheLimit {
			text, isErr := call(t, cs, tool, args)
			what := c.name + "/" + tool
			if !isErr {
				t.Errorf("%s: want a tool error, got %d bytes of text", what, len(text))
				continue
			}
			if text == unreachable || strings.Contains(text, "not running") || strings.Contains(text, "cannot reach") {
				t.Errorf("%s: an oversized answer is worded as an unreachable server: %s", what, text)
			}
			if strings.Contains(text, "redirect") {
				t.Errorf("%s: an oversized answer is worded as a redirect: %s", what, text)
			}
			assertLeaksNoAddress(t, what, text, strings.TrimPrefix(addr, "http://"))
			if fixed == "" {
				fixed = text
			} else if text != fixed {
				t.Errorf("%s: the oversized message is not fixed:\n got %q\nwant %q", what, text, fixed)
			}
		}
	}
}

// Negative: an answer of exactly 16 MiB is within the cap and is answered.
func TestMCPToolCallWithAnAnswerOfExactly16MiBSucceeds(t *testing.T) {
	cs := stdioSession(t, paddedAnswer(t, http.StatusOK, answerCap))
	text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "x"})
	if isErr || !strings.Contains(text, "No articles match") {
		t.Errorf("an answer of exactly 16 MiB: tool error %v, text %q, want the empty search result", isErr, text)
	}
}

// Criterion (from #200): a Retry-After above 3600 seconds is an unknown
// wait, so the agent is told to wait a little, never the server's number.
func TestRateLimitedToolCallWithAWaitAboveAnHourSaysWaitALittle(t *testing.T) {
	for _, secs := range []string{"3601", "86400"} {
		cs := session(t, rateLimitedAPI(t, secs), testToken)
		for tool, args := range toolsOverTheLimit {
			text, isErr := call(t, cs, tool, args)
			if !isErr {
				t.Errorf("Retry-After %s, %s: want a tool error, got %s", secs, tool, text)
				continue
			}
			if !strings.Contains(text, "Wait a little") {
				t.Errorf("Retry-After %s, %s: the tool error does not fall back to \"Wait a little\": %s", secs, tool, text)
			}
			if strings.Contains(text, secs) {
				t.Errorf("Retry-After %s, %s: the tool error passes on the wait above an hour: %s", secs, tool, text)
			}
			assertNoHeaderPointer(t, tool, text)
		}
	}
}
