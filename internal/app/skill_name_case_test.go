package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #509, the HTTP contract seam: a skill name is matched
// case-insensitively, as an agent name is. GET /api/v1/skills/{name} and
// GET /api/v1/skills/{name}/files/{path} find "Deploy" when asked for
// "deploy", global and Project scope alike, and answer the name as the
// bundle's directory spells it. A Project bundle shadows a global one whose
// directory differs in case, and the list names it once. Only the skill name
// folds: a file path inside the bundle stays exact. A name that is no skill in
// any case is still the 404 envelope. Agents already behave this way, and the
// agent route is asserted alongside as the reference.

const caseContentProject = "github.com/acme/shop"

// caseContentServer serves a dir Source with the global skills Deploy (with
// scripts/run.sh) and Lint, the Project skill deploy of github.com/acme/shop
// (with checklist.txt) shadowing Deploy, and the agent reviewer.
func caseContentServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	proj := "projects/" + caseContentProject
	for p, body := range map[string]string{
		"global/skills/Deploy/SKILL.md":       "# Deploy global\n",
		"global/skills/Deploy/scripts/run.sh": "#!/bin/sh\necho hi\n",
		"global/skills/Lint/SKILL.md":         "# Lint\n",
		"global/agents/reviewer.md":           "---\nname: reviewer\ndescription: Reviews code\n---\nBody\n",
		proj + "/skills/deploy/SKILL.md":      "# Deploy shop\n",
		proj + "/skills/deploy/checklist.txt": "check it\n",
	} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: dir, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSkillGetFindsADifferentlyCasedName(t *testing.T) {
	srv := caseContentServer(t)
	for _, name := range []string{"Deploy", "deploy", "DEPLOY"} {
		e := itemsOne(t, srv, readerToken, "/api/v1/skills/"+name)
		if e.Name != "Deploy" {
			t.Errorf("GET skills/%s: name %q, want the bundle's own spelling Deploy", name, e.Name)
		}
		wantFiles(t, "global "+name, e, []itemFile{{"SKILL.md", 16}, {"scripts/run.sh", 18}})
	}
	// The agent route is the reference.
	if a := itemsOne(t, srv, readerToken, "/api/v1/agents/REVIEWER"); a.Name != "reviewer" {
		t.Errorf("GET agents/REVIEWER: name %q, want reviewer", a.Name)
	}
}

func TestSkillGetInAProjectFindsADifferentlyCasedNameAndShadowsTheGlobalBundle(t *testing.T) {
	srv := caseContentServer(t)
	q := "?project=" + url.QueryEscape(caseContentProject)
	for _, name := range []string{"Deploy", "deploy", "DEPLOY"} {
		e := itemsOne(t, srv, readerToken, "/api/v1/skills/"+name+q)
		if e.Name != "deploy" {
			t.Errorf("GET skills/%s%s: name %q, want the Project bundle's spelling deploy", name, q, e.Name)
		}
		wantFiles(t, "project "+name, e, []itemFile{{"SKILL.md", 14}, {"checklist.txt", 9}})
	}
	// The global view still answers the global bundle.
	wantFiles(t, "global view", itemsOne(t, srv, readerToken, "/api/v1/skills/deploy"),
		[]itemFile{{"SKILL.md", 16}, {"scripts/run.sh", 18}})

	// The list names the skill once, by the winner's spelling.
	skills := itemsList(t, srv, readerToken, "/api/v1/skills"+q, "skills")
	if got := itemNames(skills); !slices.Equal(got, []string{"Lint", "deploy"}) {
		t.Errorf("skills in the Project view %v, want [Lint deploy]: one entry per name, whatever its case", got)
	}
}

func TestSkillFileIsFoundByADifferentlyCasedSkillName(t *testing.T) {
	srv := caseContentServer(t)
	q := "?project=" + url.QueryEscape(caseContentProject)
	for _, name := range []string{"Deploy", "deploy", "DEPLOY"} {
		status, body := itemsGet(t, srv, readerToken, skillFilePath(name, "scripts/run.sh"))
		if status != http.StatusOK || string(body) != "#!/bin/sh\necho hi\n" {
			t.Errorf("GET file of %s: status %d body %q, want 200 and the global script", name, status, body)
		}
		status, body = itemsGet(t, srv, readerToken, skillFilePath(name, "checklist.txt")+q)
		if status != http.StatusOK || string(body) != "check it\n" {
			t.Errorf("GET Project file of %s: status %d body %q, want 200 and the Project checklist", name, status, body)
		}
		// The Project bundle answers whole: the shadowed global file is a miss.
		if status, body = itemsGet(t, srv, readerToken, skillFilePath(name, "scripts/run.sh")+q); status != http.StatusNotFound {
			t.Errorf("GET shadowed global file via %s in the Project view: status %d, want 404: %s", name, status, body)
		}
	}
}

// Negative cases: only the skill name folds. A path inside the bundle stays
// exact, a name that is no skill in any case is a 404 that echoes nothing, and
// a differently cased name is no way out of the bundle.
func TestSkillCaseFoldingDoesNotWidenWhatIsServed(t *testing.T) {
	srv := caseContentServer(t)
	for _, path := range []string{
		skillFilePath("Deploy", "skill.md"),
		skillFilePath("Deploy", "SCRIPTS/run.sh"),
		skillFilePath("Deploy", "../Lint/SKILL.md"),
		"/api/v1/skills/Missing",
		"/api/v1/skills/Deploys",
		"/api/v1/skills/%2E%2E%2FLint",
		"/api/v1/skills/Deploy%2F..%2FLint",
	} {
		status, body := itemsGet(t, srv, readerToken, path)
		if status != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404: %s", path, status, body)
			continue
		}
		if code, hint := errorHint(t, body); code != "not_found" || hint == "" {
			t.Errorf("GET %s: code %q hint %q, want not_found with a hint", path, code, hint)
		}
	}
}
