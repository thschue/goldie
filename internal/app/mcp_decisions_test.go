package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	gmcp "github.com/thschue/goldie/internal/mcp"
)

// Acceptance tests for #735 (slice of #34), the MCP protocol seam: the tool
// find_decisions and the resource goldie://decisions/{name}, over the real
// public handler and the decisions fixtures of decisions_api_test.go and
// decisions_search_test.go (alpha accepted with keywords db and queue; marmot
// accepted; marmot-new proposed; marmot-no rejected; marmot-old deprecated; the
// chain quokka-1 and quokka-2 superseded by the accepted quokka-3; the Project
// github.com/acme/shop with shop-pick and its own alpha). Pattern followed:
// mcp_loops_test.go and mcp_prompts_resources_test.go.
//
// The contract they fix, beyond the issue:
//   - find_decisions is advertised next to the ten existing tools with a
//     description and the schema of find_skills: a required string query and an
//     optional string project, nothing else;
//   - a call answers the accepted decisions that match, each with its name (the
//     id for the resource), its title and the Scope that answered; no match is
//     an answer ("No decisions ..."), not an error. Only accepted decisions are
//     hits and a superseded one is replaced by its accepted successor (that is
//     what search kind=decision does, #734);
//   - goldie://decisions/{name} answers the decision's title, its status and its
//     body, in the session's Project view. Only an accepted decision is read; a
//     proposed, rejected, deprecated or superseded one, an unknown name and a
//     malformed URI give the protocol's resource-not-found error, reworded to
//     name find_decisions, with no address, path or token and no body;
//   - the template is advertised with the others.

const decisionsTemplate = "goldie://decisions/{name}"

func connectDecisions(t *testing.T, opts ...gmcp.Option) *mcp.ClientSession {
	t.Helper()
	srv, _ := decisionServer(t, searchDecisionEdits())
	return sessionFor(t, srv.URL, readerToken, opts...)
}

func TestMCPFindDecisionsIsAdvertisedWithItsInputSchema(t *testing.T) {
	res, err := connectDecisions(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		if tl.Name != "find_decisions" {
			continue
		}
		if tl.Description == "" {
			t.Error("find_decisions has no description")
		}
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			Type       string `json:"type"`
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		if s.Type != "object" || len(s.Properties) != 2 || s.Properties["query"].Type != "string" ||
			s.Properties["project"].Type != "string" || !slices.Equal(s.Required, []string{"query"}) {
			t.Errorf("find_decisions: want a required string query and an optional string project, got %s", raw)
		}
		return
	}
	t.Error("find_decisions is not among the tools")
}

func TestMCPFindDecisionsAnswersAcceptedDecisionsWithNameTitleAndScope(t *testing.T) {
	cs := connectDecisions(t)
	text, isErr := call(t, cs, "find_decisions", map[string]any{"query": "queue"})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	for _, want := range []string{"alpha", "Alpha", "global"} {
		if !strings.Contains(text, want) {
			t.Errorf("find_decisions lacks %q: %s", want, text)
		}
	}
	// A title word matches too, and only accepted decisions are answered.
	text, isErr = call(t, cs, "find_decisions", map[string]any{"query": "marmot"})
	if isErr || !strings.Contains(text, "Marmot cache") {
		t.Fatalf("marmot: want the accepted decision: %v %s", isErr, text)
	}
	for _, bad := range []string{"marmot-new", "marmot-no", "marmot-old", "Marmot proposal", "Marmot refusal", "Marmot legacy"} {
		if strings.Contains(text, bad) {
			t.Errorf("find_decisions answered %q, which is not accepted: %s", bad, text)
		}
	}
}

func TestMCPFindDecisionsReplacesASupersededDecisionByItsSuccessor(t *testing.T) {
	text, isErr := call(t, connectDecisions(t), "find_decisions", map[string]any{"query": "quokka"})
	if isErr || !strings.Contains(text, "quokka-3") || !strings.Contains(text, "Wombat store") {
		t.Fatalf("want the accepted successor: %v %s", isErr, text)
	}
	for _, bad := range []string{"quokka-1", "quokka-2", "Quokka store"} {
		if strings.Contains(text, bad) {
			t.Errorf("find_decisions answered the superseded %q: %s", bad, text)
		}
	}
}

func TestMCPFindDecisionsFollowsTheProjectView(t *testing.T) {
	srv, _ := decisionServer(t, searchDecisionEdits())
	cs := sessionFor(t, srv.URL, readerToken)
	text, isErr := call(t, cs, "find_decisions", map[string]any{"query": "shop", "project": decisionsShop})
	if isErr || !strings.Contains(text, "shop-pick") || !strings.Contains(text, decisionsShop) {
		t.Errorf("project argument: want shop-pick with the Project's Scope: %v %s", isErr, text)
	}
	shop := sessionFor(t, srv.URL, readerToken, gmcp.WithProject(decisionsShop, "test"))
	text, isErr = call(t, shop, "find_decisions", map[string]any{"query": "shop"})
	if isErr || !strings.Contains(text, "shop-pick") {
		t.Errorf("session Project: want shop-pick: %v %s", isErr, text)
	}
	// The global view does not know the Project's decision.
	text, isErr = call(t, cs, "find_decisions", map[string]any{"query": "shop"})
	if isErr || strings.Contains(text, "shop-pick") {
		t.Errorf("global view: want no shop-pick: %v %s", isErr, text)
	}
}

// Negative: a query that matches only decisions that are not accepted, or none,
// is an answer that names no decision, not an error.
func TestMCPFindDecisionsWithNoMatchIsNotAnError(t *testing.T) {
	cs := connectDecisions(t)
	for _, q := range []string{"zzzqqq", "refusal", "proposal", "legacy"} {
		text, isErr := call(t, cs, "find_decisions", map[string]any{"query": q})
		if isErr || !strings.Contains(text, "No decisions") {
			t.Errorf("query %q: want an empty answer, not an error: %v %s", q, isErr, text)
		}
		for _, bad := range []string{"marmot", "Marmot"} {
			if strings.Contains(text, bad) {
				t.Errorf("query %q: the answer names %q: %s", q, bad, text)
			}
		}
	}
}

// Abuse: a wrong token or no API is a tool error that leaks nothing.
func TestMCPFindDecisionsWithAWrongTokenOrNoAPILeaksNothing(t *testing.T) {
	srv, _ := decisionServer(t, nil)
	wrong := sessionFor(t, srv.URL, "wrong-token-0123456789abcdef")
	down := session(t, "http://127.0.0.1:1", readerToken)
	for who, cs := range map[string]*mcp.ClientSession{"wrong token": wrong, "no API": down} {
		text, isErr := call(t, cs, "find_decisions", map[string]any{"query": "queue"})
		if !isErr {
			t.Errorf("%s: want a tool error: %s", who, text)
		}
		for _, bad := range []string{"127.0.0.1", "http://", "wrong-token", readerToken, "/api/", "Alpha"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: error leaks %q: %s", who, bad, text)
			}
		}
	}
}

func TestMCPDecisionResourceTemplateIsAdvertised(t *testing.T) {
	res, err := connectDecisions(t).ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, rt := range res.ResourceTemplates {
		if rt.URITemplate == decisionsTemplate {
			if rt.Name == "" {
				t.Errorf("resource template %s has no name", rt.URITemplate)
			}
			return
		}
	}
	t.Errorf("resource templates lack %s", decisionsTemplate)
}

func TestMCPResourceReadAnswersADecisionWithBodyAndStatus(t *testing.T) {
	cs := connectDecisions(t)
	text, _, err := readResource(cs, "goldie://decisions/alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Alpha", "accepted", "Body of Alpha."} {
		if !strings.Contains(text, want) {
			t.Errorf("the decision lacks %q: %q", want, text)
		}
	}
	if strings.Contains(text, "Zeta") || strings.Contains(text, "Shop Alpha") {
		t.Errorf("the answer shows another decision: %q", text)
	}
	// An accepted decision at the end of a chain is read; the session's
	// Project view decides which alpha answers.
	text, _, err = readResource(cs, "goldie://decisions/quokka-3")
	if err != nil || !strings.Contains(text, "Body of Wombat store.") {
		t.Errorf("quokka-3: %v %q", err, text)
	}
	shop := connectDecisions(t, gmcp.WithProject(decisionsShop, "test"))
	text, _, err = readResource(shop, "goldie://decisions/alpha")
	if err != nil || !strings.Contains(text, "Body of Shop Alpha.") {
		t.Errorf("session Project: want the Project's alpha: %v %q", err, text)
	}
}

// Negative: an unknown name, or a decision that is not accepted, is the miss
// reword (resource-not-found naming find_decisions), never an internal error
// and never the body.
func TestMCPDecisionResourceMissIsResourceNotFoundNamingFindDecisions(t *testing.T) {
	cs := connectDecisions(t)
	for _, uri := range []string{
		"goldie://decisions/no-such-decision",
		"goldie://decisions/marmot-new", // proposed
		"goldie://decisions/marmot-no",  // rejected
		"goldie://decisions/marmot-old", // deprecated
		"goldie://decisions/quokka-1",   // superseded
		"goldie://decisions/quokka-2",   // superseded
		"goldie://decisions/shop-pick",  // a Project's decision, not in the global view
	} {
		text, _, err := readResource(cs, uri)
		if err == nil {
			t.Errorf("read %s: want an error, got %q", uri, text)
			continue
		}
		resourceNotFound(t, err)
		if !strings.Contains(err.Error(), "find_decisions") {
			t.Errorf("read %s: the miss does not name find_decisions: %v", uri, err)
		}
		for _, bad := range append([]string{"Body of", "Marmot", "Quokka", "Shop Pick"}, promptResourceLeaks...) {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("read %s: the error leaks %q: %v", uri, bad, err)
			}
		}
	}
}

// Abuse: a malformed URI is an error and no other decision, file or agent
// answers.
func TestMCPDecisionResourceRefusesHostileAndMalformedURIs(t *testing.T) {
	cs := connectDecisions(t)
	for _, uri := range []string{
		"goldie://decisions/",
		"goldie://decisions/..",
		"goldie://decisions/../alpha",
		"goldie://decisions/alpha/..",
		"goldie://decisions/alpha/extra",
		"goldie://decisions/%2e%2e",
		"goldie://decisions/alpha%2F..%2Fzeta",
		"goldie://decisions/alpha%00",
		"goldie://decisions/alpha?project=" + decisionsShop,
		"goldie://decisions/" + strings.Repeat("a", 5000),
		"goldie://decisions/../agents/releaser",
		"goldie://decision/alpha",
	} {
		text, _, err := readResource(cs, uri)
		if err == nil {
			t.Errorf("read %q: want an error, got %q", uri, text)
			continue
		}
		for _, bad := range promptResourceLeaks {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("read %q: the error leaks %q: %v", uri, bad, err)
			}
		}
	}
}

func TestMCPDecisionResourceWithAWrongTokenLeaksNothing(t *testing.T) {
	srv, _ := decisionServer(t, nil)
	cs := sessionFor(t, srv.URL, "wrong-token-0123456789abcdef")
	text, _, err := readResource(cs, "goldie://decisions/alpha")
	if err == nil {
		t.Fatalf("a wrong token reads a decision: %q", text)
	}
	for _, bad := range append([]string{"Body of"}, promptResourceLeaks...) {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error leaks %q: %v", bad, err)
		}
	}
}
