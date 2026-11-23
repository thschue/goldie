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

// Acceptance tests for #585: GET /api/v1/skills lists each skill with what a
// skill card needs, in four optional fields of Skill: scope (the value
// SearchHit.scope carries), status, description (both from the frontmatter of
// SKILL.md, as an agent's are) and requires_sync (true when the bundle holds a
// script, false for a text-only bundle). An absent field means unknown.
// Fixtures are written per test: the bundles are
//   - deploy: global, with scripts/run.sh, and a shop copy without scripts;
//   - lint: global, text only, with a description in its frontmatter;
//   - wip-skill: global draft.

type cardSkill struct {
	Name         string  `json:"name"`
	Scope        *string `json:"scope"`
	Status       *string `json:"status"`
	Description  *string `json:"description"`
	RequiresSync *bool   `json:"requires_sync"`
}

const cardShop = "github.com/acme/shop"

func skillCardServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	writeRaw(t, dir, "global/skills/deploy/SKILL.md", "---\ndescription: Deploys the app\n---\n# Deploy global\n")
	writeRaw(t, dir, "global/skills/deploy/scripts/run.sh", "#!/bin/sh\necho hi\n")
	writeRaw(t, dir, "global/skills/lint/SKILL.md", "---\ndescription: Lints the code\n---\n# Lint\n")
	writeRaw(t, dir, "global/skills/wip-skill/SKILL.md", "---\ndescription: wip-skill-marker\nstatus: draft\n---\n# Wip\n")
	writeRaw(t, dir, "projects/"+cardShop+"/skills/deploy/SKILL.md", "---\ndescription: Deploys the shop\n---\n# Deploy shop\n")
	writeRaw(t, dir, "projects/"+cardShop+"/skills/deploy/checklist.txt", "check it\n")
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: dir, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)
	return srv
}

func cardList(t *testing.T, srv *httptest.Server, token, query string) map[string]cardSkill {
	t.Helper()
	path := "/api/v1/skills"
	if query != "" {
		path += "?" + query
	}
	status, body := itemsGet(t, srv, token, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, status, body)
	}
	var res struct {
		Skills []cardSkill `json:"skills"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, body)
	}
	out := map[string]cardSkill{}
	for _, s := range res.Skills {
		out[s.Name] = s
	}
	return out
}

func wantCardScope(t *testing.T, label string, s cardSkill, want string) {
	t.Helper()
	if s.Scope == nil {
		t.Errorf("%s: scope is absent, want %q", label, want)
	} else if *s.Scope != want {
		t.Errorf("%s: scope = %q, want %q", label, *s.Scope, want)
	}
}

func wantCardSync(t *testing.T, label string, s cardSkill, want bool) {
	t.Helper()
	if s.RequiresSync == nil {
		t.Errorf("%s: requires_sync is absent, want %v", label, want)
	} else if *s.RequiresSync != want {
		t.Errorf("%s: requires_sync = %v, want %v", label, *s.RequiresSync, want)
	}
}

// Criterion: Skill declares four optional fields, none required, no enum.
func TestServedSpecDeclaresTheSkillCardFieldsAsOptional(t *testing.T) {
	doc := specDoc(t)
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	skill, _ := schemas["Skill"].(map[string]any)
	props, _ := skill["properties"].(map[string]any)
	want := map[string]string{"scope": "string", "status": "string", "description": "string", "requires_sync": "boolean"}
	for name, typ := range want {
		p := contracttest.ResolveRef(doc, props[name])
		if p == nil {
			t.Errorf("Skill declares no %s property", name)
			continue
		}
		if p["type"] != typ {
			t.Errorf("Skill.%s type %v, want %s", name, p["type"], typ)
		}
		if _, ok := p["enum"]; ok {
			t.Errorf("Skill.%s has an enum %v; the value is open (ADR 0011)", name, p["enum"])
		}
		if d, _ := p["description"].(string); strings.TrimSpace(d) == "" {
			t.Errorf("Skill.%s has no description", name)
		}
	}
	req, _ := skill["required"].([]any)
	for _, r := range req {
		if _, isNew := want[r.(string)]; isNew {
			t.Errorf("Skill requires %v; the field is optional (additive, ADR 0011)", r)
		}
	}
	if !slices.Contains(req, any("name")) || !slices.Contains(req, any("files")) {
		t.Errorf("Skill required = %v, must still be name and files", req)
	}
}

// Criterion: scope is global, or the Project whose copy won.
func TestSkillListNamesTheScopeOfEachCopy(t *testing.T) {
	srv := skillCardServer(t)
	plain := cardList(t, srv, readerToken, "")
	wantCardScope(t, "deploy, no Project", plain["deploy"], "global")
	wantCardScope(t, "lint, no Project", plain["lint"], "global")

	shop := cardList(t, srv, readerToken, "project="+url.QueryEscape(cardShop))
	wantCardScope(t, "deploy in the shop view", shop["deploy"], cardShop)
	wantCardScope(t, "lint in the shop view", shop["lint"], "global")

	other := cardList(t, srv, readerToken, "project=github.com/acme/unknown")
	wantCardScope(t, "deploy in an unregistered Project's view", other["deploy"], "global")
}

// Criterion: the skill scope equals the SearchHit.scope for the same name and Project.
func TestSkillListScopeEqualsTheSearchHitScope(t *testing.T) {
	srv := skillCardServer(t)
	for _, project := range []string{"", cardShop} {
		q := url.Values{"q": {"deploy"}, "kind": {"skill"}}
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
		list := cardList(t, srv, readerToken, query)
		found := false
		for _, h := range res.Results {
			if h.ID != "deploy" {
				continue
			}
			found = true
			wantCardScope(t, "deploy in project "+project+" (search says "+h.Scope+")", list["deploy"], h.Scope)
		}
		if !found {
			t.Errorf("search project %q found no deploy skill hit: %s", project, body)
		}
	}
}

// Criterion: a skill with scripts has requires_sync true, a text-only one false.
func TestSkillListFlagsRequiresSyncForScripts(t *testing.T) {
	srv := skillCardServer(t)
	plain := cardList(t, srv, readerToken, "")
	wantCardSync(t, "deploy (has scripts/run.sh)", plain["deploy"], true)
	wantCardSync(t, "lint (text only)", plain["lint"], false)

	// The shop copy of deploy won whole: it has no script, so it is text only.
	shop := cardList(t, srv, readerToken, "project="+url.QueryEscape(cardShop))
	wantCardSync(t, "shop deploy (SKILL.md and checklist.txt)", shop["deploy"], false)
}

// Criterion: description comes from the frontmatter of SKILL.md.
func TestSkillListCarriesTheDescriptionFromSkillMd(t *testing.T) {
	srv := skillCardServer(t)
	plain := cardList(t, srv, readerToken, "")
	for name, want := range map[string]string{"deploy": "Deploys the app", "lint": "Lints the code"} {
		if d := plain[name].Description; d == nil || *d != want {
			t.Errorf("%s: description = %v, want %q", name, d, want)
		}
	}
	shop := cardList(t, srv, readerToken, "project="+url.QueryEscape(cardShop))
	if d := shop["deploy"].Description; d == nil || *d != "Deploys the shop" {
		t.Errorf("shop deploy: description = %v, want the shop copy's", d)
	}
}

// Criterion: a draft skill is listed only with drafts=true and the operator
// role, carries status draft, and a reader asking for drafts is a 403.
func TestDraftSkillIsListedOnlyForAnOperatorAskingForDrafts(t *testing.T) {
	srv := skillCardServer(t)
	for _, q := range []string{"", "drafts=false"} {
		if _, ok := cardList(t, srv, operatorToken, q)["wip-skill"]; ok {
			t.Errorf("an operator with %q sees the draft skill", q)
		}
		if _, ok := cardList(t, srv, readerToken, q)["wip-skill"]; ok {
			t.Errorf("a reader with %q sees the draft skill", q)
		}
	}

	list := cardList(t, srv, operatorToken, "drafts=true")
	wip, ok := list["wip-skill"]
	if !ok {
		t.Fatalf("an operator with drafts=true does not see the draft skill: %v", list)
	}
	if wip.Status == nil || *wip.Status != "draft" {
		t.Errorf("draft skill status = %v, want draft", wip.Status)
	}
	wantCardScope(t, "draft skill", wip, "global")
	if _, ok := list["deploy"]; !ok {
		t.Errorf("drafts=true dropped the published skills: %v", list)
	}

	status, body := itemsGet(t, srv, readerToken, "/api/v1/skills?drafts=true")
	if status != http.StatusForbidden {
		t.Errorf("reader GET skills?drafts=true: status %d, want 403: %s", status, body)
	}
	if strings.Contains(string(body), "wip-skill") {
		t.Errorf("the 403 for a reader leaks the draft: %s", body)
	}
}

// Criterion: old clients keep working: the existing keys stay, and a skill
// without a description or status lists without them (unknown, not "").
func TestSkillListKeepsItsKeysAndLeavesUnknownFieldsAbsent(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "global/skills/bare/SKILL.md", "# Bare\n")
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: dir, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)

	_, body := itemsGet(t, srv, readerToken, "/api/v1/skills")
	var res struct {
		Skills []map[string]any `json:"skills"`
	}
	if err := json.Unmarshal(body, &res); err != nil || len(res.Skills) != 1 {
		t.Fatalf("body %s: %v", body, err)
	}
	s := res.Skills[0]
	if s["name"] != "bare" || s["files"] == nil {
		t.Errorf("the skill lost name or files: %s", body)
	}
	if v, ok := s["description"]; ok {
		t.Errorf("a skill without a description lists description %v, want it absent", v)
	}
	if v, ok := s["status"]; ok {
		t.Errorf("a published skill lists status %v, want it absent", v)
	}
	if s["requires_sync"] != false {
		t.Errorf("text-only skill requires_sync = %v, want false", s["requires_sync"])
	}
}
