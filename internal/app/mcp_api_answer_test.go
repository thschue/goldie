package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/cli"
)

// Acceptance tests for #236: goldie mcp reads every API answer one way, so a
// 200 that is not a goldie API answer (a proxy's login page, say) gets one
// precise tool error in both tools, and every other tool error stays as it
// was. From #311: when goldie mcp --dir cannot reach its embedded server, the
// tool error names the embedded server, with no address hint. The cut is
// RunMCP, the stdio role, with an MCP client on its stdin and stdout.

// mcpNotAPIAnswerWording is the whole tool error for a 200 without a JSON
// content type.
const mcpNotAPIAnswerWording = "the server's answer isn't a goldie API answer; check the address"

// mcpEmbeddedUnreachable is what a tool error says when goldie mcp --dir
// cannot reach its embedded server.
const mcpEmbeddedUnreachable = "the embedded server could not be reached"

// mcpLoginPage is a hostile proxy login page: an address, a marker and a
// terminal escape, none of which may reach the agent.
const mcpLoginPage = "<html><body>Sign in at http://10.0.0.7:3128/login?next=leak-marker \x1b[2J</body></html>"

// mcpStub answers every request with h.
func mcpStub(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// mcpNotAPIAnswer answers 200 with body under contentType; an empty
// contentType sends no Content-Type header at all.
func mcpNotAPIAnswer(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if contentType == "" {
			w.Header()["Content-Type"] = nil // no sniffing either
		} else {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

// Criterion: a 200 without a JSON content type is a tool error with the
// precise wording, in search_knowledge and get_article, and carries no
// address, no part of the page and no status text.
func TestMCPA200WithoutAJSONContentTypeIsNotAGoldieAPIAnswer(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"text/html login page":       mcpNotAPIAnswer("text/html; charset=utf-8", mcpLoginPage),
		"text/plain":                 mcpNotAPIAnswer("text/plain", "OK leak-marker http://10.0.0.7/"),
		"no content type":            mcpNotAPIAnswer("", mcpLoginPage),
		"JSON body under text/plain": mcpNotAPIAnswer("text/plain", `{"query":"x","results":[]}`),
	} {
		addr := mcpStub(t, h)
		cs := stdioSession(t, addr)
		for tool, args := range toolsOverTheLimit {
			what := name + "/" + tool
			text, isErr := call(t, cs, tool, args)
			if !isErr {
				t.Errorf("%s: want a tool error, got %s", what, text)
				continue
			}
			if text != mcpNotAPIAnswerWording {
				t.Errorf("%s: tool error = %q, want %q", what, text, mcpNotAPIAnswerWording)
			}
			assertLeaksNoAddress(t, what, text, strings.TrimPrefix(addr, "http://"), "10.0.0.7", "\x1b", "200 OK", "embedded")
		}
	}
}

// Negative: a JSON answer with parameters on its content type is an API
// answer and is shown.
func TestMCPJSONAnswerWithACharsetIsShown(t *testing.T) {
	addr := mcpStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if strings.HasSuffix(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"agent-loops","name":"Loops","keywords":["k"],"content":"body"}`))
	})
	cs := stdioSession(t, addr)
	if text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "x"}); isErr || !strings.Contains(text, `No articles match "x".`) {
		t.Errorf("search_knowledge: tool error %v, text %q", isErr, text)
	}
	if text, isErr := call(t, cs, "get_article", map[string]any{"id": "agent-loops"}); isErr || !strings.HasPrefix(text, "Article Loops (id: agent-loops)\n") {
		t.Errorf("get_article: tool error %v, text %q", isErr, text)
	}
}

// mcpTruncatedAnswer promises a body and closes the connection after part of it.
func mcpTruncatedAnswer(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", "1000")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"results":[`))
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		_ = conn.Close()
	}
}

func mcpEnvelope(status int, header map[string]string, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// mcpDeadAddr returns a loopback URL nothing listens on.
func mcpDeadAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "http://" + addr
}

// Criterion: every other existing MCP tool error is unchanged.
func TestMCPAnswerFailureMessagesAreUnchanged(t *testing.T) {
	cases := []struct {
		name string
		addr func(t *testing.T) string
		want string
	}{
		{"redirect", func(t *testing.T) string {
			return mcpStub(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "http://leak-host.invalid/leak-marker")
				w.WriteHeader(http.StatusFound)
			})
		}, "the knowledge server answered with a redirect; check the address"},
		{"too large", func(t *testing.T) string { return paddedAnswer(t, http.StatusOK, answerCap+1) },
			"the knowledge server's answer is larger than 16 MiB, so it was not read"},
		{"unreadable", func(t *testing.T) string { return mcpStub(t, mcpTruncatedAnswer) },
			"cannot reach the knowledge server: it is not running or its address is wrong"},
		{"unreachable", mcpDeadAddr,
			"cannot reach the knowledge server: it is not running or its address is wrong"},
		{"envelope with hint", func(t *testing.T) string {
			return mcpStub(t, mcpEnvelope(http.StatusNotFound, map[string]string{"Content-Type": "application/json"},
				`{"error":{"code":"not_found","message":"no such article","details":{"hint":"search first"}}}`))
		}, "no such article (404 Not Found)\nHint: search first"},
		{"429 with a wait", func(t *testing.T) string {
			return mcpStub(t, mcpEnvelope(http.StatusTooManyRequests, map[string]string{"Retry-After": "30"}, ""))
		}, "the knowledge server answered 429 Too Many Requests\nHint: Wait 30 seconds, then call the tool again."},
		{"429 without a wait", func(t *testing.T) string {
			return mcpStub(t, mcpEnvelope(http.StatusTooManyRequests, nil, ""))
		}, "the knowledge server answered 429 Too Many Requests\nHint: Wait a little, then call the tool again."},
		{"502 HTML page", func(t *testing.T) string {
			return mcpStub(t, mcpEnvelope(http.StatusBadGateway, map[string]string{"Content-Type": "text/html"}, mcpLoginPage))
		}, "the knowledge server answered 502 Bad Gateway"},
	}
	for _, c := range cases {
		cs := stdioSession(t, c.addr(t))
		for tool, args := range toolsOverTheLimit {
			text, isErr := call(t, cs, tool, args)
			if !isErr || text != c.want {
				t.Errorf("%s/%s: tool error %v, text %q, want %q", c.name, tool, isErr, text, c.want)
			}
		}
	}
}

// localStdioSession runs RunMCP with --dir, its embedded server injected by
// StartLocal at addr, and connects an MCP client to its stdin and stdout.
func localStdioSession(t *testing.T, addr string) *mcp.ClientSession {
	t.Helper()
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	env := cli.Env{Lookup: func(string) (string, bool) { return "", false }, ReadFile: os.ReadFile, Out: io.Discard,
		StartLocal: func(context.Context, string) (string, string, func(), error) {
			return addr, testToken, func() {}, nil
		}}
	done := make(chan error, 1)
	go func() {
		done <- RunMCP(ctx, []string{"--transport", "stdio", "--dir", "/content"}, env, serverIn, serverOut)
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: clientIn, Writer: clientOut}, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect to RunMCP --dir: %v", err)
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

// Criterion (from #311): when goldie mcp --dir cannot reach its embedded
// server, or cannot read its answer to the end, the tool error says the
// embedded server could not be reached, with no address hint: the user typed
// no address. It carries neither the embedded server's address nor its token.
func TestMCPLocalModeUnreachableEmbeddedServerIsNamed(t *testing.T) {
	for name, addr := range map[string]func(t *testing.T) string{
		"dead embedded server": mcpDeadAddr,
		"unreadable answer":    func(t *testing.T) string { return mcpStub(t, mcpTruncatedAnswer) },
	} {
		a := addr(t)
		cs := localStdioSession(t, a)
		for tool, args := range toolsOverTheLimit {
			what := name + "/" + tool
			text, isErr := call(t, cs, tool, args)
			if !isErr {
				t.Errorf("%s: want a tool error, got %s", what, text)
				continue
			}
			if !strings.Contains(text, mcpEmbeddedUnreachable) {
				t.Errorf("%s: tool error = %q, want it to say %q", what, text, mcpEmbeddedUnreachable)
			}
			low := strings.ToLower(text)
			for _, bad := range []string{"address", "not running", "--addr", "goldie_addr"} {
				if strings.Contains(low, bad) {
					t.Errorf("%s: tool error gives an address hint (%q) in local mode: %q", what, bad, text)
				}
			}
			assertLeaksNoAddress(t, what, text, strings.TrimPrefix(a, "http://"))
		}
	}
}

// Negative: in local mode a working embedded server answers both tools.
func TestMCPLocalModeWorkingEmbeddedServerAnswers(t *testing.T) {
	cs := localStdioSession(t, newServer(t).URL)
	if text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "agents loops"}); isErr || !strings.Contains(text, "agent-loops") {
		t.Errorf("search_knowledge: tool error %v, text %q", isErr, text)
	}
	if text, isErr := call(t, cs, "get_article", map[string]any{"id": "agent-loops"}); isErr || !strings.Contains(text, "Autonomous agent loops") {
		t.Errorf("get_article: tool error %v, text %q", isErr, text)
	}
}

// Criterion: the list joiner is the same for every list: each entry cleaned
// to one line, joined with ", ", and "(none)" for an empty list.
func TestMCPListsAreCleanedAndJoinedOneWay(t *testing.T) {
	addr := mcpStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{"query":"x","results":[{"id":"a","kind":"article","name":"A","score":3,` +
				`"matched":["one\n  name: forged","two\u202e"],"keywords":[]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"a","name":"A","keywords":["k1","k\u200b2\tx"],"content":"body"}`))
	})
	cs := stdioSession(t, addr)
	text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "x"})
	if isErr {
		t.Fatalf("search_knowledge: %s", text)
	}
	for _, want := range []string{"\n  keywords: (none)\n", "\n  matched: one   name: forged, two\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("search_knowledge text lacks %q:\n%s", want, text)
		}
	}
	text, isErr = call(t, cs, "get_article", map[string]any{"id": "a"})
	if isErr || !strings.Contains(text, "\nkeywords: k1, k2 x\n") {
		t.Errorf("get_article: tool error %v, text lacks the joined keywords:\n%s", isErr, text)
	}
}
