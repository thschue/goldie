package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// Acceptance tests for #415 (slice 3 of #18), the HTTP contract seam:
// GET /api/v1/instructions?project=&target= through the public handler
// PublicHandler builds. Every response is checked against the served spec by
// do. Rendering itself (clause choice, order, templating) is #414's, tested in
// the engine; these tests fix what the endpoint adds.
//
// The contract they fix, beyond the spec:
//   - the 200 body is an object with "block" (the rendered text) and "digest"
//     (the hex SHA-256 of the block, as the engine computes it);
//   - target is required and is "claude" or "agents"; anything else, or none,
//     is a 400 bad_request envelope whose hint names the valid targets and
//     echoes nothing of the request;
//   - project= behaves as for articles: optional, an unregistered Project gets
//     the global block plus a hint naming it, an invalid one is a 400;
//   - {{mcp_name}} is the server setting GOLDIE_MCP_NAME, default goldie, and
//     {{cli}} is goldie;
//   - the endpoint needs a token like every other /api/v1 route.
//
// The fixture has the global clauses base (claude and agents; uses all three
// variables), claude-only (claude) and, for Project github.com/acme/shop,
// shop-rules (claude and agents).
const instructionsContent = "testdata/instructions-content"

type instructionsAnswer struct {
	Block  string `json:"block"`
	Digest string `json:"digest"`
	Hint   string `json:"hint"`
}

// instructionsServer starts the public handler with GOLDIE_MCP_NAME read from
// the environment the way the server reads it: mcpName "" leaves it unset.
func instructionsServer(t *testing.T, mcpName string) *httptest.Server {
	t.Helper()
	env := map[string]string{}
	if mcpName != "" {
		env["GOLDIE_MCP_NAME"] = mcpName
	}
	cfg := loadedConfig(t, env)
	cfg.ContentDir = instructionsContent
	cfg.APITokens = namedTokenLines()
	cfg.RateLimit = roomyRateLimit
	h, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func instructionsGet(t *testing.T, srv *httptest.Server, query string) instructionsAnswer {
	t.Helper()
	status, body := itemsGet(t, srv, readerToken, "/api/v1/instructions"+query)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/instructions%s: status %d, want 200: %s", query, status, body)
	}
	var a instructionsAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("GET /api/v1/instructions%s: %v: %s", query, err, body)
	}
	return a
}

func TestServedSpecDeclaresTheInstructionsOperation(t *testing.T) {
	if ops := specOperations(t); !slices.Contains(ops, "GET /api/v1/instructions") {
		t.Errorf("the spec declares no GET /api/v1/instructions; it declares %v", ops)
	}
}

// Criterion: the block is returned, rendered, with the default mcp_name and
// {{cli}} = goldie.
func TestInstructionsRenderTheBlockWithDefaultMCPNameAndCLI(t *testing.T) {
	srv := instructionsServer(t, "")
	a := instructionsGet(t, srv, "?target=claude&project="+url.QueryEscape(itemsProject))
	for _, want := range []string{
		"Base for github.com/acme/shop: ask the goldie MCP server, or run `goldie lookup <id>`.",
		"CLAUDE-ONLY-CLAUSE", "SHOP-RULES-CLAUSE for github.com/acme/shop.",
	} {
		if !strings.Contains(a.Block, want) {
			t.Errorf("block %q does not contain %q", a.Block, want)
		}
	}
	if strings.Contains(a.Block, "{{") {
		t.Errorf("block %q holds an unreplaced placeholder", a.Block)
	}
}

// Criterion: {{mcp_name}} comes from GOLDIE_MCP_NAME; {{cli}} stays goldie.
func TestInstructionsUseTheConfiguredMCPName(t *testing.T) {
	srv := instructionsServer(t, "acme-golden")
	a := instructionsGet(t, srv, "?target=agents&project="+url.QueryEscape(itemsProject))
	want := "Base for github.com/acme/shop: ask the acme-golden MCP server, or run `goldie lookup <id>`."
	if !strings.Contains(a.Block, want) {
		t.Errorf("block %q does not contain %q", a.Block, want)
	}
}

// Criterion: the target selects the clauses.
func TestInstructionsTargetSelectsTheClauses(t *testing.T) {
	srv := instructionsServer(t, "")
	agents := instructionsGet(t, srv, "?target=agents")
	if strings.Contains(agents.Block, "CLAUDE-ONLY-CLAUSE") || !strings.Contains(agents.Block, "ask the goldie MCP server") {
		t.Errorf("agents block %q, want the shared clause and not the claude-only one", agents.Block)
	}
	claude := instructionsGet(t, srv, "?target=claude")
	if !strings.Contains(claude.Block, "CLAUDE-ONLY-CLAUSE") {
		t.Errorf("claude block %q lacks the claude-only clause", claude.Block)
	}
}

// Criterion: the digest is present, is the SHA-256 of the block, is stable
// across requests and changes when the block does.
func TestInstructionsDigestIsPresentStableAndFollowsTheBlock(t *testing.T) {
	srv := instructionsServer(t, "")
	q := "?target=claude&project=" + url.QueryEscape(itemsProject)
	a := instructionsGet(t, srv, q)
	sum := sha256.Sum256([]byte(a.Block))
	if a.Digest == "" || a.Digest != hex.EncodeToString(sum[:]) {
		t.Errorf("digest %q, want the hex SHA-256 of the block %q", a.Digest, a.Block)
	}
	if b := instructionsGet(t, srv, q); b.Block != a.Block || b.Digest != a.Digest {
		t.Errorf("a second request answered %+v, want %+v", b, a)
	}
	if g := instructionsGet(t, srv, "?target=claude"); g.Digest == a.Digest || g.Digest == "" {
		t.Errorf("the global block and the Project's share digest %q", g.Digest)
	}
	renamed := instructionsGet(t, instructionsServer(t, "other-name"), q)
	if renamed.Digest == a.Digest || renamed.Block == a.Block {
		t.Errorf("a different GOLDIE_MCP_NAME left the block and digest unchanged")
	}
}

// Criterion: an unknown, missing or hostile target is a 400 with a hint, and
// nothing of the request is echoed.
func TestInstructionsUnknownTargetIsABadRequestWithAHint(t *testing.T) {
	srv := instructionsServer(t, "")
	for _, q := range []string{
		"?target=gemini", "?target=", "", "?project=" + itemsProject,
		"?target=Claude", "?target=claude%0Aagents", "?target=..%2F..%2Fetc%2Fpasswd",
	} {
		status, body := itemsGet(t, srv, readerToken, "/api/v1/instructions"+q)
		if status != http.StatusBadRequest {
			t.Errorf("GET /api/v1/instructions%s: status %d, want 400: %s", q, status, body)
			continue
		}
		code, hint := errorHint(t, body)
		if code != "bad_request" {
			t.Errorf("GET /api/v1/instructions%s: code %q, want bad_request", q, code)
		}
		if !strings.Contains(hint, "claude") || !strings.Contains(hint, "agents") {
			t.Errorf("GET /api/v1/instructions%s: hint %q does not name the valid targets", q, hint)
		}
		for _, leak := range []string{"gemini", "passwd"} {
			if strings.Contains(string(body), leak) {
				t.Errorf("GET /api/v1/instructions%s: the answer echoes the request: %s", q, body)
			}
		}
	}
}

// Criterion: project= is optional; an unregistered Project gets the global
// block and a hint naming it; an invalid one is a 400.
func TestInstructionsProjectConventions(t *testing.T) {
	srv := instructionsServer(t, "")
	global := instructionsGet(t, srv, "?target=claude")
	if global.Hint != "" || strings.Contains(global.Block, "SHOP-RULES-CLAUSE") || !strings.Contains(global.Block, "CLAUDE-ONLY-CLAUSE") {
		t.Errorf("global answer %+v, want the global clauses only and no hint", global)
	}

	const unknown = "github.com/acme/unknown"
	u := instructionsGet(t, srv, "?target=claude&project="+unknown)
	if strings.Contains(u.Block, "SHOP-RULES-CLAUSE") || !strings.Contains(u.Block, "CLAUDE-ONLY-CLAUSE") {
		t.Errorf("unregistered Project block %q, want the global clauses", u.Block)
	}
	if !strings.Contains(u.Hint, unknown) {
		t.Errorf("unregistered Project hint %q does not name the Project", u.Hint)
	}

	for _, bad := range []string{"not-a-project", "../../etc", "github.com/acme/shop%0A", "a/b/c/d"} {
		status, body := itemsGet(t, srv, readerToken, "/api/v1/instructions?target=claude&project="+bad)
		if status != http.StatusBadRequest {
			t.Errorf("project=%s: status %d, want 400: %s", bad, status, body)
			continue
		}
		if code, _ := errorHint(t, body); code != "bad_request" {
			t.Errorf("project=%s: code %q, want bad_request", bad, code)
		}
	}
}

// Criterion: the endpoint needs a token like every other route.
func TestInstructionsNeedAToken(t *testing.T) {
	srv := instructionsServer(t, "")
	instructionsGet(t, srv, "?target=claude") // a reader token is enough
	const wrong = "wrong-token-presented-here"
	for name, auth := range map[string]func(*http.Request){
		"no header":   nil,
		"wrong token": bearer(wrong),
		"token in query": func(r *http.Request) {
			r.URL.RawQuery += "&access_token=" + readerToken
		},
	} {
		resp, body := do(t, http.MethodGet, srv.URL+"/api/v1/instructions?target=claude", auth)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401: %s", name, resp.StatusCode, body)
			continue
		}
		if code, _ := errorHint(t, body); code != "unauthorized" {
			t.Errorf("%s: code %q, want unauthorized", name, code)
		}
		if strings.Contains(string(body), "CLAUDE-ONLY") || strings.Contains(string(body), wrong) {
			t.Errorf("%s: the answer leaks content or echoes the token: %s", name, body)
		}
	}
}

// A request with no Project, or an unregistered one, and no clause that
// applies gets the default block without an empty "()" where the Project
// would be named.
func TestInstructionsDefaultBlockWithoutAProjectHasNoEmptyParentheses(t *testing.T) {
	cfg := loadedConfig(t, nil)
	cfg.ContentDir = t.TempDir()
	cfg.APITokens = namedTokenLines()
	cfg.RateLimit = roomyRateLimit
	h, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	a := instructionsGet(t, srv, "?target=agents")
	if strings.Contains(a.Block, "()") || !strings.HasPrefix(a.Block, "This repository uses Goldie") {
		t.Errorf("default block %q, want the sentence without a parenthetical", a.Block)
	}
	// An unregistered Project resolves as no Project: the same block, and the
	// hint, not the block, names the Project.
	const unknown = "github.com/acme/unknown"
	u := instructionsGet(t, srv, "?target=agents&project="+unknown)
	if u.Block != a.Block || !strings.Contains(u.Hint, unknown) {
		t.Errorf("unregistered Project answer %+v, want the block %q and a hint naming it", u, a.Block)
	}
}

// bindingDecisionsSentence is the closing sentence of the built-in default
// block (#716), exactly as the issue words it.
const bindingDecisionsSentence = "Accepted decisions (global and project) are binding. If a change would contradict one, stop and say so, and propose a new decision instead of silently deviating."

// Criterion (#716): the default block, served for a Project and target with no
// resolving clause, ends with the binding-decisions sentence, exactly.
func TestInstructionsDefaultBlockEndsWithTheBindingDecisionsSentence(t *testing.T) {
	cfg := loadedConfig(t, nil)
	cfg.ContentDir = t.TempDir()
	cfg.APITokens = namedTokenLines()
	cfg.RateLimit = roomyRateLimit
	h, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	for _, q := range []string{
		"?target=agents", "?target=claude",
		"?target=claude&project=" + url.QueryEscape(itemsProject),
	} {
		a := instructionsGet(t, srv, q)
		if !strings.HasSuffix(a.Block, bindingDecisionsSentence) {
			t.Errorf("%s: default block %q does not end with %q", q, a.Block, bindingDecisionsSentence)
		}
		if n := strings.Count(a.Block, bindingDecisionsSentence); n != 1 {
			t.Errorf("%s: the sentence appears %d times, want once", q, n)
		}
	}
}

// Criterion (#716): a Project with its own resolving clause gets that clause
// unchanged; the sentence is never appended to it.
func TestInstructionsResolvingClauseIsServedWithoutTheBindingDecisionsSentence(t *testing.T) {
	srv := instructionsServer(t, "")
	for _, q := range []string{
		"?target=claude&project=" + url.QueryEscape(itemsProject),
		"?target=agents&project=" + url.QueryEscape(itemsProject),
		"?target=claude",
	} {
		a := instructionsGet(t, srv, q)
		if strings.Contains(a.Block, "Accepted decisions") || strings.Contains(a.Block, "are binding") {
			t.Errorf("%s: block %q carries the default clause's sentence", q, a.Block)
		}
	}
}
