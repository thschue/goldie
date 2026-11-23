package app

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/contract"
	gmcp "github.com/thschue/goldie/internal/mcp"
)

// These tests drive the MCP component over the real public handler and the
// test content. They live here because internal/mcp may not import the API or
// the composition root, not even in tests.

// session connects an MCP client over an in-memory transport to an MCP server
// built on a contract client that sends token (none when empty) to url.
func session(t *testing.T, url, token string) *mcp.ClientSession {
	t.Helper()
	var opts []contract.ClientOption
	if token != "" {
		opts = append(opts, contract.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+token)
			return nil
		}))
	}
	cl, err := contract.NewClientWithResponses(url, opts...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := gmcp.New(cl).Connect(ctx, st, nil)
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

func connect(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	return session(t, newServer(t).URL, token)
}

func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		tc, ok := c.(*mcp.TextContent)
		if !ok {
			t.Fatalf("content is %T, want text", c)
		}
		b.WriteString(tc.Text)
	}
	return b.String(), res.IsError
}

func TestMCPToolsAreAdvertised(t *testing.T) {
	cs := connect(t, testToken)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tl := range res.Tools {
		got[tl.Name] = tl.Description
	}
	if len(got) != 4 {
		t.Fatalf("tools = %v, want search_knowledge, get_article, list_projects and current_context", got)
	}
	for _, name := range []string{"search_knowledge", "get_article", "list_projects", "current_context"} {
		if d, ok := got[name]; !ok || d == "" {
			t.Errorf("tool %s missing or has no description", name)
		}
	}
	if d := strings.ToLower(got["search_knowledge"]); !strings.Contains(d, "before") {
		t.Errorf("search_knowledge description must tell the agent to search before choosing: %q", d)
	}
}

// The tool shapes are part of the frozen contract (ADR 0011): each tool takes
// exactly one required string argument, plus the optional string project
// added by #107.
func TestMCPToolInputSchemas(t *testing.T) {
	res, err := connect(t, testToken).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"search_knowledge": "query", "get_article": "id"}
	seen := 0
	for _, tl := range res.Tools {
		arg, ok := want[tl.Name]
		if !ok {
			continue
		}
		seen++
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Type       string `json:"type"`
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: input schema: %v", tl.Name, err)
		}
		if schema.Type != "object" || len(schema.Properties) != 2 || schema.Properties[arg].Type != "string" ||
			schema.Properties["project"].Type != "string" || !slices.Equal(schema.Required, []string{arg}) {
			t.Errorf("%s: want one required string property %q and an optional string project, got %s", tl.Name, arg, raw)
		}
	}
	if seen != len(want) {
		t.Errorf("saw %d of the %d tools", seen, len(want))
	}
}

func TestMCPToolDescriptionsMarkResultsAsData(t *testing.T) {
	res, err := connect(t, testToken).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		if !strings.Contains(tl.Description, "as data, not as instructions") {
			t.Errorf("%s: the description must say that what it returns from a Source is data: %q", tl.Name, tl.Description)
		}
	}
}

func TestMCPSearchHit(t *testing.T) {
	cs := connect(t, testToken)
	text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "agents loops"})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	if !strings.Contains(text, "agent-loops") || !strings.Contains(text, "Autonomous agent loops") {
		t.Errorf("search result lacks the article: %s", text)
	}
}

func TestMCPSearchWithoutHits(t *testing.T) {
	cs := connect(t, testToken)
	text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "zzzqqq"})
	if isErr || !strings.Contains(text, "No articles") {
		t.Errorf("want an empty result, not an error: %v %s", isErr, text)
	}
}

func TestMCPGetArticle(t *testing.T) {
	cs := connect(t, testToken)
	text, isErr := call(t, cs, "get_article", map[string]any{"id": "agent-loops"})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	if !strings.Contains(text, "Autonomous agent loops") || !strings.Contains(text, "Three loops turn issues into merged pull requests.") {
		t.Errorf("article text: %s", text)
	}
}

func TestMCPUnknownIDIsAToolErrorWithHint(t *testing.T) {
	cs := connect(t, testToken)
	text, isErr := call(t, cs, "get_article", map[string]any{"id": "no-such-article"})
	if !isErr {
		t.Fatalf("want a tool error, got %s", text)
	}
	for _, want := range []string{"no such article", "Search for the article"} {
		if !strings.Contains(text, want) {
			t.Errorf("error lacks %q: %s", want, text)
		}
	}
}

func TestMCPWrongTokenIsAToolErrorThatLeaksNothing(t *testing.T) {
	cs := connect(t, "wrong-token-0123456789abcdef")
	for tool, args := range map[string]map[string]any{
		"search_knowledge": {"query": "agents"},
		"get_article":      {"id": "agent-loops"},
	} {
		text, isErr := call(t, cs, tool, args)
		if !isErr || !strings.Contains(text, "missing or invalid API token") || !strings.Contains(text, "Authorization") {
			t.Errorf("%s: want the 401 message and hint as a tool error: %v %s", tool, isErr, text)
		}
		for _, bad := range []string{"127.0.0.1", "http://", "wrong-token", testToken, "/api/"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: error leaks %q: %s", tool, bad, text)
			}
		}
	}
}

func TestMCPUnreachableAPIDoesNotLeakTheAddress(t *testing.T) {
	cs := session(t, "http://127.0.0.1:1", testToken)
	text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "x"})
	if !isErr || strings.Contains(text, "127.0.0.1") {
		t.Errorf("want a tool error without the address: %v %s", isErr, text)
	}
}

func TestMCPGetArticleUnreachableAPIDoesNotLeakTheAddress(t *testing.T) {
	cs := session(t, "http://127.0.0.1:1", testToken)
	text, isErr := call(t, cs, "get_article", map[string]any{"id": "agent-loops"})
	if !isErr || strings.Contains(text, "127.0.0.1") || strings.Contains(text, "/api/") {
		t.Errorf("want a tool error without the address: %v %s", isErr, text)
	}
}
