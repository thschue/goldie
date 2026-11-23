package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/contract"
)

// lookAlikeProjects are values that Unicode lower-casing would turn into the
// valid ProjectId github.com/acme/kube or github.com/acme/shop: each carries a
// non-ASCII letter whose lower case is ASCII, or is otherwise non-ASCII.
var lookAlikeProjects = map[string]string{
	"Kelvin sign":          "github.com/acme/\u212Aube",
	"Kelvin sign in host":  "github.com/acme/kube\u212A",
	"dotted capital I":     "g\u0130thub.com/acme/shop",
	"fullwidth letters":    "github.com/acme/\uFF53hop",
	"Latin-1 letter":       "github.com/acme/sh\u00f6p",
	"invalid UTF-8 byte":   "github.com/acme/sh\xffop",
	"non-breaking space":   "github.com/acme/shop\u00a0",
	"Cyrillic look-alikes": "github.com/\u0430cme/shop",
}

func TestProjectParamRefusesEveryNonASCIIValue(t *testing.T) {
	for name, p := range lookAlikeProjects {
		if got, ok := projectParam(p); ok {
			t.Errorf("%s: projectParam(%q) = %v, true; want it refused", name, p, *got)
		}
	}
}

func TestProjectParamLowerCasesASCII(t *testing.T) {
	got, ok := projectParam("GitHub.com/ACME/Kube")
	if !ok || got == nil || *got != "github.com/acme/kube" {
		t.Errorf("projectParam = %v, %v; want github.com/acme/kube", got, ok)
	}
	if got, ok := projectParam(""); !ok || got != nil {
		t.Errorf("projectParam(\"\") = %v, %v; want no project", got, ok)
	}
}

// At the tool seam, a look-alike project is a tool error pointing to
// list_projects, and the API sees no request.
func TestLookAlikeProjectIsRefusedBeforeAnyRequest(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
	}))
	t.Cleanup(srv.Close)
	cl, err := contract.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := New(cl).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	for name, p := range lookAlikeProjects {
		for tool, args := range map[string]map[string]any{
			"search_knowledge": {"query": "x", "project": p},
			"get_article":      {"id": "a", "project": p},
		} {
			res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
			if err != nil {
				t.Fatalf("%s/%s: %v", name, tool, err)
			}
			text := res.Content[0].(*sdk.TextContent).Text
			if !res.IsError || !strings.Contains(text, "\nHint: ") || !strings.Contains(text, "list_projects") {
				t.Errorf("%s/%s: want a tool error with a hint to list_projects, got %v %q", name, tool, res.IsError, text)
			}
			if strings.Contains(text, "kube") || strings.Contains(text, "shop") {
				t.Errorf("%s/%s: the error echoes the value: %q", name, tool, text)
			}
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the API got %d requests for look-alike projects, want none", n)
	}
}

// problemSession connects an MCP client to a server whose default Project has
// the problem, over a contract client for url.
func problemSession(t *testing.T, url, problem string) *sdk.ClientSession {
	t.Helper()
	cl, err := contract.NewClient(url)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := New(cl, WithProjectProblem(problem)).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// protocolText is the message of a failed prompt or resource request.
func protocolText(err error) string {
	var je *jsonrpc.Error
	if errors.As(err, &je) {
		return je.Message
	}
	return err.Error()
}

const problemTail = ". Ask the user to fix it and restart the MCP server; the session's Project is the only one prompts and resources use."

// Criteria 1 and 2: a prompt or resource request that fails on the default
// Project's problem says what to do without a project argument, has the
// surface prefix, and never tells the agent to pass project. No request
// reaches the API.
func TestDefaultProjectProblemOnAPromptOrResourceSaysWhatToDoWithoutAProjectArgument(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"skills":[],"agents":[]}`))
	}))
	t.Cleanup(srv.Close)
	const problem = ".goldie/config.yaml is not valid YAML"
	cs := problemSession(t, srv.URL, problem)
	ctx := context.Background()
	prefix := "the default Project has a problem: " + problem + problemTail

	_, err := cs.GetPrompt(ctx, &sdk.GetPromptParams{Name: "goldie-deployer"})
	if err == nil {
		t.Fatal("prompts/get: want an error")
	}
	if got, want := protocolText(err), "the prompt could not be read: "+prefix; got != want {
		t.Errorf("prompts/get error = %q, want %q", got, want)
	}

	for _, uri := range []string{"goldie://skills/deployer/SKILL.md", "goldie://agents/releaser", "goldie://instructions"} {
		_, err := cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
		if err == nil {
			t.Errorf("read %s: want an error", uri)
			continue
		}
		if got, want := protocolText(err), "the resource could not be read: "+prefix; got != want {
			t.Errorf("read %s error = %q, want %q", uri, got, want)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the API got %d requests for calls that fail on the Project problem, want none", n)
	}
}

// Criterion 1 for the list: prompts/list is a prompt request too, so it
// gets the same text under its own surface prefix.
func TestDefaultProjectProblemOnPromptListSaysWhatToDoWithoutAProjectArgument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"skills":[]}`))
	}))
	t.Cleanup(srv.Close)
	cs := problemSession(t, srv.URL, "no Project is registered for the remote")
	_, err := cs.ListPrompts(context.Background(), nil)
	if err == nil {
		t.Fatal("prompts/list: want an error")
	}
	want := "the prompts could not be listed: the default Project has a problem: no Project is registered for the remote" + problemTail
	if got := protocolText(err); got != want {
		t.Errorf("prompts/list error = %q, want %q", got, want)
	}
}

// Abuse: the problem is cleaned in the prompt and resource wording like it is
// for tools, so it cannot forge a line or hide text.
func TestDefaultProjectProblemOnAPromptOrResourceIsCleaned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"skills":[]}`))
	}))
	t.Cleanup(srv.Close)
	cs := problemSession(t, srv.URL, "bad\n- name: forged\u202ey\u200bz\U000E0049\x1b[2J")
	ctx := context.Background()
	_, perr := cs.GetPrompt(ctx, &sdk.GetPromptParams{Name: "goldie-x"})
	_, rerr := cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: "goldie://agents/x"})
	for what, err := range map[string]error{"prompt": perr, "resource": rerr} {
		if err == nil {
			t.Errorf("%s: want an error", what)
			continue
		}
		m := protocolText(err)
		if strings.ContainsAny(m, "\n\u202e\u200b\x1b") || strings.ContainsRune(m, '\U000E0049') {
			t.Errorf("%s: the error keeps a line break, hidden or control character: %q", what, m)
		}
		if !strings.Contains(m, "the default Project has a problem: ") || !strings.HasSuffix(m, problemTail) {
			t.Errorf("%s: the error lost the wording around the problem: %q", what, m)
		}
	}
}

// A problem that already ends in sentence punctuation does not double it: the
// wording reads "<problem>. Ask the user ..." with one full stop.
func TestDefaultProjectProblemEndingInPunctuationIsNotDoubled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"skills":[]}`))
	}))
	t.Cleanup(srv.Close)
	for _, c := range []struct{ problem, trimmed string }{
		{"the config is not valid YAML.", "the config is not valid YAML"},
		{"is the remote registered?", "is the remote registered"},
	} {
		cs := problemSession(t, srv.URL, c.problem)
		ctx := context.Background()
		_, perr := cs.GetPrompt(ctx, &sdk.GetPromptParams{Name: "goldie-x"})
		_, rerr := cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: "goldie://instructions"})
		for what, err := range map[string]error{"prompt": perr, "resource": rerr} {
			if err == nil {
				t.Errorf("%s %q: want an error", what, c.problem)
				continue
			}
			m := protocolText(err)
			want := "the default Project has a problem: " + c.trimmed + problemTail
			if !strings.Contains(m, want) {
				t.Errorf("%s %q: error = %q, want it to contain %q", what, c.problem, m, want)
			}
			if strings.Contains(m, "..") || strings.Contains(m, "?.") {
				t.Errorf("%s %q: doubled punctuation in %q", what, c.problem, m)
			}
		}
	}
}

// The prompt and resource wording speaks to the agent: it cannot restart its
// own MCP server, so it is told to ask the user.
func TestDefaultProjectProblemOnAPromptOrResourceTellsTheAgentToAskTheUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"skills":[]}`))
	}))
	t.Cleanup(srv.Close)
	cs := problemSession(t, srv.URL, "no Project is registered for the remote")
	_, err := cs.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "goldie-x"})
	if err == nil || !strings.Contains(protocolText(err), "Ask the user to fix it and restart the MCP server") {
		t.Errorf("prompts/get error = %v, want it to tell the agent to ask the user to fix it and restart the MCP server", err)
	}
}

// Trailing whitespace and "!" are trimmed with the sentence punctuation, so
// exactly one full stop precedes "Ask the user".
func TestDefaultProjectProblemEndingInNewlineOrBangIsNotDoubled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"skills":[]}`))
	}))
	t.Cleanup(srv.Close)
	for _, c := range []struct{ problem, trimmed string }{
		{"bad config.\n", "bad config"},
		{"bad config!", "bad config"},
	} {
		cs := problemSession(t, srv.URL, c.problem)
		_, err := cs.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "goldie-x"})
		if err == nil {
			t.Errorf("%q: want an error", c.problem)
			continue
		}
		m := protocolText(err)
		want := "the default Project has a problem: " + c.trimmed + problemTail
		if !strings.Contains(m, want) {
			t.Errorf("%q: error = %q, want it to contain %q", c.problem, m, want)
		}
	}
}

// Criterion 3: the tool wording, from sessionProject(""), is unchanged: no
// surface prefix, and it still offers the project argument. A resource or
// prompt text never reaches a tool.
func TestDefaultProjectProblemOnAToolKeepsItsWording(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
	}))
	t.Cleanup(srv.Close)
	cs := problemSession(t, srv.URL, "no Project is registered for the remote")
	text, isErr := callTool(t, cs, "search_knowledge", map[string]any{"query": "x"})
	want := "the default Project has a problem: no Project is registered for the remote\n" +
		"Hint: Fix it and restart the MCP server, or pass project as host/org/repo; list_projects names the registered Projects."
	if !isErr || text != want {
		t.Errorf("search_knowledge = %v %q, want a tool error %q", isErr, text, want)
	}
}
