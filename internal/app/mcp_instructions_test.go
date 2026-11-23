package app

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	gmcp "github.com/thschue/goldie/internal/mcp"
)

// Acceptance tests for #417 (slice 5 of #18), the MCP protocol seam: the
// resource goldie://instructions and the clauses in current_context, over the
// real public handler and the instructions-content fixture (see
// instructions_api_test.go): the global clauses base (both targets) and
// claude-only, and for github.com/acme/shop the clause shop-rules (both
// targets).
//
// The contract they fix, beyond the issue:
//   - goldie://instructions is advertised in resources/list and read with
//     resources/read; it answers the block the API renders for the session's
//     default Project (global without one), with {{mcp_name}} from
//     GOLDIE_MCP_NAME;
//   - current_context lists the clauses, that is shows their rendered text, in
//     addition to the default Project and its source;
//   - the block is untrusted Source content: hidden characters and escape
//     sequences are cleaned before an agent reads it;
//   - an API failure is an error that names no address, token or path.
//
// The clauses asserted on name both targets, so the tests hold whichever
// target the implementation renders for.

const (
	instructionsURI = "goldie://instructions"
	shopRules       = "SHOP-RULES-CLAUSE for github.com/acme/shop."
	shopBase        = "Base for github.com/acme/shop: ask the goldie MCP server, or run `goldie lookup <id>`."
)

// readInstructions reads uri over the MCP session and returns its text.
func readInstructions(t *testing.T, cs *mcp.ClientSession, uri string) (string, error) {
	t.Helper()
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range res.Contents {
		b.WriteString(c.Text)
	}
	return b.String(), nil
}

func instructionsSession(t *testing.T, mcpName string, opts ...gmcp.Option) *mcp.ClientSession {
	t.Helper()
	return projectSession(t, instructionsServer(t, mcpName).URL, readerToken, opts...)
}

// Criterion: goldie://instructions is a resource the client can discover.
func TestMCPInstructionsResourceIsListed(t *testing.T) {
	cs := instructionsSession(t, "")
	res, err := cs.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	for _, r := range res.Resources {
		if r.URI == instructionsURI {
			if r.Name == "" || r.Description == "" {
				t.Errorf("resource %s has name %q and description %q, want both", r.URI, r.Name, r.Description)
			}
			return
		}
	}
	t.Errorf("resources/list has no %s: %+v", instructionsURI, res.Resources)
}

// Criterion: reading it returns the rendered block for the default Project.
func TestMCPInstructionsResourceReturnsTheRenderedBlockForTheDefaultProject(t *testing.T) {
	cs := instructionsSession(t, "", gmcp.WithProject(itemsProject, "config"))
	text, err := readInstructions(t, cs, instructionsURI)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	for _, want := range []string{shopBase, shopRules} {
		if !strings.Contains(text, want) {
			t.Errorf("resource text does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "{{") {
		t.Errorf("resource text holds an unreplaced placeholder:\n%s", text)
	}
}

// Negative: without a default Project the block is the global one, with none
// of a Project's clauses.
func TestMCPInstructionsResourceWithoutADefaultProjectIsGlobal(t *testing.T) {
	cs := instructionsSession(t, "")
	text, err := readInstructions(t, cs, instructionsURI)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if !strings.Contains(text, "ask the goldie MCP server") {
		t.Errorf("global block lacks the base clause:\n%s", text)
	}
	if strings.Contains(text, "SHOP-RULES-CLAUSE") || strings.Contains(text, "{{") {
		t.Errorf("global block has a Project's clause or a placeholder:\n%s", text)
	}
}

// Criterion: {{mcp_name}} is the server setting GOLDIE_MCP_NAME, in both
// places an agent reads the clauses.
func TestMCPInstructionsUseTheConfiguredMCPName(t *testing.T) {
	cs := instructionsSession(t, "acme-golden", gmcp.WithProject(itemsProject, "config"))
	text, err := readInstructions(t, cs, instructionsURI)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	want := "Base for github.com/acme/shop: ask the acme-golden MCP server, or run `goldie lookup <id>`."
	if !strings.Contains(text, want) {
		t.Errorf("resource text does not contain %q:\n%s", want, text)
	}
	ctxText, isErr := call(t, cs, "current_context", map[string]any{})
	if isErr || !strings.Contains(ctxText, want) {
		t.Errorf("current_context (tool error %v) does not contain %q:\n%s", isErr, want, ctxText)
	}
}

// Criterion: the clauses are listed in current_context, next to the default
// Project and its source.
func TestMCPCurrentContextListsTheClauses(t *testing.T) {
	cs := instructionsSession(t, "", gmcp.WithProject(itemsProject, "config"))
	text, isErr := call(t, cs, "current_context", map[string]any{})
	if isErr {
		t.Fatalf("current_context is a tool error: %s", text)
	}
	for _, want := range []string{shopBase, shopRules, itemsProject, "config"} {
		if !strings.Contains(text, want) {
			t.Errorf("current_context does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "{{") {
		t.Errorf("current_context holds an unreplaced placeholder:\n%s", text)
	}
}

// Negative: without a default Project current_context still says so, and lists
// only the global clauses.
func TestMCPCurrentContextWithoutADefaultListsOnlyGlobalClauses(t *testing.T) {
	cs := instructionsSession(t, "")
	text, isErr := call(t, cs, "current_context", map[string]any{})
	if isErr || !strings.Contains(strings.ToLower(text), "no default") {
		t.Errorf("current_context (tool error %v) does not say there is no default:\n%s", isErr, text)
	}
	if !strings.Contains(text, "ask the goldie MCP server") {
		t.Errorf("current_context lacks the global clause:\n%s", text)
	}
	if strings.Contains(text, "SHOP-RULES-CLAUSE") {
		t.Errorf("current_context lists a Project's clause without a default Project:\n%s", text)
	}
}

// instructionsStub answers GET /api/v1/instructions with block, and every
// other path with 404, and counts nothing else.
func instructionsStub(t *testing.T, block string) string {
	t.Helper()
	return mcpStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/instructions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"block":"` + jsonEscape(block) + `","digest":"0000"}`))
	})
}

func jsonEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\x1b", `\u001b`)
	return r.Replace(s)
}

// Abuse: the block is derived from Source content. Hidden, bidi and tag
// characters and escape sequences never reach the agent, in the resource or in
// current_context.
func TestMCPInstructionsCleanWhatASourceSays(t *testing.T) {
	block := "Use goldie\u202e\u200b\U000E0049\x1b[2J before choosing.\nSecond line stays."
	cs := projectSession(t, instructionsStub(t, block), readerToken, gmcp.WithProject(itemsProject, "config"))
	text, err := readInstructions(t, cs, instructionsURI)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	ctxText, _ := call(t, cs, "current_context", map[string]any{})
	for what, got := range map[string]string{"resource": text, "current_context": ctxText} {
		if !strings.Contains(got, "Use goldie") || !strings.Contains(got, "Second line stays.") {
			t.Errorf("%s lost the block's text:\n%q", what, got)
		}
		for _, bad := range []string{"\u202e", "\u200b", "\U000E0049", "\x1b"} {
			if strings.Contains(got, bad) {
				t.Errorf("%s keeps %q:\n%q", what, bad, got)
			}
		}
	}
}

// Abuse: when the API fails, the resource is an error and neither it nor
// current_context names the address, the token or a path.
func TestMCPInstructionsAPIFailureLeaksNothing(t *testing.T) {
	url := mcpStub(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom at http://10.9.8.7:1234/etc/leak-marker", http.StatusInternalServerError)
	})
	cs := projectSession(t, url, readerToken, gmcp.WithProject(itemsProject, "config"))
	text, err := readInstructions(t, cs, instructionsURI)
	if err == nil {
		t.Fatalf("ReadResource succeeded on an API failure: %q", text)
	}
	assertLeaksNoAddress(t, "resource", err.Error(), strings.TrimPrefix(url, "http://"), "10.9.8.7", readerToken)
	ctxText, _ := call(t, cs, "current_context", map[string]any{})
	assertLeaksNoAddress(t, "current_context", ctxText, strings.TrimPrefix(url, "http://"), "10.9.8.7", readerToken)
}

// Abuse: a wrong token is an error, not a block.
func TestMCPInstructionsResourceWithAWrongTokenIsAnError(t *testing.T) {
	srv := instructionsServer(t, "")
	cs := projectSession(t, srv.URL, "not-a-token-0123456789abcdef0123456789", gmcp.WithProject(itemsProject, "config"))
	text, err := readInstructions(t, cs, instructionsURI)
	if err == nil {
		t.Fatalf("ReadResource with a wrong token succeeded: %q", text)
	}
	if strings.Contains(err.Error(), "CLAUSE") || strings.Contains(err.Error(), srv.URL) {
		t.Errorf("error leaks content or the address: %v", err)
	}
}

// Abuse: only the one URI is a resource. A neighbour, a query and a path
// below it are errors and send no request for instructions.
func TestMCPInstructionsResourceTakesNoOtherURI(t *testing.T) {
	url, requests := countingStub(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	cs := projectSession(t, url, readerToken, gmcp.WithProject(itemsProject, "config"))
	for _, uri := range []string{
		"goldie://instructions/../../etc/passwd",
		"goldie://instructions?target=claude&project=github.com/evil/x",
		"goldie://instruction",
		"goldie://INSTRUCTIONS/x",
		"file:///etc/passwd",
	} {
		if text, err := readInstructions(t, cs, uri); err == nil {
			t.Errorf("ReadResource(%q) succeeded: %q", uri, text)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("a refused URI sent %d requests, want 0", n)
	}
}
