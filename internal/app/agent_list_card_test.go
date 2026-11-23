package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #586: GET /api/v1/agents lists each agent with two
// optional fields of Agent: scope (the value SearchHit.scope carries) and
// status. An absent field means unknown. Fixtures are written per test:
//   - reviewer: global, and a shop copy that shadows it;
//   - planner: global only;
//   - wip-agent: global draft.

type cardAgent struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Scope       *string `json:"scope"`
	Status      *string `json:"status"`
}

func agentCardServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	writeRaw(t, dir, "global/agents/reviewer.md", "---\nname: reviewer\ndescription: Reviews globally\n---\nbody\n")
	writeRaw(t, dir, "global/agents/planner.md", "---\nname: planner\ndescription: Plans work\n---\nbody\n")
	writeRaw(t, dir, "global/agents/wip-agent.md", "---\nname: wip-agent\ndescription: wip marker\nstatus: draft\n---\nbody\n")
	writeRaw(t, dir, "projects/"+cardShop+"/agents/reviewer.md", "---\nname: reviewer\ndescription: Reviews the shop\n---\nbody\n")
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: dir, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)
	return srv
}

func agentCardList(t *testing.T, srv *httptest.Server, token, query string) map[string]cardAgent {
	t.Helper()
	path := "/api/v1/agents"
	if query != "" {
		path += "?" + query
	}
	status, body := itemsGet(t, srv, token, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, status, body)
	}
	var res struct {
		Agents []cardAgent `json:"agents"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, body)
	}
	out := map[string]cardAgent{}
	for _, a := range res.Agents {
		out[a.Name] = a
	}
	return out
}

func wantAgentScope(t *testing.T, label string, a cardAgent, want string) {
	t.Helper()
	if a.Scope == nil {
		t.Errorf("%s: scope is absent, want %q", label, want)
	} else if *a.Scope != want {
		t.Errorf("%s: scope = %q, want %q", label, *a.Scope, want)
	}
}

// Criterion: Agent gains optional scope and status, outside required, no enum.
func TestServedSpecDeclaresTheAgentScopeAndStatusAsOptional(t *testing.T) {
	doc := specDoc(t)
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	agent, _ := schemas["Agent"].(map[string]any)
	props, _ := agent["properties"].(map[string]any)
	for _, name := range []string{"scope", "status"} {
		p := contracttest.ResolveRef(doc, props[name])
		if p == nil {
			t.Errorf("Agent declares no %s property", name)
			continue
		}
		if p["type"] != "string" {
			t.Errorf("Agent.%s type %v, want string", name, p["type"])
		}
		if _, ok := p["enum"]; ok {
			t.Errorf("Agent.%s has an enum %v; the value is open (ADR 0011)", name, p["enum"])
		}
		if d, _ := p["description"].(string); strings.TrimSpace(d) == "" {
			t.Errorf("Agent.%s has no description", name)
		}
	}
	req, _ := agent["required"].([]any)
	for _, r := range req {
		if r == "scope" || r == "status" {
			t.Errorf("Agent requires %v; the field is optional (additive, ADR 0011)", r)
		}
	}
	if !slices.Contains(req, any("name")) || !slices.Contains(req, any("description")) {
		t.Errorf("Agent required = %v, must still be name and description", req)
	}
}

// Criterion: scope is global, or the Project whose copy won; a shadowing copy
// reports its own Scope.
func TestAgentListNamesTheScopeOfEachCopy(t *testing.T) {
	srv := agentCardServer(t)
	plain := agentCardList(t, srv, readerToken, "")
	wantAgentScope(t, "reviewer, no Project", plain["reviewer"], "global")
	wantAgentScope(t, "planner, no Project", plain["planner"], "global")

	shop := agentCardList(t, srv, readerToken, "project="+url.QueryEscape(cardShop))
	wantAgentScope(t, "reviewer in the shop view", shop["reviewer"], cardShop)
	if shop["reviewer"].Description != "Reviews the shop" {
		t.Errorf("shop reviewer description = %q, want the copy's", shop["reviewer"].Description)
	}
	wantAgentScope(t, "planner in the shop view", shop["planner"], "global")

	other := agentCardList(t, srv, readerToken, "project=github.com/acme/unknown")
	wantAgentScope(t, "reviewer in an unregistered Project's view", other["reviewer"], "global")
}

// Criterion: the agent scope uses the same value as SearchHit.scope.
func TestAgentListScopeEqualsTheSearchHitScope(t *testing.T) {
	srv := agentCardServer(t)
	for _, project := range []string{"", cardShop} {
		q := url.Values{"q": {"reviewer"}, "kind": {"agent"}}
		query := ""
		if project != "" {
			q.Set("project", project)
			query = "project=" + url.QueryEscape(project)
		}
		status, body := itemsGet(t, srv, readerToken, "/api/v1/search?"+q.Encode())
		if status != http.StatusOK {
			t.Fatalf("search project %q: status %d: %s", project, status, body)
		}
		var res struct {
			Results []lifecycleHit `json:"results"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatal(err)
		}
		list := agentCardList(t, srv, readerToken, query)
		found := false
		for _, h := range res.Results {
			if h.ID != "reviewer" {
				continue
			}
			found = true
			wantAgentScope(t, "reviewer in project "+project+" (search says "+h.Scope+")", list["reviewer"], h.Scope)
		}
		if !found {
			t.Errorf("search project %q found no reviewer agent hit: %s", project, body)
		}
	}
}

// Criterion: a draft agent is listed only with drafts=true and the operator
// role, carries status draft, and a reader asking for drafts is a 403.
func TestDraftAgentIsListedOnlyForAnOperatorAskingForDrafts(t *testing.T) {
	srv := agentCardServer(t)
	for _, q := range []string{"", "drafts=false"} {
		if _, ok := agentCardList(t, srv, operatorToken, q)["wip-agent"]; ok {
			t.Errorf("an operator with %q sees the draft agent", q)
		}
		if _, ok := agentCardList(t, srv, readerToken, q)["wip-agent"]; ok {
			t.Errorf("a reader with %q sees the draft agent", q)
		}
	}

	list := agentCardList(t, srv, operatorToken, "drafts=true")
	wip, ok := list["wip-agent"]
	if !ok {
		t.Fatalf("an operator with drafts=true does not see the draft agent: %v", list)
	}
	if wip.Status == nil || *wip.Status != "draft" {
		t.Errorf("draft agent status = %v, want draft", wip.Status)
	}
	wantAgentScope(t, "draft agent", wip, "global")
	if _, ok := list["planner"]; !ok {
		t.Errorf("drafts=true dropped the published agents: %v", list)
	}

	status, body := itemsGet(t, srv, readerToken, "/api/v1/agents?drafts=true")
	if status != http.StatusForbidden {
		t.Errorf("reader GET agents?drafts=true: status %d, want 403: %s", status, body)
	}
	if strings.Contains(string(body), "wip-agent") {
		t.Errorf("the 403 for a reader leaks the draft: %s", body)
	}
}

// Criterion: an absent field means unknown. A published agent lists without
// status, and the existing keys stay for old clients.
func TestAgentListKeepsItsKeysAndLeavesStatusAbsentWhenPublished(t *testing.T) {
	srv := agentCardServer(t)
	_, body := itemsGet(t, srv, readerToken, "/api/v1/agents")
	var res struct {
		Agents []map[string]any `json:"agents"`
	}
	if err := json.Unmarshal(body, &res); err != nil || len(res.Agents) != 2 {
		t.Fatalf("body %s: %v", body, err)
	}
	for _, a := range res.Agents {
		if a["name"] == nil || a["description"] == nil {
			t.Errorf("an agent lost name or description: %v", a)
		}
		if v, ok := a["status"]; ok {
			t.Errorf("published agent %v lists status %v, want it absent", a["name"], v)
		}
		if a["scope"] != "global" {
			t.Errorf("agent %v scope = %v, want global", a["name"], a["scope"])
		}
	}
}

// A nearer draft copy never hides the published global agent; with drafts=true
// the operator sees the nearer copy with its Scope and status.
func TestNearerDraftAgentDoesNotHideThePublishedGlobalOne(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "global/agents/reviewer.md", "---\nname: reviewer\ndescription: Reviews globally\n---\nbody\n")
	writeRaw(t, dir, "projects/"+cardShop+"/agents/reviewer.md", "---\nname: reviewer\ndescription: Shop draft\nstatus: draft\n---\nbody\n")
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: dir, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)

	q := "project=" + url.QueryEscape(cardShop)
	wantAgentScope(t, "reviewer under a shop draft", agentCardList(t, srv, readerToken, q)["reviewer"], "global")
	drafts := agentCardList(t, srv, operatorToken, q+"&drafts=true")
	wantAgentScope(t, "reviewer with drafts=true", drafts["reviewer"], cardShop)
	if s := drafts["reviewer"].Status; s == nil || *s != "draft" {
		t.Errorf("shop reviewer status = %v, want draft", s)
	}
}
