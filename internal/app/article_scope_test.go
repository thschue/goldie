package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/engine"
)

// Acceptance tests for #577: GET /api/v1/articles/{id} names the Scope of the
// copy it serves in the optional scope field of Article, the value SearchHit
// and ProjectItem already carry ("global", "local" or the Project's
// host/org/repo). The fixtures are testdata/items-content (the shop copy of
// platform/deploy shadows the global one), testdata/lifecycle-content (drafts)
// and per-test directories.

type scopedArticle struct {
	ID    string  `json:"id"`
	Scope *string `json:"scope"`
}

// articleScope reads the scope of GET /api/v1/articles/{id}?query from srv.
func articleScope(t *testing.T, base, id, query string) scopedArticle {
	t.Helper()
	u := base + "/api/v1/articles/" + url.PathEscape(id)
	if query != "" {
		u += "?" + query
	}
	resp, body := apiGet(t, u)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", u, resp.StatusCode, body)
	}
	var a scopedArticle
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("GET %s: body %q is not JSON: %v", u, body, err)
	}
	return a
}

func wantArticleScope(t *testing.T, what string, got scopedArticle, want string) {
	t.Helper()
	if got.Scope == nil {
		t.Errorf("%s: scope is absent, want %q", what, want)
	} else if *got.Scope != want {
		t.Errorf("%s: scope = %q, want %q", what, *got.Scope, want)
	}
}

// Criterion: Article declares an optional scope string, not required, no enum.
func TestServedSpecDeclaresAnOptionalScopeOnArticle(t *testing.T) {
	doc := specDoc(t)
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	article, _ := schemas["Article"].(map[string]any)
	props, _ := article["properties"].(map[string]any)
	p := contracttest.ResolveRef(doc, props["scope"])
	if p == nil {
		t.Fatal("Article declares no scope property")
	}
	if p["type"] != "string" {
		t.Errorf("Article.scope type %v, want string", p["type"])
	}
	if _, ok := p["enum"]; ok {
		t.Errorf("Article.scope has an enum %v; the Scope is open (ADR 0011)", p["enum"])
	}
	if d, _ := p["description"].(string); strings.TrimSpace(d) == "" {
		t.Error("Article.scope has no description")
	}
	req, _ := article["required"].([]any)
	for _, r := range req {
		if r == "scope" {
			t.Error("Article requires scope; the field is optional (additive, ADR 0011)")
		}
	}
}

// Criterion: a global article returns scope global.
func TestArticleNamesTheGlobalScope(t *testing.T) {
	srv := itemsServer(t)
	wantArticleScope(t, "global-only, no Project", articleScope(t, srv.URL, "global-only", ""), "global")
	wantArticleScope(t, "platform/deploy, no Project", articleScope(t, srv.URL, "platform/deploy", ""), "global")
	wantArticleScope(t, "global-only in the shop view", articleScope(t, srv.URL, "global-only", "project="+overridesShop), "global")
}

// Criterion: when a Project copy wins the response has host/org/repo.
func TestArticleNamesTheProjectScopeWhenAProjectCopyWins(t *testing.T) {
	srv := itemsServer(t)
	for _, id := range []string{"platform/deploy", "runbook"} {
		wantArticleScope(t, id+" in the shop view", articleScope(t, srv.URL, id, "project="+overridesShop), overridesShop)
	}
	// Another Project's view must not show the shop copy's scope.
	wantArticleScope(t, "platform/deploy in the plain view", articleScope(t, srv.URL, "platform/deploy", "project=github.com/acme/plain"), "global")
}

// Criterion: when a local copy wins the response has local.
func TestArticleNamesTheLocalScopeWhenALocalCopyWins(t *testing.T) {
	content, repo := t.TempDir(), t.TempDir()
	writeRaw(t, content, "global/articles/style.md", "---\nname: Global style\nkeywords: [style]\n---\nglobal-style-body\n")
	writeRaw(t, content, "projects/github.com/acme/shop/articles/other.md", "---\nname: Other\nkeywords: [other]\n---\nother\n")
	writeRaw(t, repo, ".goldie/articles/style.md", "---\nname: Local style\nkeywords: [style]\n---\nlocal-style-body\n")
	src := engine.NewLocalSource(engine.NewDirSource(content), filepath.Join(repo, ".goldie"))
	syncer := engine.NewSyncer(src)
	if err := syncer.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	srv := providerServer(t, syncer.Current)
	wantArticleScope(t, "style, no Project", articleScope(t, srv.URL, "style", ""), "local")
	wantArticleScope(t, "style in the shop view", articleScope(t, srv.URL, "style", "project=github.com/acme/shop"), "local")
}

// Criterion: an enforced global article a Project copy tries to shadow still
// reports global.
func TestArticleOfAnEnforcedGlobalStillNamesTheGlobalScope(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "global/articles/rules.md", "---\nname: Rules\nkeywords: [rules]\nenforced: true\n---\nenforced-global-body\n")
	writeRaw(t, dir, "projects/github.com/acme/shop/articles/other.md", "---\nname: Other\nkeywords: [other]\n---\nother\n")
	syncer := engine.NewSyncer(engine.NewDirSource(dir))
	if err := syncer.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	srv := providerServer(t, syncer.Current)
	writeRaw(t, dir, "projects/github.com/acme/shop/articles/rules.md", "---\nname: Shop rules\nkeywords: [rules]\n---\nshop-rules-body\n")
	_ = syncer.Poll(t.Context()) // refusing the copy is one way to keep the global winning

	a := articleScope(t, srv.URL, "rules", "project=github.com/acme/shop")
	wantArticleScope(t, "enforced rules in the shop view", a, "global")
	resp, body := apiGet(t, srv.URL+"/api/v1/articles/rules?project=github.com/acme/shop")
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), "shop-rules-body") {
		t.Errorf("lookup rules: status %d, want the enforced global: %s", resp.StatusCode, body)
	}
	// An enforced Item of the items fixture reports global in the shop view too.
	wantArticleScope(t, "platform/policy in the shop view", articleScope(t, itemsServer(t).URL, "platform/policy", "project="+overridesShop), "global")
}

// Criterion: a draft read with drafts=true carries its scope; without the
// operator role it is still a 403.
func TestDraftArticleCarriesItsScopeAndStaysOperatorOnly(t *testing.T) {
	srv := lifecycleServer(t)
	code, _, body := lifecycleLookup(t, srv, operatorToken, "wip", "drafts=true")
	if code != http.StatusOK {
		t.Fatalf("operator lookup wip with drafts=true: status %d: %s", code, body)
	}
	var a scopedArticle
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatal(err)
	}
	wantArticleScope(t, "global draft wip", a, "global")

	_, _, body = lifecycleLookup(t, srv, operatorToken, "style", "drafts=true&project="+lifecycleProject)
	a = scopedArticle{}
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatal(err)
	}
	wantArticleScope(t, "Project draft style", a, lifecycleProject)

	for _, token := range []string{readerToken, hashedSecret} {
		code, _, body := lifecycleLookup(t, srv, token, "wip", "drafts=true")
		if code != http.StatusForbidden {
			t.Errorf("reader lookup wip with drafts=true: status %d, want 403: %s", code, body)
		}
		if strings.Contains(string(body), `"scope"`) || strings.Contains(string(body), "draft-marker-body") {
			t.Errorf("the 403 for a reader carries a scope or the draft: %s", body)
		}
	}
}

// Criterion: for an unregistered Project the hint response still returns the
// global article with scope global.
func TestArticleForAnUnregisteredProjectNamesTheGlobalScopeWithTheHint(t *testing.T) {
	resp, body := apiGet(t, itemsServer(t).URL+"/api/v1/articles/platform%2Fdeploy?project=github.com/acme/none")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	var a struct {
		Scope *string `json:"scope"`
		Hint  *string `json:"hint"`
	}
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatal(err)
	}
	if a.Hint == nil || *a.Hint != wantUnregisteredHint("github.com/acme/none") {
		t.Errorf("hint = %v, want the unregistered hint: %s", a.Hint, body)
	}
	if a.Scope == nil || *a.Scope != "global" {
		t.Errorf("scope = %v, want global: %s", a.Scope, body)
	}
}

// Criterion: for the same id and Project, scope equals the SearchHit.scope.
func TestArticleScopeEqualsTheSearchHitScopeForTheSameIdAndProject(t *testing.T) {
	srv := itemsServer(t)
	for _, project := range []string{"", overridesShop, "github.com/acme/plain", "github.com/acme/none"} {
		q := url.Values{"q": {"deploy"}}
		if project != "" {
			q.Set("project", project)
		}
		resp, body := apiGet(t, srv.URL+"/api/v1/search?"+q.Encode())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("search project %q: status %d: %s", project, resp.StatusCode, body)
		}
		var res struct {
			Results []lifecycleHit `json:"results"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Results) == 0 {
			t.Fatalf("search project %q found nothing: %s", project, body)
		}
		for _, h := range res.Results {
			query := ""
			if project != "" {
				query = "project=" + project
			}
			a := articleScope(t, srv.URL, h.ID, query)
			wantArticleScope(t, "article "+h.ID+" in project "+project+" (search says "+h.Scope+")", a, h.Scope)
		}
	}
}
