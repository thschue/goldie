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

// Acceptance tests for #408 (slice 4b of #15), the HTTP contract seam: the
// skill-file endpoint and the kind filter on search, through the public
// handler PublicHandler builds. Every response is checked against the served
// spec by do.
//
// The contract they fix, beyond the spec:
//   - GET /api/v1/skills/{name}/files/{path} answers the file's bytes exactly
//     as written, 200, with one fixed Content-Type whatever the file's name or
//     content, and X-Content-Type-Options: nosniff. The path is one
//     percent-encoded segment (ADR 0016): scripts%2Frun.sh. The bare-slash
//     form, a name or path that leaves the bundle, an absolute path and an
//     unknown skill or file are the 404 not_found envelope with a hint, and
//     echo nothing of the request. The Project's bundle answers whole (nearest
//     Scope wins), so a file only the global bundle has is a miss for it;
//   - the endpoint needs a token, as every /api/ route does;
//   - search takes kind= (article, skill or agent); a hit carries its kind;
//     without kind= only articles are returned (PM decision, ADR 0011); a
//     kind that is none of the three is a 400 bad_request envelope;
//   - skills and agents match on name and keywords only, never on description
//     (ADR 0005). A skill's keywords are the keywords in the frontmatter of its
//     SKILL.md, an agent's those of its file; drafts stay hidden as for articles.

// skillSearchContent is a dir Source: the skills deployer (with page.html and a
// script, keywords rollout and ship) and canary, the agents releaser (keyword
// rollout), canary-agent and a draft wip-rollout (keyword rollout), the article
// "Rollout guide" (keyword rollout), the word zebrafruit only in descriptions,
// and a Project github.com/acme/shop whose deployer bundle shadows the global.
const skillSearchContent = "testdata/skills-search-content"

func skillSearchServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: skillSearchContent, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)
	return srv
}

type searchHitEntry struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Scope   string   `json:"scope"`
	Matched []string `json:"matched"`
}

func searchHits(t *testing.T, srv *httptest.Server, token, query string) []searchHitEntry {
	t.Helper()
	status, body := itemsGet(t, srv, token, "/api/v1/search?"+query)
	if status != http.StatusOK {
		t.Fatalf("GET search?%s: status %d, want 200: %s", query, status, body)
	}
	var res struct {
		Results []searchHitEntry `json:"results"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("GET search?%s: %v: %s", query, err, body)
	}
	return res.Results
}

func hitIDs(hs []searchHitEntry) []string {
	ids := make([]string, 0, len(hs))
	for _, h := range hs {
		ids = append(ids, h.ID)
	}
	slices.Sort(ids)
	return ids
}

func skillFilePath(name, file string) string {
	return "/api/v1/skills/" + name + "/files/" + url.PathEscape(file)
}

// Criterion: the served spec declares the file operation and the kind
// parameter, and the skill reference says a client reads each listed
// files[].path with the endpoint (folded from #521).
func TestServedSpecDeclaresSkillFileEndpointAndKindFilter(t *testing.T) {
	if ops := specOperations(t); !slices.Contains(ops, "GET /api/v1/skills/{name}/files/{path}") {
		t.Errorf("the spec declares no GET /api/v1/skills/{name}/files/{path}; it declares %v", ops)
	}
	paths, _ := specDoc(t)["paths"].(map[string]any)
	skill, _ := paths["/api/v1/skills/{name}"].(map[string]any)["get"].(map[string]any)
	desc, _ := skill["description"].(string)
	if !strings.Contains(desc, "files") || !strings.Contains(desc, "/files/") {
		t.Errorf("GET /api/v1/skills/{name} says nothing of reading files[].path with the file endpoint: %q", desc)
	}
	search, _ := paths["/api/v1/search"].(map[string]any)["get"].(map[string]any)
	params, _ := search["parameters"].([]any)
	var found bool
	for _, p := range params {
		if m, _ := p.(map[string]any); m["name"] == "kind" && m["in"] == "query" {
			found = true
		}
	}
	if !found {
		t.Error("the search operation declares no kind query parameter")
	}
}

// Criterion: the endpoint answers a file's bytes as written, nested paths as
// one encoded segment.
func TestSkillFileIsServedByteForByte(t *testing.T) {
	srv := skillSearchServer(t)
	for file, want := range map[string]string{
		"SKILL.md":       "---\nname: deployer\ndescription: Ships things with zebrafruit\nkeywords: [rollout, ship]\n---\n# Deployer global\n",
		"scripts/run.sh": "#!/bin/sh\necho hi\n",
		"page.html":      "<script>alert(1)</script>\n",
	} {
		status, body := itemsGet(t, srv, readerToken, skillFilePath("deployer", file))
		if status != http.StatusOK {
			t.Errorf("GET file %s: status %d, want 200: %s", file, status, body)
			continue
		}
		if string(body) != want {
			t.Errorf("GET file %s: body %q, want %q", file, body, want)
		}
	}
}

// Criterion: a fixed content type with nosniff, never sniffed or taken from
// the file's name: an HTML file is not served as HTML.
func TestSkillFileHasOneFixedContentTypeAndNosniff(t *testing.T) {
	srv := skillSearchServer(t)
	var first string
	for i, file := range []string{"SKILL.md", "scripts/run.sh", "page.html"} {
		resp, body := do(t, http.MethodGet, srv.URL+skillFilePath("deployer", file), bearer(readerToken))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET file %s: status %d, want 200: %s", file, resp.StatusCode, body)
		}
		ct := resp.Header.Get("Content-Type")
		if i == 0 {
			first = ct
		}
		if ct != first || ct == "" {
			t.Errorf("GET file %s: Content-Type %q, want the one fixed type %q", file, ct, first)
		}
		for _, bad := range []string{"html", "javascript", "svg", "xml"} {
			if strings.Contains(strings.ToLower(ct), bad) {
				t.Errorf("GET file %s: Content-Type %q is a type a browser would run or render", file, ct)
			}
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET file %s: X-Content-Type-Options %q, want nosniff", file, got)
		}
	}
}

// Criterion: the Project's bundle answers whole, nearest Scope winning.
func TestSkillFileResolvesForTheProjectBundle(t *testing.T) {
	srv := skillSearchServer(t)
	q := "?project=" + url.QueryEscape("github.com/acme/shop")
	for file, want := range map[string]string{
		"SKILL.md":      "---\nname: deployer\ndescription: Shop deployer\nkeywords: [rollout]\n---\n# Deployer shop\n",
		"checklist.txt": "check it\n",
	} {
		status, body := itemsGet(t, srv, readerToken, skillFilePath("deployer", file)+q)
		if status != http.StatusOK || string(body) != want {
			t.Errorf("GET file %s for the Project: status %d body %q, want 200 %q", file, status, body, want)
		}
	}
	// A file only the global bundle has is a miss for the Project.
	if status, body := itemsGet(t, srv, readerToken, skillFilePath("deployer", "scripts/run.sh")+q); status != http.StatusNotFound {
		t.Errorf("a global-only file for the Project: status %d, want 404: %s", status, body)
	}
	// And the Project's file is a miss without it.
	if status, body := itemsGet(t, srv, readerToken, skillFilePath("deployer", "checklist.txt")); status != http.StatusNotFound {
		t.Errorf("a Project-only file without the Project: status %d, want 404: %s", status, body)
	}
	if status, body := itemsGet(t, srv, readerToken, skillFilePath("deployer", "SKILL.md")+"?project=not%20a%20project"); status != http.StatusBadRequest {
		t.Errorf("an invalid project: status %d, want 400: %s", status, body)
	}
}

// Criterion: a miss is the 404 not_found envelope with a hint; the bare-slash
// form (ADR 0016), a path that leaves the bundle, an absolute path, a
// backslash or NUL path and an unknown skill are misses, and nothing of the
// request is echoed.
func TestSkillFileMissesAreNotFoundAndNeverLeaveTheBundle(t *testing.T) {
	srv := skillSearchServer(t)
	// The route exists: a hit answers 200, so a miss below is the handler's.
	if status, body := itemsGet(t, srv, readerToken, skillFilePath("deployer", "SKILL.md")); status != http.StatusOK {
		t.Fatalf("GET the SKILL.md: status %d, want 200: %s", status, body)
	}
	for _, p := range []string{
		"/api/v1/skills/deployer/files/nope.txt",
		"/api/v1/skills/deployer/files/scripts/run.sh",
		"/api/v1/skills/deployer/files/scripts",
		"/api/v1/skills/deployer/files/..%2F..%2Fglobal%2Farticles%2Frollout-guide.md",
		"/api/v1/skills/deployer/files/..%2Fcanary%2FSKILL.md",
		"/api/v1/skills/deployer/files/%2e%2e%2fSKILL.md",
		"/api/v1/skills/deployer/files/scripts%2F..%2F..%2Fcanary%2FSKILL.md",
		"/api/v1/skills/deployer/files/%2Fetc%2Fpasswd",
		"/api/v1/skills/deployer/files/..%5C..%5Cpasswd",
		"/api/v1/skills/deployer/files/SKILL.md%00.txt",
		"/api/v1/skills/nope/files/SKILL.md",
		"/api/v1/skills/..%2Fcanary/files/SKILL.md",
		"/api/v1/skills/deployer%2F..%2Fcanary/files/SKILL.md",
	} {
		status, body := itemsGet(t, srv, readerToken, p)
		if status != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404: %s", p, status, body)
			continue
		}
		code, hint := errorHint(t, body)
		if code != "not_found" || strings.TrimSpace(hint) == "" {
			t.Errorf("GET %s: code %q hint %q, want not_found with a hint", p, code, hint)
		}
		for _, leak := range []string{"passwd", "Canary", "rollout-guide-body", "alert(1)", "echo hi"} {
			if strings.Contains(string(body), leak) {
				t.Errorf("GET %s: the answer echoes the request or leaks content (%q): %s", p, leak, body)
			}
		}
	}
}

// Criterion: the endpoint needs a token, as every /api/ route does.
func TestSkillFileNeedsAToken(t *testing.T) {
	srv := skillSearchServer(t)
	p := skillFilePath("deployer", "SKILL.md")
	for label, mutate := range map[string]func(*http.Request){
		"no token":    nil,
		"wrong token": bearer("wrong-token-0123456789abcdef0123456789abcdef"),
	} {
		resp, body := do(t, http.MethodGet, srv.URL+p, mutate)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s with %s: status %d, want 401: %s", p, label, resp.StatusCode, body)
		}
		if strings.Contains(string(body), "Deployer") {
			t.Errorf("GET %s with %s: the file leaks: %s", p, label, body)
		}
	}
}

// Criterion: without kind=, search returns articles only, each hit carrying
// kind, so a client that follows a hit with an article lookup keeps working.
func TestSearchWithoutKindReturnsArticlesOnly(t *testing.T) {
	srv := skillSearchServer(t)
	hits := searchHits(t, srv, readerToken, "q=rollout")
	if got := hitIDs(hits); !slices.Equal(got, []string{"rollout-guide"}) {
		t.Fatalf("hits %v, want only the article [rollout-guide]", got)
	}
	if hits[0].Kind != "article" {
		t.Errorf("kind %q, want article", hits[0].Kind)
	}
	// A name match takes no skill or agent in either.
	if got := hitIDs(searchHits(t, srv, readerToken, "q=canary")); len(got) != 0 {
		t.Errorf("q=canary without kind returned %v, want no hits (only skills and agents match)", got)
	}
}

// Criterion: kind= restricts the hits to that Kind and every hit says which.
func TestSearchKindFilterRestrictsHitsAndEveryHitCarriesKind(t *testing.T) {
	srv := skillSearchServer(t)
	for kind, want := range map[string][]string{
		"article": {"rollout-guide"},
		"skill":   {"deployer"},
		"agent":   {"releaser"},
	} {
		hits := searchHits(t, srv, readerToken, "q=rollout&kind="+kind)
		if got := hitIDs(hits); !slices.Equal(got, want) {
			t.Errorf("kind=%s: hits %v, want %v", kind, got, want)
		}
		for _, h := range hits {
			if h.Kind != kind {
				t.Errorf("kind=%s: hit %s has kind %q", kind, h.ID, h.Kind)
			}
		}
	}
}

// Criterion: skills and agents match on name and keywords, with the
// explanation matched says so.
func TestSearchMatchesSkillAndAgentNamesAndKeywords(t *testing.T) {
	srv := skillSearchServer(t)
	hits := searchHits(t, srv, readerToken, "q=rollout&kind=skill")
	if len(hits) != 1 || !slices.Contains(hits[0].Matched, "rollout") || hits[0].Scope != "global" {
		t.Errorf("a keyword match: %+v, want deployer matched on rollout, global", hits)
	}
	for kind, want := range map[string]string{"skill": "canary", "agent": "canary-agent"} {
		hits := searchHits(t, srv, readerToken, "q=canary&kind="+kind)
		if got := hitIDs(hits); !slices.Equal(got, []string{want}) {
			t.Errorf("a name match, kind=%s: %v, want [%s]", kind, got, want)
			continue
		}
		if !slices.Contains(hits[0].Matched, "name") {
			t.Errorf("a name match, kind=%s: matched %v, want name", kind, hits[0].Matched)
		}
	}
}

// Criterion: the description is never searched (ADR 0005): a word only a
// description has finds nothing, for any Kind.
func TestSearchDoesNotMatchSkillOrAgentDescriptions(t *testing.T) {
	srv := skillSearchServer(t)
	for _, kind := range []string{"skill", "agent", "article"} {
		if got := hitIDs(searchHits(t, srv, readerToken, "q=zebrafruit&kind="+kind)); len(got) != 0 {
			t.Errorf("q=zebrafruit&kind=%s returned %v, want no hits: the word is only in descriptions", kind, got)
		}
	}
	if got := hitIDs(searchHits(t, srv, readerToken, "q=zebrafruit")); len(got) != 0 {
		t.Errorf("q=zebrafruit returned %v, want no hits", got)
	}
}

// Criterion: a Project's skill answers in search with its own Scope, and a
// draft agent is hidden unless an operator asks for drafts.
func TestSearchKindResolvesForTheProjectAndHidesDraftAgents(t *testing.T) {
	srv := skillSearchServer(t)
	hits := searchHits(t, srv, readerToken, "q=rollout&kind=skill&project="+url.QueryEscape("github.com/acme/shop"))
	if len(hits) != 1 || hits[0].ID != "deployer" || hits[0].Scope != "github.com/acme/shop" {
		t.Errorf("project search: %+v, want one deployer hit in the Project's Scope", hits)
	}
	if got := hitIDs(searchHits(t, srv, operatorToken, "q=rollout&kind=agent")); !slices.Equal(got, []string{"releaser"}) {
		t.Errorf("agents %v, want [releaser]; the draft wip-rollout is hidden", got)
	}
	if got := hitIDs(searchHits(t, srv, operatorToken, "q=rollout&kind=agent&drafts=true")); !slices.Equal(got, []string{"releaser", "wip-rollout"}) {
		t.Errorf("agents with drafts %v, want [releaser wip-rollout]", got)
	}
	status, body := itemsGet(t, srv, readerToken, "/api/v1/search?q=rollout&kind=agent&drafts=true")
	if status != http.StatusForbidden || strings.Contains(string(body), "wip-rollout") {
		t.Errorf("a reader asking for drafts: status %d, want 403 without the draft: %s", status, body)
	}
}

// Criterion: a kind that is not article, skill or agent is a 400 envelope, not
// an empty answer and not an echo.
func TestSearchWithAnUnknownKindIsABadRequest(t *testing.T) {
	srv := skillSearchServer(t)
	for _, kind := range []string{"nope", "SKILL", "skill,agent", "%3Cscript%3E", "skill%00"} {
		status, body := itemsGet(t, srv, readerToken, "/api/v1/search?q=rollout&kind="+kind)
		if status != http.StatusBadRequest {
			t.Errorf("kind=%s: status %d, want 400: %s", kind, status, body)
			continue
		}
		if code, _ := errorHint(t, body); code != "bad_request" {
			t.Errorf("kind=%s: code %q, want bad_request", kind, code)
		}
		if strings.Contains(string(body), "script") || strings.Contains(string(body), "nope") {
			t.Errorf("kind=%s: the answer echoes the input: %s", kind, body)
		}
	}
}
