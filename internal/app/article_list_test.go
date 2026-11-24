package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #722: GET /api/v1/articles?project=&drafts= returns
// ArticleList{articles: ArticleSummary[], hint?}. An ArticleSummary has id,
// name, keywords, scope, status? (only when the article is not published),
// overrides (never null) and enforced, and no content. The list is the view's
// winners, one per id, sorted by id. The seam is HTTP through the public
// handler, over testdata/items-content unless a test writes its own fixture:
//   - global: global-only, old-deploy (deprecated), platform/deploy,
//     platform/notes, platform/policy (enforced), style, wip (draft);
//   - shop: platform/deploy (shadows global), runbook, shop-rule (enforced),
//     style (draft, shadows global only with drafts), shop-wip (draft), and a
//     suppression of article:platform/notes.

const articlesAPI = "/api/v1/articles"

type articleSummary struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Keywords []string  `json:"keywords"`
	Scope    string    `json:"scope"`
	Status   *string   `json:"status"`
	Override *[]string `json:"overrides"`
	Enforced *bool     `json:"enforced"`
}

func artListServer(t *testing.T, contentDir string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: contentDir, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)
	return srv
}

// artListGet reads a 200 article list and returns its entries and raw body.
func artListGet(t *testing.T, srv *httptest.Server, token, query string) ([]articleSummary, []byte) {
	t.Helper()
	path := articlesAPI
	if query != "" {
		path += "?" + query
	}
	status, body := itemsGet(t, srv, token, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, status, body)
	}
	var res struct {
		Articles []articleSummary `json:"articles"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, body)
	}
	return res.Articles, body
}

func artIDs(list []articleSummary) []string {
	ids := make([]string, 0, len(list))
	for _, a := range list {
		ids = append(ids, a.ID)
	}
	return ids
}

func artByID(t *testing.T, list []articleSummary, id string) articleSummary {
	t.Helper()
	for _, a := range list {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("no article %q in %v", id, artIDs(list))
	return articleSummary{}
}

func wantArtOverrides(t *testing.T, label string, a articleSummary, want []string, enforced bool) {
	t.Helper()
	if a.Override == nil {
		t.Errorf("%s: overrides is absent or null, want %q", label, want)
	} else if !slices.Equal(*a.Override, want) {
		t.Errorf("%s: overrides = %q, want %q", label, *a.Override, want)
	}
	if a.Enforced == nil || *a.Enforced != enforced {
		t.Errorf("%s: enforced = %v, want %v", label, a.Enforced, enforced)
	}
}

// Criterion 8: the spec declares the operation with the Drafts parameter by
// reference, a Project parameter, and the ArticleList and ArticleSummary
// shapes without content.
func TestServedSpecDeclaresTheArticleListOperation(t *testing.T) {
	if !slices.Contains(specOperations(t), "GET "+articlesAPI) {
		t.Fatalf("the spec declares no GET %s", articlesAPI)
	}
	doc := specDoc(t)
	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths[articlesAPI].(map[string]any)
	get, _ := item["get"].(map[string]any)
	params, _ := get["parameters"].([]any)
	var drafts, project int
	for _, raw := range params {
		m, _ := raw.(map[string]any)
		if m["$ref"] == "#/components/parameters/Drafts" {
			drafts++
		}
		if m["name"] == "drafts" {
			t.Errorf("GET %s declares drafts inline; it must reference components/parameters/Drafts", articlesAPI)
		}
		if m["name"] == "project" {
			project++
		}
	}
	if drafts != 1 || project != 1 {
		t.Errorf("GET %s references Drafts %d times and declares project %d times, want once each", articlesAPI, drafts, project)
	}

	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	summary, _ := schemas["ArticleSummary"].(map[string]any)
	props, _ := summary["properties"].(map[string]any)
	for _, name := range []string{"id", "name", "keywords", "scope", "status", "overrides", "enforced"} {
		if _, ok := props[name]; !ok {
			t.Errorf("ArticleSummary declares no %s property", name)
		}
	}
	if _, ok := props["content"]; ok {
		t.Error("ArticleSummary declares content; the list carries none")
	}
	req, _ := summary["required"].([]any)
	if slices.Contains(req, any("status")) {
		t.Error("ArticleSummary requires status; it is present only when the article is not published")
	}
	for _, name := range []string{"id", "name", "keywords", "scope", "overrides", "enforced"} {
		if !slices.Contains(req, any(name)) {
			t.Errorf("ArticleSummary required = %v, want it to include %s", req, name)
		}
	}
	list, _ := schemas["ArticleList"].(map[string]any)
	lprops, _ := list["properties"].(map[string]any)
	if lprops["articles"] == nil || lprops["hint"] == nil {
		t.Errorf("ArticleList properties = %v, want articles and hint", lprops)
	}
	lreq, _ := list["required"].([]any)
	if slices.Contains(lreq, any("hint")) || !slices.Contains(lreq, any("articles")) {
		t.Errorf("ArticleList required = %v, want articles and not hint", lreq)
	}
}

// Criterion 1: winners of the view, one per id, sorted by id; no content.
func TestArticleListReturnsTheWinnersOfTheViewSortedByID(t *testing.T) {
	srv := artListServer(t, itemsContent)
	list, body := artListGet(t, srv, readerToken, "")
	want := []string{"global-only", "old-deploy", "platform/deploy", "platform/notes", "platform/policy", "style"}
	if got := artIDs(list); !slices.Equal(got, want) {
		t.Errorf("global view lists %v, want %v", got, want)
	}
	deploy := artByID(t, list, "platform/deploy")
	if deploy.Name != "Global deploy" || !slices.Equal(deploy.Keywords, []string{"deploy", "kubernetes"}) || deploy.Scope != "global" {
		t.Errorf("platform/deploy = %+v, want name Global deploy, keywords [deploy kubernetes], scope global", deploy)
	}
	if strings.Contains(string(body), "global-deploy-body") || strings.Contains(string(body), `"content"`) {
		t.Errorf("the list carries content: %s", body)
	}
}

// Criterion 1: overrides is never null; status is present only when the
// article is not published.
func TestArticleListHasEmptyOverridesNotNullAndStatusOnlyWhenNotPublished(t *testing.T) {
	srv := artListServer(t, itemsContent)
	_, body := artListGet(t, srv, readerToken, "")
	if strings.Contains(string(body), `"overrides":null`) || !strings.Contains(string(body), `"overrides":[]`) {
		t.Errorf("body does not carry overrides as the empty list, never null: %s", body)
	}
	var res struct {
		Articles []map[string]any `json:"articles"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Articles) == 0 {
		t.Fatalf("empty list: %s", body)
	}
	for _, a := range res.Articles {
		status, has := a["status"]
		if a["id"] == "old-deploy" {
			if status != "deprecated" {
				t.Errorf("old-deploy status = %v, want deprecated", status)
			}
		} else if has {
			t.Errorf("published article %v carries status %v, want it absent", a["id"], status)
		}
		if o, ok := a["overrides"].([]any); !ok || o == nil {
			t.Errorf("article %v overrides = %v, want a list", a["id"], a["overrides"])
		}
	}
}

// Criterion 2: the Project's copy shadows the global one and shows overrides;
// an enforced item in the Project is flagged; a suppressed one is absent.
func TestArticleListInAProjectViewShowsTheNearestCopyAndItsOverrides(t *testing.T) {
	srv := artListServer(t, itemsContent)
	list, _ := artListGet(t, srv, readerToken, "project="+url.QueryEscape(itemsProject))
	want := []string{"global-only", "old-deploy", "platform/deploy", "platform/policy", "runbook", "shop-rule", "style"}
	if got := artIDs(list); !slices.Equal(got, want) {
		t.Fatalf("shop view lists %v, want %v", got, want)
	}
	deploy := artByID(t, list, "platform/deploy")
	if deploy.Name != "Shop deploy" || deploy.Scope != itemsProject {
		t.Errorf("shop platform/deploy = %+v, want the shop copy with scope %s", deploy, itemsProject)
	}
	wantArtOverrides(t, "shop platform/deploy", deploy, []string{"global"}, false)
	wantArtOverrides(t, "shop-rule", artByID(t, list, "shop-rule"), []string{}, true)
	wantArtOverrides(t, "global-only", artByID(t, list, "global-only"), []string{}, false)
	// The shop's draft style does not hide the published global one.
	style := artByID(t, list, "style")
	if style.Scope != "global" || style.Name != "Style guide" {
		t.Errorf("shop view style = %+v, want the published global article", style)
	}
	wantArtOverrides(t, "style", style, []string{}, false)
}

// Criterion 4: a suppressed Item is absent from the Project's view, and only
// there.
func TestArticleListOmitsAnArticleTheProjectSuppresses(t *testing.T) {
	srv := artListServer(t, itemsContent)
	shop, _ := artListGet(t, srv, readerToken, "project="+url.QueryEscape(itemsProject))
	if slices.Contains(artIDs(shop), "platform/notes") {
		t.Errorf("the shop view lists the suppressed platform/notes: %v", artIDs(shop))
	}
	global, _ := artListGet(t, srv, readerToken, "")
	if !slices.Contains(artIDs(global), "platform/notes") {
		t.Errorf("the global view lacks platform/notes: %v", artIDs(global))
	}
	plain, _ := artListGet(t, srv, readerToken, "project=github.com/acme/plain")
	if !slices.Contains(artIDs(plain), "platform/notes") || !slices.Contains(artIDs(plain), "plain-only") {
		t.Errorf("the plain view = %v, want platform/notes and plain-only", artIDs(plain))
	}
}

// Criterion 2: the Project id matches ignoring case.
func TestArticleListMatchesTheProjectIgnoringCase(t *testing.T) {
	srv := artListServer(t, itemsContent)
	list, body := artListGet(t, srv, readerToken, "project="+url.QueryEscape("GitHub.com/ACME/Shop"))
	if a := artByID(t, list, "platform/deploy"); a.Name != "Shop deploy" {
		t.Errorf("platform/deploy = %+v, want the shop copy", a)
	}
	if strings.Contains(string(body), `"hint"`) {
		t.Errorf("a registered Project, spelled in another case, gets a hint: %s", body)
	}
}

// Criterion 2: an unregistered Project gets the global list and a hint, and
// no other Project's articles; a registered one and no Project get no hint.
func TestArticleListForAnUnregisteredProjectIsTheGlobalListWithAHint(t *testing.T) {
	srv := artListServer(t, itemsContent)
	global, globalBody := artListGet(t, srv, readerToken, "")
	if strings.Contains(string(globalBody), `"hint"`) {
		t.Errorf("a list without a Project carries a hint: %s", globalBody)
	}
	list, body := artListGet(t, srv, readerToken, "project=github.com/acme/unknown")
	if !slices.Equal(artIDs(list), artIDs(global)) {
		t.Errorf("unregistered view lists %v, want the global %v", artIDs(list), artIDs(global))
	}
	var res struct {
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal(body, &res); err != nil || strings.TrimSpace(res.Hint) == "" {
		t.Errorf("unregistered Project: hint = %q (%v), want a hint: %s", res.Hint, err, body)
	}
	if strings.Contains(string(body), "other-only") {
		t.Errorf("the unregistered view leaks another Project's article: %s", body)
	}
	_, shopBody := artListGet(t, srv, readerToken, "project="+url.QueryEscape(itemsProject))
	if strings.Contains(string(shopBody), `"hint"`) {
		t.Errorf("a registered Project gets a hint: %s", shopBody)
	}
}

// Criterion 2: a project that is not a ProjectId is a 400 envelope.
func TestArticleListRefusesAnInvalidProject(t *testing.T) {
	srv := artListServer(t, itemsContent)
	for _, project := range []string{"../x", "..%2Fx", "github.com%2Facme%2F..%2F..%2Fetc%2Fpasswd", "not-a-project"} {
		path := articlesAPI + "?project=" + project
		status, body := itemsGet(t, srv, readerToken, path)
		if status != http.StatusBadRequest {
			t.Errorf("GET %s: status %d, want 400: %s", path, status, body)
			continue
		}
		assertEnvelope(t, body, "")
		if strings.Contains(string(body), "passwd") {
			t.Errorf("the 400 echoes the project: %s", body)
		}
	}
}

// Criterion 3: a draft is hidden by default, a reader's drafts=true is a 403
// forbidden that leaks nothing, an operator's lists the draft with its status.
func TestArticleListShowsDraftsOnlyToAnOperatorAskingForThem(t *testing.T) {
	srv := artListServer(t, itemsContent)
	for _, q := range []string{"", "drafts=false"} {
		for _, token := range []string{readerToken, operatorToken} {
			list, _ := artListGet(t, srv, token, q)
			if slices.Contains(artIDs(list), "wip") {
				t.Errorf("query %q with token %.8s lists the draft: %v", q, token, artIDs(list))
			}
		}
	}

	status, body := itemsGet(t, srv, readerToken, articlesAPI+"?drafts=true")
	if status != http.StatusForbidden {
		t.Errorf("reader drafts=true: status %d, want 403: %s", status, body)
	} else {
		assertEnvelope(t, body, "forbidden")
	}
	if strings.Contains(string(body), "wip") || strings.Contains(string(body), "Draft marker") {
		t.Errorf("the 403 leaks the draft: %s", body)
	}

	list, _ := artListGet(t, srv, operatorToken, "drafts=true")
	wip := artByID(t, list, "wip")
	if wip.Status == nil || *wip.Status != "draft" || wip.Scope != "global" {
		t.Errorf("draft wip = %+v, want status draft, scope global", wip)
	}
	if !slices.Contains(artIDs(list), "global-only") {
		t.Errorf("drafts=true dropped the published articles: %v", artIDs(list))
	}

	shop, _ := artListGet(t, srv, operatorToken, "drafts=true&project="+url.QueryEscape(itemsProject))
	style := artByID(t, shop, "style")
	if style.Scope != itemsProject || style.Status == nil || *style.Status != "draft" {
		t.Errorf("shop style with drafts = %+v, want the shop draft", style)
	}
	wantArtOverrides(t, "shop draft style", style, []string{"global"}, false)
	if wipShop := artByID(t, shop, "shop-wip"); wipShop.Status == nil || *wipShop.Status != "draft" {
		t.Errorf("shop-wip = %+v, want status draft", wipShop)
	}
}

// Criterion 4 (security): an enforced global Item still wins over a Project
// copy and over a Project draft with drafts=true. The copies are added after a
// good load, so the test holds whether the load refuses them (the last good
// Snapshot keeps serving) or the view keeps them from shadowing.
func TestArticleListKeepsAnEnforcedGlobalOverAProjectCopyOrDraft(t *testing.T) {
	dir := t.TempDir()
	const enforcedFM = "---\nname: %s\nkeywords: [lock]\nenforced: true\n---\nbody\n"
	writeRaw(t, dir, "global/articles/locked.md", fmt.Sprintf(enforcedFM, "Locked global"))
	writeRaw(t, dir, "global/articles/locked-draft.md", fmt.Sprintf(enforcedFM, "Locked global two"))
	writeRaw(t, dir, "global/articles/wip.md", "---\nname: Global wip\nkeywords: [wip]\nstatus: draft\n---\nbody\n")
	writeRaw(t, dir, "projects/"+itemsProject+"/articles/other.md", "---\nname: Other\nkeywords: [other]\n---\nbody\n")
	syncer := engine.NewSyncer(engine.NewDirSource(dir))
	if err := syncer.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	srv := providerServer(t, syncer.Current) // testToken is an operator token

	writeRaw(t, dir, "projects/"+itemsProject+"/articles/locked.md", "---\nname: Locked shop\nkeywords: [lock]\n---\nbody\n")
	writeRaw(t, dir, "projects/"+itemsProject+"/articles/locked-draft.md", "---\nname: Locked shop draft\nkeywords: [lock]\nstatus: draft\n---\nbody\n")
	_ = syncer.Poll(t.Context()) // refusing the copies is one way to keep the global winning

	for _, project := range []string{itemsProject, "github.com/ACME/Shop"} {
		for _, drafts := range []string{"", "&drafts=true"} {
			q := "project=" + url.QueryEscape(project) + drafts
			resp, body := apiGet(t, srv.URL+articlesAPI+"?"+q)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s?%s: status %d, want 200: %s", articlesAPI, q, resp.StatusCode, body)
			}
			var res struct {
				Articles []articleSummary `json:"articles"`
			}
			if err := json.Unmarshal(body, &res); err != nil {
				t.Fatal(err)
			}
			if drafts != "" {
				artByID(t, res.Articles, "wip") // drafts=true took effect
			}
			for _, id := range []string{"locked", "locked-draft"} {
				a := artByID(t, res.Articles, id)
				if a.Scope != "global" || !strings.HasPrefix(a.Name, "Locked global") {
					t.Errorf("?%s: %s = %+v, want the enforced global article", q, id, a)
				}
				wantArtOverrides(t, q+" "+id, a, []string{}, true)
			}
		}
	}
}

// Criterion 5: the list holds no content, so 2000 articles stay small.
func TestArticleListOf2000ArticlesCarriesNoContent(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("content-marker-line\n", 400)
	for i := range 2000 {
		writeRaw(t, dir, fmt.Sprintf("global/articles/bulk-%04d.md", i),
			fmt.Sprintf("---\nname: Bulk %d\nkeywords: [bulk]\n---\n%s", i, body))
	}
	srv := artListServer(t, dir)
	list, raw := artListGet(t, srv, readerToken, "")
	if len(list) != 2000 {
		t.Fatalf("the list holds %d articles, want 2000", len(list))
	}
	if strings.Contains(string(raw), "content-marker-line") {
		t.Error("the list carries article content")
	}
	if len(raw) > 1<<20 {
		t.Errorf("a list of 2000 articles is %d bytes, want under 1 MiB", len(raw))
	}
}

// Criterion 6: a token is required.
func TestArticleListNeedsAToken(t *testing.T) {
	srv := artListServer(t, itemsContent)
	for label, mutate := range map[string]func(*http.Request){
		"no token":    nil,
		"wrong token": bearer("wrong-token-0123456789abcdef0123456789abcdef"),
	} {
		resp, body := do(t, http.MethodGet, srv.URL+articlesAPI, mutate)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s with %s: status %d, want 401: %s", articlesAPI, label, resp.StatusCode, body)
			continue
		}
		assertEnvelope(t, body, "")
	}
}

// Criterion 6: before the first sync the list is a 503 not_ready; a reader's
// drafts=true is still the 403 (ADR 0014).
func TestArticleListBeforeTheFirstLoadIsNotReady(t *testing.T) {
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

	for _, path := range []string{articlesAPI, articlesAPI + "?project=" + url.QueryEscape(itemsProject)} {
		resp, body := do(t, http.MethodGet, srv.URL+path, bearer(readerToken))
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("GET %s before the first load: status %d, want 503: %s", path, resp.StatusCode, body)
			continue
		}
		assertEnvelope(t, body, "not_ready")
	}
	resp, body := do(t, http.MethodGet, srv.URL+articlesAPI+"?drafts=true", bearer(readerToken))
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("reader drafts=true before the first load: status %d, want 403: %s", resp.StatusCode, body)
	}
}

// Criterion 7: a list call is not recorded in the activity feed, as the skill,
// agent and loop lists are not, so Home loading it does not flood the feed.
func TestArticleListIsNotRecordedInTheActivityFeed(t *testing.T) {
	pub := activityHandler(t, platform.Config{ContentDir: itemsContent})
	before := len(waitEvents(t, pub.Activity(), 0))
	for _, p := range []string{articlesAPI, articlesAPI + "?project=" + url.QueryEscape(itemsProject)} {
		if rec := serveAs(t, pub, p, bearer(readerToken)); rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200: %s", p, rec.Code, rec.Body.String())
		}
	}
	if after := len(waitEvents(t, pub.Activity(), 0)); after != before {
		t.Errorf("article list calls were recorded: %d events, want %d", after, before)
	}
	// A lookup is still recorded, so the check above can tell.
	e := lastEvent(t, pub, articlesAPI+"/global-only", http.StatusOK, nil)
	if e.Kind != api.KindLookup {
		t.Errorf("lookup recorded as %q, want lookup", e.Kind)
	}
}

// Criterion 8: the span and metric path template covers the route.
func TestSpanPathCoversTheArticleListRoute(t *testing.T) {
	for _, in := range []string{articlesAPI, strings.ToUpper(articlesAPI)} {
		if got := SpanPath(in); got != articlesAPI {
			t.Errorf("SpanPath(%q) = %q, want %q", in, got, articlesAPI)
		}
	}
}
