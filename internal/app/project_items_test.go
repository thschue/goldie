package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #388 (slice 1 of the remaining #10 work): GET
// /api/v1/projects/{id}/items exports a Project's resolved view, so an
// embedded server can build on a central server's content and check local
// shadowing. The id is one percent-encoded path segment (ADR 0016).
//
// The view is every winning Item (published or deprecated, never a draft)
// with id, name, keywords, content, status, replaced_by, scope and enforced,
// plus the Project's Suppressions. Only winners are sent: no other Project's
// Items and no shadowed copies.
//
// The response shape these tests fix, where the issue names fields only:
// {"items": [...], "suppressions": ["article:..."], "hint": "..."}; hint is
// present only for a Project the server does not hold, as in search and
// lookup. status is always present ("published" for a published Item) and
// items is sorted by id ascending (PM decision on #388). A view over
// contract.MaxResponseBytes is refused whole: 422, code view_too_large, a hint
// and no partial body.
//
// The content (testdata/items-content) has global Items platform/deploy,
// platform/policy (enforced), platform/notes, old-deploy (deprecated,
// replaced by platform/deploy), style, global-only and the draft wip; and
// three Projects:
//   - github.com/acme/shop shadows platform/deploy, suppresses platform/notes,
//     has runbook, an enforced shop-rule and two drafts (style, which would
//     shadow the global style, and shop-wip);
//   - github.com/acme/other has other-only;
//   - github.com/acme/plain has plain-only and no project.yaml.

const itemsContent = "testdata/items-content"

const shopItems = "/api/v1/projects/github.com%2Facme%2Fshop/items"

type viewItem struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Keywords   []string `json:"keywords"`
	Content    string   `json:"content"`
	Status     *string  `json:"status"`
	ReplacedBy *string  `json:"replaced_by"`
	Scope      string   `json:"scope"`
	Enforced   *bool    `json:"enforced"`
}

type projectView struct {
	Items        []viewItem `json:"items"`
	Suppressions []string   `json:"suppressions"`
	Hint         *string    `json:"hint"`
}

func itemsServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newServerWith(t, platform.Config{ContentDir: itemsContent})
}

// namedTokensItemsServer serves itemsContent with a reader and an operator token.
func namedTokensItemsServer(t *testing.T) *httptest.Server {
	t.Helper()
	h, err := PublicHandler(platform.Config{ContentDir: itemsContent, APITokens: namedTokenLines(), RateLimit: platform.DefaultRateLimit()}, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func getView(t *testing.T, base, path string) (projectView, []byte) {
	t.Helper()
	resp, body := apiGet(t, base+path)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, resp.StatusCode, body)
	}
	var v projectView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("GET %s: body %q is not JSON: %v", path, body, err)
	}
	return v, body
}

func (v projectView) byID(t *testing.T) map[string]viewItem {
	t.Helper()
	m := map[string]viewItem{}
	for _, it := range v.Items {
		if _, dup := m[it.ID]; dup {
			t.Errorf("id %s appears twice in the view", it.ID)
		}
		m[it.ID] = it
	}
	return m
}

func ids(m map[string]viewItem) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func sameIDs(t *testing.T, what string, got map[string]viewItem, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(ids(got), "|") != strings.Join(want, "|") {
		t.Errorf("%s: ids = %q, want %q", what, ids(got), want)
	}
}

func isEnforced(it viewItem) bool { return it.Enforced != nil && *it.Enforced }

func TestProjectItemsAreTheResolvedViewWithScopeAndEnforced(t *testing.T) {
	base := itemsServer(t).URL
	v, _ := getView(t, base, shopItems)
	got := v.byID(t)
	sameIDs(t, "shop view", got,
		"platform/deploy", "platform/policy", "old-deploy", "style", "global-only", "runbook", "shop-rule")

	// The Project's copy wins over the global one: its content, its scope.
	d := got["platform/deploy"]
	if d.Scope != shopProject || strings.TrimSpace(d.Content) != "shop-deploy-body" || d.Name != "Shop deploy" {
		t.Errorf("platform/deploy = %+v, want the shop copy with scope %s", d, shopProject)
	}
	if strings.Join(d.Keywords, ",") != "deploy,shop" {
		t.Errorf("platform/deploy keywords = %q, want [deploy shop]", d.Keywords)
	}
	// A global Item the Project does not shadow keeps the global scope.
	for _, id := range []string{"platform/policy", "old-deploy", "style", "global-only"} {
		if got[id].Scope != "global" {
			t.Errorf("%s scope = %q, want global", id, got[id].Scope)
		}
	}
	if got["runbook"].Scope != shopProject || got["shop-rule"].Scope != shopProject {
		t.Errorf("Project Items scope = %q, %q, want %s", got["runbook"].Scope, got["shop-rule"].Scope, shopProject)
	}
	// A draft Project copy does not shadow the global style.
	if s := got["style"]; !strings.Contains(s.Content, "global-style-body") || s.Name != "Style guide" {
		t.Errorf("style = %+v, want the global Item (a draft copy never shadows)", s)
	}
	// enforced is reported for global and Project Items, and false otherwise.
	for id, want := range map[string]bool{
		"platform/policy": true, "shop-rule": true,
		"platform/deploy": false, "old-deploy": false, "style": false, "global-only": false, "runbook": false,
	} {
		if isEnforced(got[id]) != want {
			t.Errorf("%s enforced = %v, want %v", id, got[id].Enforced, want)
		}
	}
	for id, it := range got {
		if strings.TrimSpace(it.Content) == "" || it.Name == "" || len(it.Keywords) == 0 {
			t.Errorf("%s misses name, keywords or content: %+v", id, it)
		}
	}
}

func TestProjectItemsKeepTheLifecycleOfPublishedAndDeprecatedItems(t *testing.T) {
	base := itemsServer(t).URL
	got, _ := getView(t, base, shopItems)
	items := got.byID(t)

	old := items["old-deploy"]
	if old.Status == nil || *old.Status != "deprecated" {
		t.Errorf("old-deploy status = %v, want deprecated", old.Status)
	}
	if old.ReplacedBy == nil || *old.ReplacedBy != "article:platform/deploy" {
		t.Errorf("old-deploy replaced_by = %v, want article:platform/deploy (as written)", old.ReplacedBy)
	}
	for _, id := range []string{"platform/deploy", "platform/policy", "style", "runbook"} {
		it := items[id]
		if it.Status == nil || *it.Status != "published" {
			t.Errorf("%s status = %v, want published (always present)", id, it.Status)
		}
		if it.ReplacedBy != nil {
			t.Errorf("%s replaced_by = %q, want none", id, *it.ReplacedBy)
		}
	}
}

func TestProjectItemsAreSortedByIDAscending(t *testing.T) {
	v, _ := getView(t, itemsServer(t).URL, shopItems)
	got := make([]string, 0, len(v.Items))
	for _, it := range v.Items {
		got = append(got, it.ID)
	}
	if !sort.StringsAreSorted(got) || len(got) < 2 {
		t.Errorf("item ids = %q, want sorted ascending", got)
	}
}

// A view over the response cap is refused whole, never truncated: a partial
// view would hide enforced Items from upstream checks.
func TestProjectItemsOverTheResponseCapAreRefusedWholeWith422(t *testing.T) {
	dir := t.TempDir()
	// Each article is under the 1 MiB per-article limit; together they exceed the cap.
	filler := strings.Repeat("x", 1_000_000)
	n := contract.MaxResponseBytes/len(filler) + 2
	for i := 0; i < n; i++ {
		writeArticle(t, dir, fmt.Sprintf("big/item-%02d", i), "Big", filler)
	}
	writeArticle(t, dir, "small", "Small", "small-body")
	srv := newServerWith(t, platform.Config{ContentDir: dir})

	resp, body := apiGet(t, srv.URL+"/api/v1/projects/github.com%2Facme%2Fshop/items")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422: %.200s", resp.StatusCode, body)
	}
	code, hint := errorHint(t, body)
	if code != "view_too_large" || !strings.Contains(hint, "over the export limit") {
		t.Errorf("code %q hint %q, want view_too_large with the export-limit hint", code, hint)
	}
	if len(body) > 4096 || strings.Contains(string(body), "small-body") || strings.Contains(string(body), "xxxxxxxx") {
		t.Errorf("the refusal carries part of the view (%d bytes)", len(body))
	}
}

// The cap applies to the encoded body too: JSON escapes "<" to six bytes, so
// content well under the cap as raw bytes can still be over it once encoded.
func TestProjectItemsOverTheCapOnlyOnceEncodedAreRefusedWholeWith422(t *testing.T) {
	dir := t.TempDir()
	// 3 x 1,000,000 raw bytes is under the cap; encoded (x6) it is about 18 MB.
	filler := strings.Repeat("<", 1_000_000)
	for i := 0; i < 3; i++ {
		writeArticle(t, dir, fmt.Sprintf("big/item-%d", i), "Big", filler)
	}
	srv := newServerWith(t, platform.Config{ContentDir: dir})

	resp, body := apiGet(t, srv.URL+shopItems)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422: %.200s", resp.StatusCode, body)
	}
	if code, _ := errorHint(t, body); code != "view_too_large" {
		t.Errorf("code %q, want view_too_large", code)
	}
	if len(body) > 4096 || strings.Contains(string(body), "u003c") {
		t.Errorf("the refusal carries part of the view (%d bytes)", len(body))
	}
}

func TestProjectItemsNeverCarryDrafts(t *testing.T) {
	base := itemsServer(t).URL
	for _, q := range []string{"", "?drafts=true", "?drafts=false"} {
		for _, path := range []string{shopItems, "/api/v1/projects/github.com%2Facme%2Fplain/items", "/api/v1/projects/github.com%2Facme%2Fnone/items"} {
			resp, body := apiGet(t, base+path+q)
			if resp.StatusCode >= 500 {
				t.Errorf("%s%s: status %d: %s", path, q, resp.StatusCode, body)
			}
			for _, marker := range []string{`"wip"`, "draft-marker-body", "Draft marker", "shop-wip", "shop-style-draft-body", "Shop draft", "Shop style draft"} {
				if strings.Contains(string(body), marker) {
					t.Errorf("%s%s: body carries the draft marker %q: %s", path, q, marker, body)
				}
			}
		}
	}
	// An operator asking for drafts on this route still gets none.
	srv := namedTokensItemsServer(t)
	resp, body := do(t, http.MethodGet, srv.URL+shopItems+"?drafts=true", bearer(operatorToken))
	if resp.StatusCode >= 500 {
		t.Errorf("operator drafts=true: status %d: %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "draft-marker-body") || strings.Contains(string(body), "shop-wip") {
		t.Errorf("operator drafts=true: the view carries a draft: %s", body)
	}
}

func TestProjectItemsCarryTheSuppressionsAndLeaveSuppressedItemsOut(t *testing.T) {
	base := itemsServer(t).URL
	v, body := getView(t, base, shopItems)
	if strings.Join(v.Suppressions, "|") != "article:platform/notes" {
		t.Errorf("suppressions = %q, want [article:platform/notes]", v.Suppressions)
	}
	if _, ok := v.byID(t)["platform/notes"]; ok || strings.Contains(string(body), "global-notes-body") {
		t.Errorf("the suppressed Item platform/notes is in the view: %s", body)
	}
	// A Project that suppresses nothing answers an empty list, never null.
	_, plain := getView(t, base, "/api/v1/projects/github.com%2Facme%2Fplain/items")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(plain, &raw); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw["suppressions"])); got != "[]" {
		t.Errorf("plain suppressions = %s, want []", got)
	}
}

// Only winners are sent: nothing of another Project, no shadowed copy.
func TestProjectItemsSendOnlyWinnersAndNothingOfOtherProjects(t *testing.T) {
	base := itemsServer(t).URL

	_, shop := getView(t, base, shopItems)
	for _, leak := range []string{"other-only", "other-only-secret-body", "plain-only", "plain-only-body", "global-deploy-body", "Global deploy"} {
		if strings.Contains(string(shop), leak) {
			t.Errorf("shop view carries %q (another Project's Item or the shadowed global copy): %s", leak, shop)
		}
	}

	other, otherBody := getView(t, base, "/api/v1/projects/github.com%2Facme%2Fother/items")
	got := other.byID(t)
	sameIDs(t, "other view", got, "platform/deploy", "platform/policy", "platform/notes", "old-deploy", "style", "global-only", "other-only")
	if got["platform/deploy"].Scope != "global" || !strings.Contains(got["platform/deploy"].Content, "global-deploy-body") {
		t.Errorf("other platform/deploy = %+v, want the global Item", got["platform/deploy"])
	}
	if got["other-only"].Scope != "github.com/acme/other" || len(other.Suppressions) != 0 {
		t.Errorf("other-only scope %q, suppressions %q", got["other-only"].Scope, other.Suppressions)
	}
	for _, leak := range []string{"shop-runbook-body", "shop-deploy-body", "shop-rule", "plain-only"} {
		if strings.Contains(string(otherBody), leak) {
			t.Errorf("other view carries %q of another Project: %s", leak, otherBody)
		}
	}
}

// An unregistered Project gets the global view with the usual hint, as search
// and lookup do (ADR 0020 for the id).
func TestProjectItemsOfAnUnregisteredProjectAreTheGlobalViewWithAHint(t *testing.T) {
	base := itemsServer(t).URL
	v, body := getView(t, base, "/api/v1/projects/github.com%2Facme%2Fnone/items")
	got := v.byID(t)
	sameIDs(t, "unregistered view", got, "platform/deploy", "platform/policy", "platform/notes", "old-deploy", "style", "global-only")
	for id, it := range got {
		if it.Scope != "global" {
			t.Errorf("%s scope = %q, want global", id, it.Scope)
		}
	}
	if v.Hint == nil || !strings.Contains(*v.Hint, "github.com/acme/none") || !strings.Contains(*v.Hint, "not registered") {
		t.Errorf("hint = %v, want the unregistered-Project hint naming the Project", v.Hint)
	}
	if len(v.Suppressions) != 0 {
		t.Errorf("suppressions = %q, want none", v.Suppressions)
	}
	if strings.Contains(string(body), "shop-") || strings.Contains(string(body), "plain-only") {
		t.Errorf("the global view carries a Project Item: %s", body)
	}
	// A registered Project gets no hint.
	if shop, _ := getView(t, base, shopItems); shop.Hint != nil {
		t.Errorf("hint = %q for a registered Project", *shop.Hint)
	}
}

func TestProjectItemsMatchTheProjectIgnoringCase(t *testing.T) {
	base := itemsServer(t).URL
	v, _ := getView(t, base, "/api/v1/projects/github.com%2FACME%2FShop/items")
	if got := v.byID(t); got["runbook"].Scope != shopProject || v.Hint != nil || len(v.Suppressions) != 1 {
		t.Errorf("view of the upper-case spelling = %+v hint %v, want the shop view with the stored spelling in scope", got["runbook"], v.Hint)
	}
}

// ADR 0016: the id is one encoded segment; a bare-slash path is not this route.
func TestProjectItemsWithBareSlashesIsNotFound(t *testing.T) {
	base := itemsServer(t).URL
	resp, body := apiGet(t, base+"/api/v1/projects/github.com/acme/shop/items")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", resp.StatusCode, body)
	}
	if code, hint := errorHint(t, body); code != "not_found" || hint == "" {
		t.Errorf("code %q hint %q, want the not-found envelope with a hint", code, hint)
	}
}

// A malformed or hostile id is the 400 envelope and is never echoed.
func TestProjectItemsRejectAMalformedIdWithoutEchoingIt(t *testing.T) {
	base := itemsServer(t).URL
	for _, id := range []string{
		"just-a-name",
		"github.com%2Facme",
		"github.com%2Facme%2Fshop%2Fextra",
		"github.com%2Facme%2Fshop%0AInjected",
		"github.com%2Facme%2F%3Cscript%3E",
		"github.com%2Facme%2F..%2F..%2Fetc%2Fpasswd",
		"github.com%2Facme%2Fshop%20GET%20%2Fapi%2Fv1%2Fprojects",
		"github.com%2Facme%2Fshop%00",
	} {
		resp, body := apiGet(t, base+"/api/v1/projects/"+id+"/items")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", id, resp.StatusCode, body)
			continue
		}
		if code, hint := errorHint(t, body); code != "bad_request" || hint == "" {
			t.Errorf("%s: code %q hint %q, want bad_request with a hint", id, code, hint)
		}
		for _, leak := range []string{"Injected", "script", "passwd", "just-a-name", "GET /api", "global-deploy-body"} {
			if strings.Contains(string(body), leak) {
				t.Errorf("%s: body echoes or leaks %q: %s", id, leak, body)
			}
		}
	}
}

// The view is behind the API token; the 401 body shows no Item.
func TestProjectItemsNeedTheToken(t *testing.T) {
	base := itemsServer(t).URL
	for name, auth := range map[string]func(*http.Request){
		"no header":   nil,
		"wrong token": bearer("wrong-token-presented-here"),
	} {
		resp, body := do(t, http.MethodGet, base+shopItems, auth)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401: %s", name, resp.StatusCode, body)
		}
		for _, leak := range []string{"article:", "suppress", "shop-deploy-body", "global-style-body"} {
			if strings.Contains(string(body), leak) {
				t.Errorf("%s: the 401 body shows %q: %s", name, leak, body)
			}
		}
	}
}

// A reader token reads the view: it is a read, not an operator action.
func TestProjectItemsAreReadableWithAReaderToken(t *testing.T) {
	srv := namedTokensItemsServer(t)
	resp, body := do(t, http.MethodGet, srv.URL+shopItems, bearer(readerToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reader: status %d, want 200: %s", resp.StatusCode, body)
	}
}

func TestProjectItemsOnlyAnswerGet(t *testing.T) {
	base := itemsServer(t).URL
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		resp, body := do(t, method, base+shopItems, bearer(testToken))
		if resp.StatusCode < 400 || resp.StatusCode > 499 {
			t.Errorf("%s: status %d, want a 4xx: %s", method, resp.StatusCode, body)
		}
	}
}

// The spec says what the view is: content is untrusted, the operation exists
// and is a GET with the id as a path parameter.
func TestSpecMarksProjectItemsContentUntrusted(t *testing.T) {
	base := itemsServer(t).URL
	resp, body := apiGet(t, base+"/openapi.json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths["/api/v1/projects/{id}/items"].(map[string]any)
	get, _ := item["get"].(map[string]any)
	if get == nil {
		t.Fatal("the spec has no GET /api/v1/projects/{id}/items")
	}
	ok, _ := get["responses"].(map[string]any)["200"].(map[string]any)
	schema := lookupJSON(ok, "content", "application/json", "schema")
	props := schemaProps(doc, schema)
	list := schemaOf(doc, props["items"])
	elem := schemaProps(doc, schemaOf(doc, list["items"]))
	content, _ := elem["content"].(map[string]any)
	if content == nil {
		t.Fatalf("the 200 schema has no items[].content: %v", props)
	}
	if d, _ := schemaOf(doc, content)["description"].(string); !strings.Contains(strings.ToLower(d), "untrusted") {
		t.Errorf("items[].content description %q does not say it is untrusted", d)
	}
	for _, field := range []string{"id", "name", "keywords", "content", "status", "replaced_by", "scope", "enforced"} {
		if _, ok := elem[field]; !ok {
			t.Errorf("items[] has no %s property in the spec", field)
		}
	}
	if _, ok := props["suppressions"]; !ok {
		t.Error("the 200 schema has no suppressions property")
	}
	if _, ok := get["responses"].(map[string]any)["422"]; !ok {
		t.Error("the spec has no 422 response for a view over the export limit")
	}
}

func lookupJSON(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		next, _ := m[k].(map[string]any)
		if next == nil {
			return nil
		}
		m = next
	}
	return m
}

// schemaOf follows a local $ref.
func schemaOf(doc map[string]any, s any) map[string]any {
	m, _ := s.(map[string]any)
	for i := 0; i < 8 && m != nil; i++ {
		ref, _ := m["$ref"].(string)
		if !strings.HasPrefix(ref, "#/") {
			return m
		}
		var cur any = doc
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			cur, _ = cur.(map[string]any)[part]
		}
		m, _ = cur.(map[string]any)
	}
	return m
}

func schemaProps(doc map[string]any, s any) map[string]any {
	props, _ := schemaOf(doc, s)["properties"].(map[string]any)
	return props
}
