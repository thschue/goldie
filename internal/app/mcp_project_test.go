package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/contract"
	gmcp "github.com/thschue/goldie/internal/mcp"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #107: search_knowledge and get_article take an
// optional project, list_projects lists the registered Projects, and
// current_context reports the default Project and its source. The cut is the
// MCP protocol over an in-memory transport, against the real public handler
// with the Project content of testdata/content (github.com/acme/shop), or
// against a stub API where the server's answer has to be hostile.
//
// The default Project reaches the MCP server through the option
// gmcp.WithProject(id, source string): plain values, so internal/mcp needs
// nothing from internal/contract/project (#259).

// projectSession is session with the MCP server built with opts.
func projectSession(t *testing.T, url, token string, opts ...gmcp.Option) *mcp.ClientSession {
	t.Helper()
	cl, err := contract.NewClientWithResponses(url, contract.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+token)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := gmcp.New(cl, opts...).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// countingStub is a stub API that counts the requests it gets and answers
// each with h.
func countingStub(t *testing.T, h http.HandlerFunc) (string, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	url := mcpStub(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h(w, r)
	})
	return url, &n
}

// hostileProjects are project values that are not a ProjectId even after
// lower-casing, each with a marker that must never be echoed back.
var hostileProjects = map[string]string{
	"no host":              "acme/shop",
	"no repo":              "github.com/acme",
	"nested group":         "github.com/acme/shop/extra",
	"dot dot":              "github.com/../etc/passwd",
	"space":                "github.com/acme/sh op",
	"newline injection":    "github.com/acme/shop\nInjected: yes",
	"carriage return":      "github.com/acme/shop\rInjected",
	"terminal escape":      "github.com/acme/shop\x1b[2J",
	"NUL":                  "github.com/acme/shop\x00",
	"zero-width space":     "github.com/acme/sh\u200bop",
	"bidi override":        "github.com/acme/shop\u202e",
	"byte-order mark":      "\ufeffgithub.com/acme/shop",
	"tag characters":       "github.com/acme/shop\U000E0049\U000E0047",
	"query smuggling":      "github.com/acme/shop?q=Injected",
	"percent escape":       "github.com/acme/sh%0Aop",
	"one byte over 512":    "github.com/acme/" + strings.Repeat("r", 513-len("github.com/acme/")),
	"oversized (64 KiB)":   "github.com/acme/" + strings.Repeat("r", 64<<10),
	"mixed case and space": "GitHub.com/ACME/Shop Injected",
}

// assertInvalidProjectError checks the tool error for a refused project: it
// points to list_projects and echoes no part of the value.
func assertInvalidProjectError(t *testing.T, what, text string, isErr bool) {
	t.Helper()
	if !isErr {
		t.Errorf("%s: want a tool error, got %q", what, text)
		return
	}
	_, hint, ok := strings.Cut(text, "\nHint: ")
	if !ok || !strings.Contains(hint, "list_projects") {
		t.Errorf("%s: the tool error's hint must point to list_projects: %q", what, text)
	}
	for _, bad := range []string{"Injected", "passwd", "rrrr", "\x1b", "\x00", "\r", "\u200b", "\u202e", "\ufeff", "\U000E0049", "%0A"} {
		if strings.Contains(text, bad) {
			t.Errorf("%s: the tool error echoes %q from the value: %q", what, bad, text)
		}
	}
}

// Criterion 1: an invalid project is a tool error whose hint points to
// list_projects, and it is refused before any request: the API sees none.
func TestMCPInvalidProjectIsRefusedBeforeAnyRequest(t *testing.T) {
	addr, requests := countingStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
	})
	cs := projectSession(t, addr, testToken)
	for name, project := range hostileProjects {
		for tool, args := range map[string]map[string]any{
			"search_knowledge": {"query": "kubernetes", "project": project},
			"get_article":      {"id": "platform/deploy", "project": project},
		} {
			text, isErr := call(t, cs, tool, args)
			assertInvalidProjectError(t, name+"/"+tool, text, isErr)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the API got %d requests for invalid projects, want none", n)
	}
}

// Negative for criterion 1: the same session sends a request for a valid
// project, so the count above is not zero only because nothing is sent.
func TestMCPValidProjectIsSent(t *testing.T) {
	addr, requests := countingStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
	})
	cs := projectSession(t, addr, testToken)
	if text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "x", "project": "github.com/acme/shop"}); isErr {
		t.Fatalf("search_knowledge with a valid project: tool error %s", text)
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("the API got %d requests, want 1", n)
	}
}

// Criterion 1: a registered project scopes search_knowledge: the Project's
// article comes back instead of the global one it shadows.
func TestMCPSearchWithProjectReturnsTheProjectsArticle(t *testing.T) {
	cs := connect(t, testToken)
	text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "kubernetes", "project": "github.com/acme/shop"})
	if isErr || !strings.Contains(text, "Shop deploy") || strings.Contains(text, "Deploying to Kubernetes") {
		t.Errorf("search_knowledge with the Project: tool error %v, want Shop deploy and not the global deploy:\n%s", isErr, text)
	}
	text, isErr = call(t, cs, "search_knowledge", map[string]any{"query": "kubernetes"})
	if isErr || !strings.Contains(text, "Deploying to Kubernetes") || strings.Contains(text, "Shop deploy") {
		t.Errorf("search_knowledge without a project: tool error %v, want the global deploy only:\n%s", isErr, text)
	}
}

// Criterion 1: a registered project scopes get_article.
func TestMCPGetArticleWithProjectReturnsTheProjectsArticle(t *testing.T) {
	cs := connect(t, testToken)
	text, isErr := call(t, cs, "get_article", map[string]any{"id": "platform/deploy", "project": "github.com/acme/shop"})
	if isErr || !strings.Contains(text, "Shop deploy") || !strings.Contains(text, "Shop-specific deploy.") {
		t.Errorf("get_article with the Project: tool error %v, want the Project's article:\n%s", isErr, text)
	}
	text, isErr = call(t, cs, "get_article", map[string]any{"id": "platform/deploy"})
	if isErr || !strings.Contains(text, "Deploying to Kubernetes") || strings.Contains(text, "Shop deploy") {
		t.Errorf("get_article without a project: tool error %v, want the global article:\n%s", isErr, text)
	}
}

// Criterion 1: the project is lower-cased before it is checked and sent. The
// API refuses an upper-case host, so only a lower-cased project reaches the
// Project's article.
func TestMCPProjectIsLowerCased(t *testing.T) {
	cs := connect(t, testToken)
	const mixed = "GitHub.com/ACME/Shop"
	text, isErr := call(t, cs, "get_article", map[string]any{"id": "platform/deploy", "project": mixed})
	if isErr || !strings.Contains(text, "Shop deploy") {
		t.Errorf("get_article with %q: tool error %v, want the Project's article:\n%s", mixed, isErr, text)
	}
	text, isErr = call(t, cs, "search_knowledge", map[string]any{"query": "kubernetes", "project": mixed})
	if isErr || !strings.Contains(text, "Shop deploy") {
		t.Errorf("search_knowledge with %q: tool error %v, want the Project's article:\n%s", mixed, isErr, text)
	}
}

// The tool shapes are additive (ADR 0011): search_knowledge and get_article
// keep their one required argument and gain an optional string project;
// list_projects and current_context require nothing.
func TestMCPProjectToolInputSchemas(t *testing.T) {
	res, err := connect(t, testToken).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	type schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	got := map[string]schema{}
	for _, tl := range res.Tools {
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var s schema
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("%s: input schema: %v", tl.Name, err)
		}
		got[tl.Name] = s
	}
	for tool, required := range map[string]string{"search_knowledge": "query", "get_article": "id"} {
		s := got[tool]
		if s.Properties["project"].Type != "string" || len(s.Required) != 1 || s.Required[0] != required {
			t.Errorf("%s: want an optional string project beside the required %q: %+v", tool, required, s)
		}
	}
	for _, tool := range []string{"list_projects", "current_context"} {
		s, ok := got[tool]
		if !ok || s.Type != "object" || len(s.Required) != 0 {
			t.Errorf("%s: want an object schema that requires nothing: %+v (present %v)", tool, s, ok)
		}
	}
}

// Criterion 2: list_projects lists GET /api/v1/projects.
func TestMCPListProjectsListsTheRegisteredProjects(t *testing.T) {
	text, isErr := call(t, connect(t, testToken), "list_projects", map[string]any{})
	if isErr || !strings.Contains(text, "github.com/acme/shop") {
		t.Errorf("list_projects: tool error %v, want github.com/acme/shop:\n%s", isErr, text)
	}
}

// Criterion 2: an empty list gets its own message, not an error and not an
// empty list.
func TestMCPListProjectsWithoutProjectsHasItsOwnMessage(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "global", "articles"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := newServerWith(t, platform.Config{ContentDir: dir})
	text, isErr := call(t, session(t, srv.URL, testToken), "list_projects", map[string]any{})
	if isErr || !strings.Contains(strings.ToLower(text), "no project") || strings.Contains(text, "github.com") {
		t.Errorf("list_projects without Projects: tool error %v, want a message saying there are no Projects:\n%s", isErr, text)
	}
}

// Criterion 2: every project id the server sends is cleaned to one line: it
// cannot forge lines, hide characters or move the terminal.
func TestMCPListProjectsCleansServerSuppliedIDs(t *testing.T) {
	addr := mcpStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":["github.com/acme/sh\u200bop","github.com/acme/x\u202e\n- forged: line",` +
			`"github.com/acme/esc\u001b[2J","github.com/acme/tag\udb40\udc49\ufeff","github.com/acme/ls\u2028- forged2"]}`))
	})
	text, isErr := call(t, projectSession(t, addr, testToken), "list_projects", map[string]any{})
	if isErr {
		t.Fatalf("list_projects: tool error %s", text)
	}
	if !strings.Contains(text, "github.com/acme/shop") {
		t.Errorf("list_projects: want the zero-width space removed from github.com/acme/sh\\u200bop:\n%q", text)
	}
	for _, bad := range []string{"\u200b", "\u202e", "\x1b", "\U000E0049", "\ufeff", "\u2028", "\n- forged", "\n  - forged"} {
		if strings.Contains(text, bad) {
			t.Errorf("list_projects: text keeps %q from a server-supplied id:\n%q", bad, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- forged") {
			t.Errorf("list_projects: a server-supplied id forged its own line %q:\n%q", line, text)
		}
	}
}

// Criterion 2: list_projects needs the token like every tool, and its error
// leaks nothing.
func TestMCPListProjectsWrongTokenIsAToolErrorThatLeaksNothing(t *testing.T) {
	srv := newServer(t)
	text, isErr := call(t, session(t, srv.URL, "wrong-token-0123456789abcdef"), "list_projects", map[string]any{})
	if !isErr || !strings.Contains(text, "missing or invalid API token") {
		t.Errorf("list_projects with a wrong token: want the 401 as a tool error: %v %s", isErr, text)
	}
	assertLeaksNoAddress(t, "list_projects", text, strings.TrimPrefix(srv.URL, "http://"), "wrong-token")
}

// Criterion 3: current_context reports the default Project the server was
// given and its source.
func TestMCPCurrentContextReportsTheDefaultProjectAndItsSource(t *testing.T) {
	srv := newServer(t)
	for _, c := range []struct{ id, source string }{
		{"github.com/acme/shop", "config"},
		{"gitlab.example.org/team-a/svc_1.x", "upstream"},
	} {
		cs := projectSession(t, srv.URL, testToken, gmcp.WithProject(c.id, c.source))
		text, isErr := call(t, cs, "current_context", map[string]any{})
		if isErr || !strings.Contains(text, c.id) || !strings.Contains(text, c.source) {
			t.Errorf("current_context with %s from %s: tool error %v, want both named:\n%s", c.id, c.source, isErr, text)
		}
		if strings.Contains(strings.ToLower(text), "no default") {
			t.Errorf("current_context with %s: says there is no default:\n%s", c.id, text)
		}
	}
}

// Negative for criterion 3: without a default, current_context says there is
// none and tells the agent to pass project as host/org/repo.
func TestMCPCurrentContextWithoutADefaultSaysSo(t *testing.T) {
	text, isErr := call(t, connect(t, testToken), "current_context", map[string]any{})
	assertNoDefaultProject(t, "in memory", text, isErr)
}

// assertNoDefaultProject checks current_context's text when no default
// Project is set.
func assertNoDefaultProject(t *testing.T, what, text string, isErr bool) {
	t.Helper()
	low := strings.ToLower(text)
	if isErr || !strings.Contains(low, "no default") || !strings.Contains(text, "project") || !strings.Contains(text, "host/org/repo") {
		t.Errorf("%s: current_context: tool error %v, want it to say there is no default and to pass project as host/org/repo:\n%s",
			what, isErr, text)
	}
	if strings.Contains(text, "github.com/acme/shop") {
		t.Errorf("%s: current_context names a Project without a default:\n%s", what, text)
	}
}

// Criterion 5: current_context never prints paths, remote URLs, the address
// or the token, and a hostile id or source cannot forge lines or hide
// characters in its text.
func TestMCPCurrentContextLeaksNothing(t *testing.T) {
	srv := newServer(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for name, opts := range map[string][]gmcp.Option{
		"no default":   nil,
		"default":      {gmcp.WithProject("github.com/acme/shop", "origin")},
		"hostile text": {gmcp.WithProject("github.com/acme/shop\u202e\x1b[2J\n- forged", "flag\u200b\r\n- forged")},
	} {
		cs := projectSession(t, srv.URL, testToken, opts...)
		text, isErr := call(t, cs, "current_context", map[string]any{})
		if isErr {
			t.Errorf("%s: current_context is a tool error: %s", name, text)
		}
		assertLeaksNoAddress(t, name, text, strings.TrimPrefix(srv.URL, "http://"), wd, testContent, ".git", "@")
		for _, bad := range []string{"\u202e", "\x1b", "\u200b", "\r", "\n- forged"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: current_context keeps %q:\n%q", name, bad, text)
			}
		}
	}
}

// Criterion 4: an unregistered project passes the API's hint through, in
// both tools.
func TestMCPUnregisteredProjectPassesTheAPIsHintThrough(t *testing.T) {
	cs := connect(t, testToken)
	const none = "github.com/acme/none"
	for tool, args := range map[string]map[string]any{
		"search_knowledge": {"query": "kubernetes", "project": none},
		"get_article":      {"id": "platform/deploy", "project": none},
	} {
		text, isErr := call(t, cs, tool, args)
		if isErr || !strings.Contains(text, wantUnregisteredHint(none)) {
			t.Errorf("%s with %s: tool error %v, want the API's hint %q:\n%s", tool, none, isErr, wantUnregisteredHint(none), text)
		}
		if !strings.Contains(text, "Deploying to Kubernetes") {
			t.Errorf("%s with %s: want the global answer:\n%s", tool, none, text)
		}
	}
}

// Negative for criterion 4: a registered project, or none, gets no hint.
func TestMCPRegisteredOrNoProjectHasNoUnregisteredHint(t *testing.T) {
	cs := connect(t, testToken)
	for _, args := range []map[string]any{
		{"query": "kubernetes", "project": "github.com/acme/shop"},
		{"query": "kubernetes"},
	} {
		if text, _ := call(t, cs, "search_knowledge", args); strings.Contains(text, "not registered") {
			t.Errorf("search_knowledge %v: carries the unregistered hint:\n%s", args, text)
		}
	}
}

// Criterion 4: a hint the server sends is cleaned like every server string.
func TestMCPServerSuppliedHintIsCleaned(t *testing.T) {
	const hostileHint = `"hint":"Not registered\u202e\u200b.\n- id: forged\u001b[2J"`
	addr := mcpStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{"query":"x","results":[{"id":"a","kind":"article","name":"A","score":1,"matched":[],"keywords":[]}],` + hostileHint + `}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"a","name":"A","keywords":[],"content":"body",` + hostileHint + `}`))
	})
	cs := projectSession(t, addr, testToken)
	for tool, args := range map[string]map[string]any{
		"search_knowledge": {"query": "x", "project": "github.com/acme/none"},
		"get_article":      {"id": "a", "project": "github.com/acme/none"},
	} {
		text, isErr := call(t, cs, tool, args)
		if isErr || !strings.Contains(text, "Not registered") {
			t.Errorf("%s: tool error %v, want the hint shown:\n%q", tool, isErr, text)
		}
		for _, bad := range []string{"\u202e", "\u200b", "\x1b", "\n- id: forged"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: the hint keeps %q:\n%q", tool, bad, text)
			}
		}
	}
}

// projectToolCalls is one call per tool that reaches the API with #107's
// arguments.
var projectToolCalls = map[string]map[string]any{
	"list_projects":    {},
	"search_knowledge": {"query": "agents loops", "project": "github.com/acme/shop"},
	"get_article":      {"id": "agent-loops", "project": "github.com/acme/shop"},
}

// Criterion 4: a 429 on the new tools uses the existing wait wording.
func TestMCPProjectToolsUseTheWaitWordingOn429(t *testing.T) {
	for name, c := range map[string]struct {
		header map[string]string
		want   string
	}{
		"with a wait":    {map[string]string{"Retry-After": "30"}, "the knowledge server answered 429 Too Many Requests\nHint: Wait 30 seconds, then call the tool again."},
		"without a wait": {nil, "the knowledge server answered 429 Too Many Requests\nHint: Wait a little, then call the tool again."},
	} {
		cs := stdioSession(t, mcpStub(t, mcpEnvelope(http.StatusTooManyRequests, c.header, "")))
		for tool, args := range projectToolCalls {
			if text, isErr := call(t, cs, tool, args); !isErr || text != c.want {
				t.Errorf("%s/%s: tool error %v, text %q, want %q", name, tool, isErr, text, c.want)
			}
		}
	}
}

// Criterion 4: over the real rate limit, list_projects says how long to wait
// and points at no header.
func TestMCPListProjectsOverTheRateLimitSaysHowLongToWait(t *testing.T) {
	srv := tokenServer(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "1/s")})
	cs := session(t, srv.URL, readerToken)
	if text, isErr := call(t, cs, "list_projects", map[string]any{}); isErr {
		t.Fatalf("first list_projects within the limit: tool error %s", text)
	}
	text, isErr := call(t, cs, "list_projects", map[string]any{})
	if !isErr || !waitIn("1").MatchString(text) {
		t.Errorf("list_projects over the limit: tool error %v, want the wait of 1 second: %s", isErr, text)
	}
	assertNoHeaderPointer(t, "list_projects", text)
	assertLeaksNoAddress(t, "list_projects", text, strings.TrimPrefix(srv.URL, "http://"), readerToken)
}
