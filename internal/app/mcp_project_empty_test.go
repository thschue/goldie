package app

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Acceptance tests for #341: on search_knowledge and get_article an empty
// project counts as omitted (the request carries no project parameter and the
// call is no error), and a whitespace-only project is refused before any
// request, with the hint naming list_projects. The cut is the MCP protocol
// over an in-memory transport against a stub API that records each request,
// as in the #338 tests in mcp_project_test.go.

// queryRecorder is a stub API that records the query of every request and
// answers search with no results and an article fetch with one article.
func queryRecorder(t *testing.T) (string, func() []url.Values) {
	t.Helper()
	var (
		mu   sync.Mutex
		seen []url.Values
	)
	addr := mcpStub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Query())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"platform/deploy","name":"Deploy","keywords":[],"content":"body"}`))
	})
	return addr, func() []url.Values {
		mu.Lock()
		defer mu.Unlock()
		return append([]url.Values(nil), seen...)
	}
}

// emptyProjectCalls is one call per tool with an empty project.
var emptyProjectCalls = map[string]map[string]any{
	"search_knowledge": {"query": "kubernetes", "project": ""},
	"get_article":      {"id": "platform/deploy", "project": ""},
}

// An empty project is no error, and each tool sends exactly one request that
// carries no project parameter at all, not even an empty one.
func TestMCPEmptyProjectIsSentWithoutAProjectParameter(t *testing.T) {
	for tool, args := range emptyProjectCalls {
		addr, requests := queryRecorder(t)
		cs := projectSession(t, addr, testToken)
		if text, isErr := call(t, cs, tool, args); isErr {
			t.Errorf("%s with an empty project: tool error %s", tool, text)
		}
		got := requests()
		if len(got) != 1 {
			t.Errorf("%s with an empty project: the API got %d requests, want 1", tool, len(got))
			continue
		}
		if got[0].Has("project") {
			t.Errorf("%s with an empty project: the request carries project=%q, want no project parameter", tool, got[0]["project"])
		}
	}
}

// Negative: the same recorder sees the project parameter when a valid project
// is passed, so the check above is not passing only because it cannot see it.
func TestMCPValidProjectIsSentAsAProjectParameter(t *testing.T) {
	for tool, args := range map[string]map[string]any{
		"search_knowledge": {"query": "kubernetes", "project": "github.com/acme/shop"},
		"get_article":      {"id": "platform/deploy", "project": "github.com/acme/shop"},
	} {
		addr, requests := queryRecorder(t)
		cs := projectSession(t, addr, testToken)
		if text, isErr := call(t, cs, tool, args); isErr {
			t.Errorf("%s with a valid project: tool error %s", tool, text)
		}
		got := requests()
		if len(got) != 1 || got[0].Get("project") != "github.com/acme/shop" {
			t.Errorf("%s with a valid project: requests %v, want one with project=github.com/acme/shop", tool, got)
		}
	}
}

// An empty project gets the same answer as no project from the real handler:
// the global article, not the Project's, and no unregistered hint.
func TestMCPEmptyProjectGivesTheGlobalAnswer(t *testing.T) {
	cs := connect(t, testToken)
	for tool, args := range emptyProjectCalls {
		text, isErr := call(t, cs, tool, args)
		if isErr || !strings.Contains(text, "Deploying to Kubernetes") || strings.Contains(text, "Shop deploy") {
			t.Errorf("%s with an empty project: tool error %v, want the global answer only:\n%s", tool, isErr, text)
		}
		if strings.Contains(text, "not registered") {
			t.Errorf("%s with an empty project: carries the unregistered hint:\n%s", tool, text)
		}
	}
}

// whitespaceProjects are project values made only of white space, ASCII and
// Unicode. None is empty, so none counts as omitted.
var whitespaceProjects = map[string]string{
	"one space":           " ",
	"two spaces":          "  ",
	"tab":                 "\t",
	"newline":             "\n",
	"carriage return":     "\r",
	"CRLF":                "\r\n",
	"vertical tab":        "\v",
	"form feed":           "\f",
	"mixed ASCII":         " \t\n ",
	"non-breaking space":  "\u00a0",
	"NBSP and space":      " \u00a0 ",
	"em space":            "\u2003",
	"ideographic space":   "\u3000",
	"line separator":      "\u2028",
	"narrow no-break":     "\u202f",
	"oversized (64 KiB)":  strings.Repeat(" ", 64<<10),
	"next line (NEL)":     "\u0085",
	"ogham space mark":    "\u1680",
	"paragraph separator": "\u2029",
}

// A whitespace-only project is a tool error whose hint names list_projects,
// on both tools, and it is refused before any request: the API sees none.
func TestMCPWhitespaceOnlyProjectIsRefusedBeforeAnyRequest(t *testing.T) {
	addr, requests := queryRecorder(t)
	cs := projectSession(t, addr, testToken)
	for name, project := range whitespaceProjects {
		for tool, args := range map[string]map[string]any{
			"search_knowledge": {"query": "kubernetes", "project": project},
			"get_article":      {"id": "platform/deploy", "project": project},
		} {
			text, isErr := call(t, cs, tool, args)
			assertInvalidProjectError(t, name+"/"+tool, text, isErr)
			if strings.TrimSpace(text) != text {
				t.Errorf("%s/%s: the tool error starts or ends with white space: %q", name, tool, text)
			}
		}
	}
	if got := requests(); len(got) != 0 {
		t.Errorf("the API got %d requests for whitespace-only projects, want none: %v", len(got), got)
	}
}

// A project with white space around a valid id is refused too: it is not
// trimmed into the id, and nothing is sent.
func TestMCPProjectWithSurroundingWhitespaceIsRefused(t *testing.T) {
	addr, requests := queryRecorder(t)
	cs := projectSession(t, addr, testToken)
	for name, project := range map[string]string{
		"leading space":   " github.com/acme/shop",
		"trailing space":  "github.com/acme/shop ",
		"trailing tab":    "github.com/acme/shop\t",
		"trailing NBSP":   "github.com/acme/shop\u00a0",
		"leading newline": "\ngithub.com/acme/shop",
	} {
		for tool, args := range map[string]map[string]any{
			"search_knowledge": {"query": "kubernetes", "project": project},
			"get_article":      {"id": "platform/deploy", "project": project},
		} {
			text, isErr := call(t, cs, tool, args)
			assertInvalidProjectError(t, name+"/"+tool, text, isErr)
		}
	}
	if got := requests(); len(got) != 0 {
		t.Errorf("the API got %d requests for projects with white space, want none: %v", len(got), got)
	}
}
