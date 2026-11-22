package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// These tests cover #168: a panic in an API handler reached through /mcp is
// recovered into the 500 envelope and logged once, with the API request's
// method and path, so the operator the envelope points at finds an entry. The
// panic value never reaches the log.

// lockedBuffer is a log sink that tools on other goroutines can write to while
// the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// lines are the non-empty lines written so far.
func (b *lockedBuffer) lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, l := range strings.Split(b.buf.String(), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// panicSecret looks like a credential, so a log that carries the panic value
// is caught.
const panicSecret = "ghp_PanicValueSecret0123456789abcdefABCD"

// secretStringer panics with a value whose String carries the secret.
type secretStringer struct{}

func (secretStringer) String() string { return "stringer " + panicSecret }

// mcpServerWithAPI serves the public mux over snap with /mcp in front of api,
// logging to the returned buffer through the server's JSON logger.
func mcpServerWithAPI(t *testing.T, api func(m *publicMux) http.Handler) (*httptest.Server, *lockedBuffer) {
	t.Helper()
	snap, err := engine.NewDirSource(testContent).Load()
	if err != nil {
		t.Fatal(err)
	}
	m, err := newPublicMux(snap, testToken)
	if err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	addMCP(m, api(m), platform.NewLogger(logs, slog.LevelDebug, "server"))
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return srv, logs
}

// Criterion (#168): a panicking API handler reached through /mcp produces the
// existing 500 envelope and exactly one log line with the method and path of
// the API request. The line never contains the panic value, whatever its type,
// nor the caller's token.
func TestMCPOverHTTPLogsARecoveredPanicOnceWithoutItsValue(t *testing.T) {
	values := map[string]any{
		"string":   panicSecret,
		"error":    errors.New("wrapped: " + panicSecret),
		"stringer": secretStringer{},
		// A value shaped to forge a second log line if written raw.
		"forged line": panicSecret + "\n" + `{"level":"error","msg":"forged ` + panicSecret + `"}`,
	}
	tools := []struct {
		tool string
		args map[string]any
		path string
	}{
		{"get_article", map[string]any{"id": "agent-loops"}, "/api/v1/articles/agent-loops"},
		{"search_knowledge", map[string]any{"query": "agents"}, "/api/v1/search"},
	}
	for name, value := range values {
		for _, tc := range tools {
			t.Run(name+"/"+tc.tool, func(t *testing.T) {
				boom := func(*publicMux) http.Handler {
					return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(value) })
				}
				srv, logs := mcpServerWithAPI(t, boom)
				cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
				text, isErr := call(t, cs, tc.tool, tc.args)
				for _, want := range []string{"500", "the server could not answer", "ask the operator to check the server logs"} {
					if !isErr || !strings.Contains(text, want) {
						t.Errorf("tool error=%v lacks %q of the 500 envelope: %s", isErr, want, text)
					}
				}
				if strings.Contains(text, panicSecret) {
					t.Errorf("the tool error carries the panic value: %s", text)
				}
				got := logs.lines()
				if len(got) != 1 {
					t.Fatalf("got %d log lines, want exactly one for the recovered panic:\n%s", len(got), strings.Join(got, "\n"))
				}
				line := got[0]
				if !json.Valid([]byte(line)) {
					t.Errorf("the log line is not one JSON record: %s", line)
				}
				if !strings.Contains(line, http.MethodGet) || !strings.Contains(line, tc.path) {
					t.Errorf("the log line does not name the API request %s %s: %s", http.MethodGet, tc.path, line)
				}
				for _, bad := range []string{panicSecret, "forged", "stringer", "wrapped", testToken, "Bearer"} {
					if strings.Contains(line, bad) {
						t.Errorf("the log line carries %q: %s", bad, line)
					}
				}
			})
		}
	}
}

// Each recovered panic is its own line: two panicking calls are two lines,
// not one, and not more.
func TestMCPOverHTTPLogsEachRecoveredPanic(t *testing.T) {
	boom := func(*publicMux) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(panicSecret) })
	}
	srv, logs := mcpServerWithAPI(t, boom)
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	call(t, cs, "get_article", map[string]any{"id": "agent-loops"})
	call(t, cs, "search_knowledge", map[string]any{"query": "agents"})
	if got := logs.lines(); len(got) != 2 {
		t.Errorf("got %d log lines for two recovered panics, want two:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// The path is the caller's input: an article id built to forge a log line
// still gives exactly one JSON line.
func TestMCPOverHTTPPanicLogCannotBeForgedThroughTheArticleID(t *testing.T) {
	boom := func(*publicMux) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(panicSecret) })
	}
	srv, logs := mcpServerWithAPI(t, boom)
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	hostile := "agent-loops\n{\"level\":\"error\",\"msg\":\"forged\"}\r\n"
	if _, isErr := call(t, cs, "get_article", map[string]any{"id": hostile}); !isErr {
		t.Error("want the 500 tool error")
	}
	got := logs.lines()
	if len(got) != 1 {
		t.Fatalf("got %d log lines, want exactly one:\n%s", len(got), strings.Join(got, "\n"))
	}
	if !json.Valid([]byte(got[0])) || strings.Contains(got[0], panicSecret) {
		t.Errorf("the log line is not one clean JSON record: %s", got[0])
	}
}

// Negative: when the API answers, success or error envelope, nothing is
// logged for /mcp. Only a recovered panic is.
func TestMCPOverHTTPLogsNothingWhenTheAPIAnswers(t *testing.T) {
	srv, logs := mcpServerWithAPI(t, func(m *publicMux) http.Handler { return m })
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	assertToolsAnswerOverHTTP(t, cs)
	if _, isErr := call(t, cs, "get_article", map[string]any{"id": "no-such-article"}); !isErr {
		t.Error("want the 404 tool error")
	}
	if got := logs.lines(); len(got) != 0 {
		t.Errorf("got %d log lines with no panic, want none:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// The server hands PublicHandler its component logger, so the operator's log
// is where a recovered panic lands. A handler built with it serves /mcp and
// logs nothing while the API answers.
func TestPublicHandlerTakesTheServerLogger(t *testing.T) {
	logs := &lockedBuffer{}
	srv := newServerWith(t, Options{Logger: platform.NewLogger(logs, slog.LevelDebug, "server")})
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	assertToolsAnswerOverHTTP(t, cs)
	if got := logs.lines(); len(got) != 0 {
		t.Errorf("got %d log lines with no panic, want none:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// The MCP server advertises its capabilities explicitly: tools, and not the
// SDK's default logging, which nothing in goldie uses (#168).
func TestMCPOverHTTPAdvertisesToolsAndNotLogging(t *testing.T) {
	srv := newServer(t)
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	caps := cs.InitializeResult().Capabilities
	if caps == nil || caps.Tools == nil {
		t.Fatalf("the server does not advertise tools: %+v", caps)
	}
	//nolint:staticcheck // SA1019: checking the deprecated logging capability is absent is the point.
	if caps.Logging != nil {
		t.Errorf("the server advertises the logging capability: %+v", caps.Logging)
	}
}

// Criterion (#168): the MCP docs say the server advertises explicit
// capabilities and does not offer the SDK's default logging capability. The
// README's MCP sections are the MCP docs.
func TestMCPDocsSayLoggingIsNotOffered(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	var sections []string
	for _, s := range strings.Split(string(readme), "\n## ")[1:] {
		if strings.HasPrefix(s, "MCP") {
			sections = append(sections, s)
		}
	}
	if len(sections) == 0 {
		t.Fatal("README.md has no MCP section")
	}
	for _, s := range sections {
		for _, para := range strings.Split(s, "\n\n") {
			p := strings.ToLower(strings.Join(strings.Fields(para), " "))
			if strings.Contains(p, "capabilit") && strings.Contains(p, "logging") &&
				(strings.Contains(p, " not ") || strings.Contains(p, " no ")) {
				return
			}
		}
	}
	t.Error("README.md's MCP sections do not say, in one paragraph, that the server advertises explicit " +
		"capabilities and does not offer the SDK's default logging capability")
}
