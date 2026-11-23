package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #273 (slice E of #9), the HTTP contract seam: Article
// and SearchHit carry the optional lifecycle fields status and replaced_by,
// and search and lookup take drafts=true from operator tokens only. The seam
// is the public handler PublicHandler builds; every response is checked
// against the served spec by do.
//
// The contract they fix, beyond the spec:
//   - status is "deprecated" or "draft"; a published Item carries neither
//     status nor replaced_by, so every existing answer stays byte for byte
//     what it was (ADR 0011; the CLI golden files depend on it);
//   - replaced_by is the reference as written, <kind>:<name> (ADR 0021);
//   - a draft is invisible without drafts=true, to operators too, and a draft
//     looked up without it is the same 404 as an id that does not exist;
//   - drafts=true from a reader is a 403 forbidden envelope whose hint names
//     drafts and the operator role, before and after the first load, and it
//     echoes nothing of the request;
//   - with drafts=true an enforced global Item still wins over a draft Project
//     copy (#359).

// lifecycleContent is a dir Source with a deprecated Item and its replacement,
// a deprecated Item without one, a global draft, and a Project draft that
// shadows a global Item that is not enforced.
const lifecycleContent = "testdata/lifecycle-content"

// lifecycleProject is the Project registered in lifecycleContent.
const lifecycleProject = "github.com/acme/shop"

// draftMarkers are the parts of the global draft wip that must never reach a
// caller who did not ask for drafts as an operator.
var draftMarkers = []string{`"wip"`, "Draft marker", "draft-marker-body"}

// lifecycleServer serves lifecycleContent with the named reader, operator and
// hashed reader tokens of namedTokenLines. The rate limit is not under test
// here, and the spelling matrix sends more requests per token than the
// default burst, so the server gets roomyRateLimit; a 429 is still a failure
// wherever a test checks the status.
func lifecycleServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: lifecycleContent, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)
	return srv
}

// lifecycleHit is the part of a SearchHit these tests read. The pointers tell
// an absent field from an empty one.
type lifecycleHit struct {
	ID         string  `json:"id"`
	Scope      string  `json:"scope"`
	Status     *string `json:"status"`
	ReplacedBy *string `json:"replaced_by"`
}

type lifecycleArticle struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	Status     *string `json:"status"`
	ReplacedBy *string `json:"replaced_by"`
}

func lifecycleSearch(t *testing.T, srv *httptest.Server, token, query string) map[string]lifecycleHit {
	t.Helper()
	resp, body := do(t, http.MethodGet, srv.URL+"/api/v1/search?"+query, bearer(token))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search ?%s: status %d, want 200: %s", query, resp.StatusCode, body)
	}
	var res struct {
		Results []lifecycleHit `json:"results"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	hits := map[string]lifecycleHit{}
	for _, h := range res.Results {
		if _, dup := hits[h.ID]; dup {
			t.Errorf("search ?%s: id %s appears twice: %s", query, h.ID, body)
		}
		hits[h.ID] = h
	}
	return hits
}

func lifecycleLookup(t *testing.T, srv *httptest.Server, token, id, query string) (int, lifecycleArticle, []byte) {
	t.Helper()
	u := srv.URL + "/api/v1/articles/" + url.PathEscape(id)
	if query != "" {
		u += "?" + query
	}
	resp, body := do(t, http.MethodGet, u, bearer(token))
	var a lifecycleArticle
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &a); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, a, body
}

func str(p *string) string {
	if p == nil {
		return "<absent>"
	}
	return strconv.Quote(*p)
}

// Criterion: the served spec declares the optional lifecycle fields on
// Article and SearchHit, the optional boolean drafts on search and lookup, and
// the 403 those operations answer a reader asking for drafts.
func TestServedSpecDeclaresLifecycleFieldsAndDrafts(t *testing.T) {
	doc := specDoc(t)
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	for _, name := range []string{"Article", "SearchHit"} {
		schema, _ := schemas[name].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		for _, field := range []string{"status", "replaced_by"} {
			p := contracttest.ResolveRef(doc, props[field])
			if p == nil {
				t.Errorf("%s declares no %s property", name, field)
				continue
			}
			if p["type"] != "string" {
				t.Errorf("%s.%s type %v, want string", name, field, p["type"])
			}
			if d, _ := p["description"].(string); strings.TrimSpace(d) == "" {
				t.Errorf("%s.%s has no description", name, field)
			}
		}
		req, _ := schema["required"].([]any)
		for _, r := range req {
			if r == "status" || r == "replaced_by" {
				t.Errorf("%s requires %v; the lifecycle fields are optional (ADR 0011)", name, r)
			}
		}
		if _, camel := props["replacedBy"]; camel {
			t.Errorf("%s declares replacedBy; new properties are snake_case", name)
		}
	}

	paths, _ := doc["paths"].(map[string]any)
	for _, path := range []string{"/api/v1/search", "/api/v1/articles/{id}"} {
		item, _ := paths[path].(map[string]any)
		op, _ := item["get"].(map[string]any)
		var drafts map[string]any
		params, _ := op["parameters"].([]any)
		for _, raw := range params {
			p := contracttest.ResolveRef(doc, raw)
			if p["name"] == "drafts" {
				drafts = p
			}
		}
		if drafts == nil {
			t.Errorf("GET %s declares no drafts parameter", path)
		} else {
			if drafts["in"] != "query" {
				t.Errorf("GET %s drafts is in %v, want query", path, drafts["in"])
			}
			if req, _ := drafts["required"].(bool); req {
				t.Errorf("GET %s drafts is required; it must be optional", path)
			}
			schema := contracttest.ResolveRef(doc, drafts["schema"])
			if schema["type"] != "boolean" {
				t.Errorf("GET %s drafts schema type %v, want boolean", path, schema["type"])
			}
			if d, _ := drafts["description"].(string); !strings.Contains(strings.ToLower(d), "operator") {
				t.Errorf("GET %s drafts description %q does not say it is for operators", path, d)
			}
		}
		responses, _ := op["responses"].(map[string]any)
		r := contracttest.ResolveRef(doc, responses["403"])
		if r == nil {
			t.Errorf("GET %s declares no 403 response", path)
			continue
		}
		c, _ := r["content"].(map[string]any)
		media, _ := c["application/json"].(map[string]any)
		schema, _ := media["schema"].(map[string]any)
		if schema["$ref"] != "#/components/schemas/ErrorEnvelope" {
			t.Errorf("GET %s 403 body schema %v, want the ErrorEnvelope", path, schema)
		}
	}
}

// Criterion: search and lookup serve status and replaced_by of a deprecated
// Item, to readers and operators alike, without asking for drafts.
func TestSearchAndLookupServeStatusAndReplacedBy(t *testing.T) {
	srv := lifecycleServer(t)
	for who, token := range map[string]string{"reader": readerToken, "hashed reader": hashedSecret, "operator": operatorToken} {
		hits := lifecycleSearch(t, srv, token, "q=lifecycle")
		old, ok := hits["deploy-v1"]
		if !ok {
			t.Fatalf("%s: search lacks the deprecated deploy-v1: %v", who, hits)
		}
		if str(old.Status) != `"deprecated"` || str(old.ReplacedBy) != `"article:deploy-v2"` {
			t.Errorf("%s: deploy-v1 hit status %s replaced_by %s, want \"deprecated\" and \"article:deploy-v2\"", who, str(old.Status), str(old.ReplacedBy))
		}
		if l := hits["legacy"]; str(l.Status) != `"deprecated"` || l.ReplacedBy != nil {
			t.Errorf("%s: legacy hit status %s replaced_by %s, want \"deprecated\" and no replaced_by", who, str(l.Status), str(l.ReplacedBy))
		}

		code, a, body := lifecycleLookup(t, srv, token, "deploy-v1", "")
		if code != http.StatusOK {
			t.Fatalf("%s: lookup deploy-v1: status %d: %s", who, code, body)
		}
		if str(a.Status) != `"deprecated"` || str(a.ReplacedBy) != `"article:deploy-v2"` {
			t.Errorf("%s: deploy-v1 article status %s replaced_by %s, want \"deprecated\" and \"article:deploy-v2\"", who, str(a.Status), str(a.ReplacedBy))
		}
		code, a, body = lifecycleLookup(t, srv, token, "legacy", "")
		if code != http.StatusOK || str(a.Status) != `"deprecated"` || a.ReplacedBy != nil {
			t.Errorf("%s: lookup legacy: status %d, status %s replaced_by %s: %s", who, code, str(a.Status), str(a.ReplacedBy), body)
		}
	}
}

// Negative case: a published Item carries neither lifecycle field, in search
// and lookup, so a client that never heard of them sees the answer it got
// before.
func TestPublishedItemsCarryNoLifecycleFields(t *testing.T) {
	srv := lifecycleServer(t)
	for _, token := range []string{readerToken, operatorToken} {
		hits := lifecycleSearch(t, srv, token, "q=lifecycle")
		for _, id := range []string{"deploy-v2", "style"} {
			h, ok := hits[id]
			if !ok {
				t.Fatalf("search lacks the published %s: %v", id, hits)
			}
			if h.Status != nil || h.ReplacedBy != nil {
				t.Errorf("published hit %s: status %s replaced_by %s, want both absent", id, str(h.Status), str(h.ReplacedBy))
			}
		}
		_, _, body := lifecycleLookup(t, srv, token, "deploy-v2", "")
		for _, field := range []string{`"status"`, `"replaced_by"`} {
			if strings.Contains(string(body), field) {
				t.Errorf("published article deploy-v2 carries %s: %s", field, body)
			}
		}
	}
	// The test content has no lifecycle frontmatter at all: its answers do
	// not change.
	plain := newServer(t)
	for _, path := range []string{"/api/v1/search?q=kubernetes", "/api/v1/articles/agent-loops"} {
		_, body := apiGet(t, plain.URL+path)
		if strings.Contains(string(body), `"status"`) || strings.Contains(string(body), `"replaced_by"`) {
			t.Errorf("GET %s carries a lifecycle field for content without one: %s", path, body)
		}
	}
}

// Criterion: a draft is invisible without drafts=true, to an operator too. A
// draft looked up is the same 404 as an id that does not exist, so its
// existence does not leak.
func TestDraftIsInvisibleWithoutDraftsEvenToAnOperator(t *testing.T) {
	srv := lifecycleServer(t)
	for who, token := range map[string]string{"reader": readerToken, "operator": operatorToken} {
		for _, query := range []string{"", "drafts=false"} {
			sq := "q=lifecycle+wip+draft"
			if query != "" {
				sq += "&" + query
			}
			resp, body := do(t, http.MethodGet, srv.URL+"/api/v1/search?"+sq, bearer(token))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s search ?%s: status %d: %s", who, sq, resp.StatusCode, body)
			}
			for _, m := range draftMarkers {
				if strings.Contains(string(body), m) {
					t.Errorf("%s search ?%s shows the draft (%s): %s", who, sq, m, body)
				}
			}
			code, _, draftBody := lifecycleLookup(t, srv, token, "wip", query)
			_, _, missingBody := lifecycleLookup(t, srv, token, "no-such-article", query)
			if code != http.StatusNotFound {
				t.Errorf("%s lookup wip ?%s: status %d, want 404: %s", who, query, code, draftBody)
				continue
			}
			if string(draftBody) != string(missingBody) {
				t.Errorf("%s lookup wip ?%s differs from a missing id, which tells the draft exists:\n%s\n%s", who, query, draftBody, missingBody)
			}
		}
		// The Project draft does not shadow the global style either.
		hits := lifecycleSearch(t, srv, token, "q=style&project="+lifecycleProject)
		if h := hits["style"]; h.Scope != engine.ScopeGlobal || h.Status != nil {
			t.Errorf("%s: style in the Project without drafts: scope %q status %s, want the published global", who, h.Scope, str(h.Status))
		}
		_, a, body := lifecycleLookup(t, srv, token, "style", "project="+lifecycleProject)
		if a.Content != "global-style-body\n" {
			t.Errorf("%s: lookup style in the Project without drafts = %s, want the global", who, body)
		}
	}
}

// Criterion: an operator asking for drafts sees them, marked status draft, in
// search and lookup, and a Project draft shadows a global Item that is not
// enforced.
func TestOperatorAskingForDraftsSeesThem(t *testing.T) {
	srv := lifecycleServer(t)
	hits := lifecycleSearch(t, srv, operatorToken, "q=lifecycle&drafts=true")
	w, ok := hits["wip"]
	if !ok {
		t.Fatalf("operator search with drafts=true lacks the draft wip: %v", hits)
	}
	if str(w.Status) != `"draft"` {
		t.Errorf("draft hit status %s, want \"draft\"", str(w.Status))
	}
	if _, ok := hits["deploy-v1"]; !ok {
		t.Errorf("operator search with drafts=true lost the deprecated deploy-v1: %v", hits)
	}
	code, a, body := lifecycleLookup(t, srv, operatorToken, "wip", "drafts=true")
	if code != http.StatusOK || str(a.Status) != `"draft"` || a.Content != "draft-marker-body\n" {
		t.Errorf("operator lookup wip with drafts=true: status %d, status %s: %s", code, str(a.Status), body)
	}
	// Ids still compare ignoring case.
	if code, _, body := lifecycleLookup(t, srv, operatorToken, "WIP", "drafts=true"); code != http.StatusOK {
		t.Errorf("operator lookup WIP with drafts=true: status %d: %s", code, body)
	}

	hits = lifecycleSearch(t, srv, operatorToken, "q=style&drafts=true&project="+lifecycleProject)
	if h := hits["style"]; h.Scope != lifecycleProject || str(h.Status) != `"draft"` {
		t.Errorf("style in the Project with drafts: scope %q status %s, want the Project draft", h.Scope, str(h.Status))
	}
	_, a, body = lifecycleLookup(t, srv, operatorToken, "style", "drafts=true&project="+lifecycleProject)
	if a.Content != "project-style-draft-body\n" || str(a.Status) != `"draft"` {
		t.Errorf("lookup style in the Project with drafts = %s, want the Project draft", body)
	}
}

// Criterion: a reader asking for drafts gets the 403 forbidden envelope with
// a hint, on search and lookup; no token or an unknown one is a 401. Nothing
// of the request, no draft and no secret is echoed.
func TestReaderAskingForDraftsIsForbidden(t *testing.T) {
	srv := lifecycleServer(t)
	const marker = "echo-marker-7f3a"
	paths := []string{
		"/api/v1/search?q=lifecycle&drafts=true",
		"/api/v1/search?q=" + marker + "&drafts=true",
		"/api/v1/search?drafts=true&q=lifecycle&project=" + lifecycleProject,
		"/api/v1/articles/wip?drafts=true",
		"/api/v1/articles/" + marker + "?drafts=true",
		"/api/v1/articles/style?drafts=true&project=" + lifecycleProject,
	}
	for _, path := range paths {
		for who, c := range map[string]struct {
			mutate func(*http.Request)
			status int
			code   string
		}{
			"reader":        {bearer(readerToken), http.StatusForbidden, "forbidden"},
			"hashed reader": {bearer(hashedSecret), http.StatusForbidden, "forbidden"},
			"no token":      {nil, http.StatusUnauthorized, "unauthorized"},
			"unknown token": {bearer("unknown-token-0123456789abcdef0123456789"), http.StatusUnauthorized, "unauthorized"},
		} {
			resp, body := do(t, http.MethodGet, srv.URL+path, c.mutate)
			if resp.StatusCode != c.status {
				t.Errorf("%s GET %s: status %d, want %d: %s", who, path, resp.StatusCode, c.status, body)
				continue
			}
			code, hint := errorHint(t, body)
			if code != c.code {
				t.Errorf("%s GET %s: code %q, want %q", who, path, code, c.code)
			}
			if c.status == http.StatusForbidden {
				lh := strings.ToLower(hint)
				if !strings.Contains(lh, "drafts") || !strings.Contains(lh, "operator") {
					t.Errorf("%s GET %s: hint %q, want it to name drafts and the operator role", who, path, hint)
				}
			}
			for _, leak := range append([]string{marker, lifecycleProject, readerToken, operatorToken, hashedSecret, "project-style-draft-body"}, draftMarkers...) {
				if strings.Contains(string(body), leak) {
					t.Errorf("%s GET %s: the body echoes %q: %s", who, path, leak, body)
				}
			}
		}
	}
}

// Abuse: no spelling of the drafts parameter shows a reader a draft. A value
// that parses as true is refused (403, or 400 when the router rejects the
// spelling); anything else is refused or answered without drafts.
func TestNoSpellingOfDraftsShowsAReaderADraft(t *testing.T) {
	srv := lifecycleServer(t)
	spellings := []string{
		"drafts=true", "drafts=TRUE", "drafts=True", "drafts=1", "drafts=t", "drafts=T",
		"%64rafts=true", "drafts=%74rue",
		"drafts=false&drafts=true", "drafts=true&drafts=false", "drafts=true&drafts=true",
		"drafts=yes", "drafts=on", "drafts=", "drafts", "drafts=+true", "drafts=true%00", "drafts=true%20",
		"Drafts=true", "DRAFTS=true", "drafts[]=true", "draft=true",
	}
	for _, sp := range spellings {
		values, _ := url.ParseQuery(sp)
		trueish := false
		if v := values["drafts"]; len(v) == 1 {
			trueish, _ = strconv.ParseBool(v[0])
		}
		for _, base := range []string{"/api/v1/search?q=lifecycle+wip+draft&", "/api/v1/articles/wip?", "/api/v1/articles/style?project=" + lifecycleProject + "&"} {
			for who, token := range map[string]string{"reader": readerToken, "hashed reader": hashedSecret} {
				path := base + sp
				resp, body := do(t, http.MethodGet, srv.URL+path, bearer(token))
				for _, m := range append([]string{"project-style-draft-body"}, draftMarkers...) {
					if strings.Contains(string(body), m) {
						t.Errorf("%s GET %s: status %d shows a draft (%s): %s", who, path, resp.StatusCode, m, body)
					}
				}
				switch resp.StatusCode {
				case http.StatusForbidden, http.StatusBadRequest:
				case http.StatusOK, http.StatusNotFound:
					if trueish {
						t.Errorf("%s GET %s: status %d, want 403 (or 400): drafts parses as true", who, path, resp.StatusCode)
					}
				default:
					t.Errorf("%s GET %s: status %d, want 403, 400, 200 or 404: %s", who, path, resp.StatusCode, body)
				}
			}
		}
	}
}

// Negative case: a reader may say drafts=false; it is the default view.
func TestReaderMaySayDraftsFalse(t *testing.T) {
	srv := lifecycleServer(t)
	hits := lifecycleSearch(t, srv, readerToken, "q=lifecycle&drafts=false")
	if _, ok := hits["deploy-v2"]; !ok {
		t.Errorf("reader search with drafts=false lacks deploy-v2: %v", hits)
	}
	if _, ok := hits["wip"]; ok {
		t.Errorf("reader search with drafts=false shows the draft: %v", hits)
	}
	if code, _, body := lifecycleLookup(t, srv, readerToken, "deploy-v2", "drafts=false"); code != http.StatusOK {
		t.Errorf("reader lookup with drafts=false: status %d: %s", code, body)
	}
}

// Criterion: a reader's drafts=true can never succeed, so it is the 403
// before the first load too, like a 400 (ADR 0014); an operator's is the 503
// not_ready every read answers before the first load.
func TestDraftsRoleCheckComesBeforeNotReady(t *testing.T) {
	tokens, err := api.ParseTokens(namedTokenLines())
	if err != nil {
		t.Fatal(err)
	}
	m, err := newPublicMuxKeepalive(func() *engine.Snapshot { return nil }, tokens, rateLimiter(platform.DefaultRateLimit()), nil, api.DefaultActivityKeepalive)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	for _, path := range []string{"/api/v1/search?q=a&drafts=true", "/api/v1/articles/wip?drafts=true"} {
		resp, body := do(t, http.MethodGet, srv.URL+path, bearer(readerToken))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("reader GET %s before the first load: status %d, want 403: %s", path, resp.StatusCode, body)
		} else {
			assertEnvelope(t, body, "forbidden")
		}
		resp, body = do(t, http.MethodGet, srv.URL+path, bearer(operatorToken))
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("operator GET %s before the first load: status %d, want 503: %s", path, resp.StatusCode, body)
		} else {
			assertEnvelope(t, body, "not_ready")
		}
	}
}

// Criterion from #359 (security): with drafts=true an enforced global Item
// still wins over a draft Project copy, in search and lookup, for every
// spelling of the Project and of the copy's name. The copy is added after a
// good load, so the test holds whether the load refuses it (the last good
// Snapshot keeps serving) or the drafts view keeps it from shadowing. The
// global draft proves drafts=true took effect.
func TestEnforcedGlobalStillWinsOverADraftProjectCopyWithDrafts(t *testing.T) {
	for _, copyName := range []string{"rules", "RULES", "Rules"} {
		t.Run(copyName, func(t *testing.T) {
			dir := t.TempDir()
			writeRaw(t, dir, "global/articles/rules.md", "---\nname: Rules\nkeywords: [rules]\nenforced: true\n---\nenforced-global-body\n")
			writeRaw(t, dir, "global/articles/rules-wip.md", "---\nname: Rules draft\nkeywords: [rules]\nstatus: draft\n---\nglobal-draft-body\n")
			writeRaw(t, dir, "projects/github.com/acme/shop/articles/other.md", "---\nname: Other\nkeywords: [other]\n---\nother\n")
			syncer := engine.NewSyncer(engine.NewDirSource(dir))
			if err := syncer.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			srv := providerServer(t, syncer.Current) // testToken is an operator token

			writeRaw(t, dir, "projects/github.com/acme/shop/articles/"+copyName+".md",
				"---\nname: Shop rules draft\nkeywords: [rules]\nstatus: draft\n---\ndraft-copy-body\n")
			_ = syncer.Poll(t.Context()) // refusing the copy is one way to keep the global winning

			for _, project := range []string{"github.com/acme/shop", "github.com/ACME/Shop"} {
				{
					q := "drafts=true&project=" + url.QueryEscape(project)
					resp, body := apiGet(t, srv.URL+"/api/v1/search?q=rules&"+q)
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("search ?%s: status %d: %s", q, resp.StatusCode, body)
					}
					var res struct {
						Results []lifecycleHit `json:"results"`
					}
					if err := json.Unmarshal(body, &res); err != nil {
						t.Fatal(err)
					}
					var rules []lifecycleHit
					sawDraft := false
					for _, h := range res.Results {
						switch strings.ToLower(h.ID) {
						case "rules":
							rules = append(rules, h)
						case "rules-wip":
							sawDraft = true
						}
					}
					if !sawDraft {
						t.Errorf("search ?%s lacks the global draft rules-wip, so drafts=true had no effect: %s", q, body)
					}
					if len(rules) != 1 || rules[0].Scope != engine.ScopeGlobal || rules[0].Status != nil {
						t.Errorf("search ?%s: rules hits %+v, want exactly the published enforced global: %s", q, rules, body)
					}

					resp, body = apiGet(t, srv.URL+"/api/v1/articles/rules?"+q)
					if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "enforced-global-body") || strings.Contains(string(body), "draft-copy-body") {
						t.Errorf("lookup rules ?%s: status %d, want the enforced global: %s", q, resp.StatusCode, body)
					}
				}
			}
		})
	}
}
