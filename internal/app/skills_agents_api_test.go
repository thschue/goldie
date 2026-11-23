package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #407 (slice 4a of #15), the HTTP contract seam: list
// and get for skills and agents, through the public handler PublicHandler
// builds. Every response is checked against the served spec by do.
//
// The contract they fix, beyond the spec:
//   - the four operations are GET /api/v1/skills, /api/v1/skills/{name},
//     /api/v1/agents and /api/v1/agents/{name};
//   - a list is an object keyed by the plural noun, as ProjectList is
//     ({"skills": [...]}, {"agents": [...]}), sorted by name, one entry per
//     name: the one that answers in the Project's view (nearest Scope wins,
//     a skill with its whole bundle);
//   - a skill, in a list and on its own, has name and files, each file
//     {path, size} (bytes) with slash paths relative to the bundle, sorted,
//     SKILL.md included, and no other keys; the skill carries no digest and
//     no requires_sync yet (#16);
//   - an agent has name and description;
//   - project= and drafts= behave as for articles: an unregistered Project
//     gets the global answer plus a hint, an invalid one is a 400, drafts=true
//     needs an operator token (403 for a reader, before anything is looked
//     up) and a draft agent is invisible without it, to operators too;
//   - a miss is a 404 not_found envelope with a hint, and echoes nothing of
//     the request;
//   - Activity records a get of a skill or an agent with Kind "get", the name
//     asked for as Query, the token name, the X-Goldie-Client version and the
//     Project, flagging a 404 as a Miss.

// skillsAgentsContent is a dir Source with the skills deploy (global, with a script)
// and lint, the agents reviewer, planner and a draft wip-agent, and a Project
// github.com/acme/shop whose deploy bundle and reviewer shadow the global ones.
const skillsAgentsContent = "testdata/skills-agents-content"

const itemsProject = "github.com/acme/shop"

func skillsAgentsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: skillsAgentsContent, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)
	return srv
}

type itemFile struct {
	Path string `json:"path"`
	Size int    `json:"size"`
}

type itemEntry struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Files       []itemFile `json:"files"`
	Hint        string     `json:"hint"`
}

// itemsGet sends one GET with the token and returns the status and body.
func itemsGet(t *testing.T, srv *httptest.Server, token, path string) (int, []byte) {
	t.Helper()
	resp, body := do(t, http.MethodGet, srv.URL+path, bearer(token))
	return resp.StatusCode, body
}

// itemsList reads a 200 list under the key and returns its entries.
func itemsList(t *testing.T, srv *httptest.Server, token, path, key string) []itemEntry {
	t.Helper()
	status, body := itemsGet(t, srv, token, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, status, body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, body)
	}
	if _, ok := raw[key]; !ok {
		t.Fatalf("GET %s: no %q key: %s", path, key, body)
	}
	var out []itemEntry
	if err := json.Unmarshal(raw[key], &out); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, body)
	}
	return out
}

// itemsOne reads a 200 single item.
func itemsOne(t *testing.T, srv *httptest.Server, token, path string) itemEntry {
	t.Helper()
	status, body := itemsGet(t, srv, token, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, status, body)
	}
	var e itemEntry
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, body)
	}
	return e
}

func itemNames(es []itemEntry) []string {
	names := make([]string, 0, len(es))
	for _, e := range es {
		names = append(names, e.Name)
	}
	return names
}

func wantFiles(t *testing.T, label string, e itemEntry, want []itemFile) {
	t.Helper()
	if !slices.Equal(e.Files, want) {
		t.Errorf("%s: files %+v, want %+v", label, e.Files, want)
	}
}

// The bytes of the fixture files: "# Deploy global\n", "#!/bin/sh\necho hi\n",
// "# Deploy shop\n", "check it\n" and "# Lint\n".
var (
	globalDeployFiles = []itemFile{{"SKILL.md", 16}, {"scripts/run.sh", 18}}
	shopDeployFiles   = []itemFile{{"SKILL.md", 14}, {"checklist.txt", 9}}
)

// Criterion: the served spec declares the four operations.
func TestServedSpecDeclaresSkillAndAgentOperations(t *testing.T) {
	ops := specOperations(t)
	for _, want := range []string{
		"GET /api/v1/skills", "GET /api/v1/skills/{name}",
		"GET /api/v1/agents", "GET /api/v1/agents/{name}",
	} {
		if !slices.Contains(ops, want) {
			t.Errorf("the spec declares no %s; it declares %v", want, ops)
		}
	}
}

// Criterion: a skill list names each skill once, sorted, with its file list
// (path, size).
func TestListSkillsCarriesEachSkillWithItsFileList(t *testing.T) {
	srv := skillsAgentsServer(t)
	skills := itemsList(t, srv, readerToken, "/api/v1/skills", "skills")
	if got := itemNames(skills); !slices.Equal(got, []string{"deploy", "lint"}) {
		t.Fatalf("skills %v, want [deploy lint]", got)
	}
	wantFiles(t, "deploy", skills[0], globalDeployFiles)
	wantFiles(t, "lint", skills[1], []itemFile{{"SKILL.md", 7}})
}

// Criterion: a skill get answers its file list, and nothing that waits for
// goldie pull (#16): no digest, no requires_sync, no executable bit.
func TestGetSkillCarriesItsFileListAndNothingFromPull(t *testing.T) {
	srv := skillsAgentsServer(t)
	e := itemsOne(t, srv, readerToken, "/api/v1/skills/deploy")
	if e.Name != "deploy" {
		t.Errorf("name %q, want deploy", e.Name)
	}
	wantFiles(t, "deploy", e, globalDeployFiles)
	if e.Hint != "" {
		t.Errorf("a global answer carries the hint %q", e.Hint)
	}

	_, body := itemsGet(t, srv, readerToken, "/api/v1/skills/deploy")
	var raw struct {
		Files []map[string]any `json:"files"`
	}
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(body, &raw)
	for _, k := range []string{"digest", "requires_sync"} {
		if _, ok := top[k]; ok {
			t.Errorf("the skill answer carries %q before #16: %s", k, body)
		}
	}
	for _, f := range raw.Files {
		if len(f) != 2 {
			t.Errorf("a file entry has keys %v, want only path and size", f)
		}
	}
}

// Criterion: project= resolves as for articles: the Project's bundle wins
// whole, in the list and on its own.
func TestSkillsResolveForTheProjectNearestScopeWinning(t *testing.T) {
	srv := skillsAgentsServer(t)
	q := "?project=" + url.QueryEscape(itemsProject)
	wantFiles(t, "get", itemsOne(t, srv, readerToken, "/api/v1/skills/deploy"+q), shopDeployFiles)
	skills := itemsList(t, srv, readerToken, "/api/v1/skills"+q, "skills")
	if got := itemNames(skills); !slices.Equal(got, []string{"deploy", "lint"}) {
		t.Fatalf("skills %v, want [deploy lint], one entry per name", got)
	}
	wantFiles(t, "list", skills[0], shopDeployFiles)
	if h := itemsOne(t, srv, readerToken, "/api/v1/skills/deploy"+q).Hint; h != "" {
		t.Errorf("a registered Project's answer carries the hint %q", h)
	}
	// The Project spelling is matched ignoring case, as for articles.
	wantFiles(t, "upper-case project", itemsOne(t, srv, readerToken,
		"/api/v1/skills/deploy?project="+url.QueryEscape("github.com/ACME/Shop")), shopDeployFiles)
}

// Criterion: an unregistered Project gets the global answer and a hint that
// names the Project, for all four operations.
func TestUnregisteredProjectGetsTheGlobalAnswerWithAHint(t *testing.T) {
	srv := skillsAgentsServer(t)
	q := "?project=github.com/acme/unknown"
	e := itemsOne(t, srv, readerToken, "/api/v1/skills/deploy"+q)
	wantFiles(t, "skill", e, globalDeployFiles)
	if !strings.Contains(e.Hint, "github.com/acme/unknown") {
		t.Errorf("skill hint %q does not name the Project", e.Hint)
	}
	a := itemsOne(t, srv, readerToken, "/api/v1/agents/reviewer"+q)
	if a.Description != "Reviews code globally" || !strings.Contains(a.Hint, "github.com/acme/unknown") {
		t.Errorf("agent %+v, want the global reviewer with a hint naming the Project", a)
	}
}

// Criterion: an invalid project is a 400 envelope on all four operations.
func TestInvalidProjectIsABadRequestOnAllFourOperations(t *testing.T) {
	srv := skillsAgentsServer(t)
	for _, base := range []string{"/api/v1/skills", "/api/v1/skills/deploy", "/api/v1/agents", "/api/v1/agents/reviewer"} {
		for _, bad := range []string{"not-a-project", "../../etc", "github.com/acme/shop%0A", "a/b/c/d"} {
			status, body := itemsGet(t, srv, readerToken, base+"?project="+bad)
			if status != http.StatusBadRequest {
				t.Errorf("GET %s?project=%s: status %d, want 400: %s", base, bad, status, body)
				continue
			}
			if code, _ := errorHint(t, body); code != "bad_request" {
				t.Errorf("GET %s?project=%s: code %q, want bad_request", base, bad, code)
			}
		}
	}
}

// Criterion: an agent list and get answer name and description, and the
// Project's agent shadows the global one.
func TestAgentsListAndGetWithProjectShadowing(t *testing.T) {
	srv := skillsAgentsServer(t)
	agents := itemsList(t, srv, readerToken, "/api/v1/agents", "agents")
	if got := itemNames(agents); !slices.Equal(got, []string{"planner", "reviewer"}) {
		t.Fatalf("agents %v, want [planner reviewer]; the draft is left out", got)
	}
	if agents[0].Description != "Plans work" || agents[1].Description != "Reviews code globally" {
		t.Errorf("agents %+v: wrong descriptions", agents)
	}
	if e := itemsOne(t, srv, readerToken, "/api/v1/agents/reviewer"); e.Name != "reviewer" || e.Description != "Reviews code globally" {
		t.Errorf("global reviewer %+v", e)
	}

	q := "?project=" + url.QueryEscape(itemsProject)
	if e := itemsOne(t, srv, readerToken, "/api/v1/agents/reviewer"+q); e.Description != "Reviews shop code" || e.Hint != "" {
		t.Errorf("shop reviewer %+v, want the Project's description and no hint", e)
	}
	shop := itemsList(t, srv, readerToken, "/api/v1/agents"+q, "agents")
	if got := itemNames(shop); !slices.Equal(got, []string{"planner", "reviewer"}) || shop[1].Description != "Reviews shop code" {
		t.Errorf("shop agents %+v, want planner and the Project's reviewer", shop)
	}
}

// Criterion: a miss is a 404 not_found envelope with a hint; a name that
// tries to leave the bundle or the directory is a miss too, and nothing of
// the request is echoed.
func TestSkillAndAgentMissesAreNotFoundWithAHint(t *testing.T) {
	srv := skillsAgentsServer(t)
	// The routes exist: a hit answers 200, so a miss below is the handler's,
	// not the router's "no such endpoint".
	itemsOne(t, srv, readerToken, "/api/v1/skills/deploy")
	itemsOne(t, srv, readerToken, "/api/v1/agents/reviewer")
	for _, base := range []string{"/api/v1/skills/", "/api/v1/agents/"} {
		for _, name := range []string{"nope", "..%2F..%2Fglobal%2Farticles%2Fintro", "deploy%2FSKILL.md", "%2Fetc%2Fpasswd", "wip-agent"} {
			status, body := itemsGet(t, srv, readerToken, base+name)
			if status != http.StatusNotFound {
				t.Errorf("GET %s%s: status %d, want 404: %s", base, name, status, body)
				continue
			}
			code, hint := errorHint(t, body)
			if code != "not_found" || strings.TrimSpace(hint) == "" || strings.Contains(hint, "OpenAPI document") {
				t.Errorf("GET %s%s: code %q hint %q, want not_found with a hint", base, name, code, hint)
			}
			if strings.Contains(string(body), "passwd") || strings.Contains(string(body), "wip-marker") {
				t.Errorf("GET %s%s: the answer echoes the request or leaks a draft: %s", base, name, body)
			}
		}
	}
}

// Criterion: a draft agent is invisible without drafts=true, to an operator
// too, and a reader asking for drafts gets the 403 envelope, on skills as well.
func TestDraftsAreOperatorOnlyAndDraftAgentsAreHiddenByDefault(t *testing.T) {
	srv := skillsAgentsServer(t)
	if got := itemNames(itemsList(t, srv, operatorToken, "/api/v1/agents", "agents")); slices.Contains(got, "wip-agent") {
		t.Errorf("an operator without drafts sees %v", got)
	}
	if got := itemNames(itemsList(t, srv, operatorToken, "/api/v1/agents?drafts=false", "agents")); slices.Contains(got, "wip-agent") {
		t.Errorf("drafts=false lists %v", got)
	}
	if status, body := itemsGet(t, srv, operatorToken, "/api/v1/agents/wip-agent"); status != http.StatusNotFound {
		t.Errorf("a draft agent without drafts: status %d, want 404: %s", status, body)
	}

	got := itemNames(itemsList(t, srv, operatorToken, "/api/v1/agents?drafts=true", "agents"))
	if !slices.Equal(got, []string{"planner", "reviewer", "wip-agent"}) {
		t.Errorf("an operator with drafts=true lists %v, want the draft too", got)
	}
	if e := itemsOne(t, srv, operatorToken, "/api/v1/agents/wip-agent?drafts=true"); e.Name != "wip-agent" {
		t.Errorf("draft agent %+v", e)
	}

	for _, p := range []string{
		"/api/v1/skills?drafts=true", "/api/v1/skills/deploy?drafts=true",
		"/api/v1/agents?drafts=true", "/api/v1/agents/wip-agent?drafts=true",
		"/api/v1/skills/nope?drafts=true",
	} {
		status, body := itemsGet(t, srv, readerToken, p)
		if status != http.StatusForbidden {
			t.Errorf("reader GET %s: status %d, want 403: %s", p, status, body)
			continue
		}
		code, hint := errorHint(t, body)
		if code != "forbidden" || !strings.Contains(hint, "operator") {
			t.Errorf("reader GET %s: code %q hint %q, want forbidden naming the operator role", p, code, hint)
		}
		if strings.Contains(string(body), "wip-marker") {
			t.Errorf("reader GET %s leaks the draft: %s", p, body)
		}
	}
	if status, body := itemsGet(t, srv, operatorToken, "/api/v1/skills?drafts=true"); status != http.StatusOK {
		t.Errorf("operator skills with drafts=true: status %d, want 200: %s", status, body)
	}
	if status, body := itemsGet(t, srv, readerToken, "/api/v1/agents?drafts=maybe"); status != http.StatusBadRequest {
		t.Errorf("drafts=maybe: status %d, want 400: %s", status, body)
	}
}

// Criterion: the four operations need a token, as every /api/ route does.
func TestSkillAndAgentOperationsNeedAToken(t *testing.T) {
	srv := skillsAgentsServer(t)
	for _, p := range []string{"/api/v1/skills", "/api/v1/skills/deploy", "/api/v1/agents", "/api/v1/agents/reviewer"} {
		for label, mutate := range map[string]func(*http.Request){
			"no token":    nil,
			"wrong token": bearer("wrong-token-0123456789abcdef0123456789abcdef"),
		} {
			resp, body := do(t, http.MethodGet, srv.URL+p, mutate)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("GET %s with %s: status %d, want 401: %s", p, label, resp.StatusCode, body)
			}
		}
	}
}

// Criterion: Activity records a get of a skill or an agent with the new Kind
// "get", the name asked for, the token name, the client version and the
// Project.
func TestGetIsRecordedInActivityWithKindGet(t *testing.T) {
	for _, base := range []string{"/api/v1/skills/", "/api/v1/agents/"} {
		pub := activityHandler(t, platform.Config{ContentDir: skillsAgentsContent})
		name, query := "deploy", "skill:deploy"
		if strings.Contains(base, "agents") {
			name, query = "reviewer", "agent:reviewer"
		}
		e := lastEvent(t, pub, base+name+"?project="+itemsProject, http.StatusOK, asClient(operatorToken, "goldie/v1.5.0"))
		if string(e.Kind) != "get" || e.Query != query {
			t.Errorf("%s: event kind %q query %q, want get %q", base, e.Kind, e.Query, query)
		}
		if e.Token != "ci-operator" || e.ClientVersion != "1.5.0" || e.Project != itemsProject {
			t.Errorf("%s: event token %q client %q project %q, want ci-operator, 1.5.0, %s", base, e.Token, e.ClientVersion, e.Project, itemsProject)
		}
		if e.Miss {
			t.Errorf("%s: a get answering 200 is recorded as a Miss", base)
		}
		assertNoSecret(t, waitEvents(t, pub.Activity(), 1))
	}
}

// Criterion: a get answering 404 is recorded as a Miss, and a request that is
// refused (bad project, drafts as a reader) is not recorded at all.
func TestMissingGetIsAMissAndRefusedRequestsAreNotRecorded(t *testing.T) {
	pub := activityHandler(t, platform.Config{ContentDir: skillsAgentsContent})
	e := lastEvent(t, pub, "/api/v1/skills/nope", http.StatusNotFound, nil)
	if string(e.Kind) != "get" || e.Query != "skill:nope" || !e.Miss {
		t.Errorf("event %+v, want a get of skill:nope flagged as a Miss", e)
	}
	e = lastEvent(t, pub, "/api/v1/agents/nope", http.StatusNotFound, nil)
	if string(e.Kind) != "get" || e.Query != "agent:nope" || !e.Miss {
		t.Errorf("event %+v, want a get of agent:nope flagged as a Miss", e)
	}
	before := len(waitEvents(t, pub.Activity(), 0))
	for _, p := range []string{"/api/v1/skills/deploy?project=not-a-project", "/api/v1/agents/reviewer?drafts=true"} {
		if rec := serveAs(t, pub, p, func(*http.Request) {}); rec.Code != http.StatusBadRequest && rec.Code != http.StatusForbidden {
			t.Errorf("GET %s: status %d, want 400 or 403", p, rec.Code)
		}
	}
	if after := len(waitEvents(t, pub.Activity(), 0)); after != before {
		t.Errorf("refused requests were recorded: %d events, want %d", after, before)
	}
}
