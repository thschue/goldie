package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// Draft skills follow the rule Items have: hidden from every read unless an
// operator asks for drafts, and a nearer draft never hides a published copy.
func draftSkillServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	writeRaw(t, dir, "global/skills/deploy/SKILL.md", "---\ndescription: Deploys the app\nkeywords: [ship]\n---\n# Deploy\n")
	writeRaw(t, dir, "global/skills/wip/SKILL.md", "---\nstatus: draft\nkeywords: [wipword]\n---\n# Wip\n")
	// A mistyped description must not turn a draft into a published skill.
	writeRaw(t, dir, "global/skills/odd/SKILL.md", "---\nstatus: draft\ndescription: [not, a, string]\nkeywords: [oddword]\n---\n# Odd\n")
	writeRaw(t, dir, "projects/"+cardShop+"/skills/deploy/SKILL.md", "---\nstatus: draft\n---\n# Deploy shop draft\n")
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: dir, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDraftSkillIsHiddenFromGetFileAndSearchUnlessAnOperatorAsks(t *testing.T) {
	srv := draftSkillServer(t)
	for _, name := range []string{"wip", "odd"} {
		for _, p := range []string{"/api/v1/skills/" + name, "/api/v1/skills/" + name + "/files/SKILL.md"} {
			if status, body := itemsGet(t, srv, readerToken, p); status != http.StatusNotFound {
				t.Errorf("reader GET %s: status %d, want 404: %s", p, status, body)
			}
			if status, body := itemsGet(t, srv, operatorToken, p); status != http.StatusNotFound {
				t.Errorf("operator without drafts GET %s: status %d, want 404: %s", p, status, body)
			}
			if status, body := itemsGet(t, srv, operatorToken, p+"?drafts=true"); status != http.StatusOK {
				t.Errorf("operator GET %s?drafts=true: status %d, want 200: %s", p, status, body)
			}
		}
	}
	for _, q := range []string{"wipword", "oddword"} {
		path := "/api/v1/search?kind=skill&q=" + q
		if _, body := itemsGet(t, srv, readerToken, path); strings.Contains(string(body), `"id"`) {
			t.Errorf("reader search %s finds a draft: %s", q, body)
		}
		_, body := itemsGet(t, srv, operatorToken, path+"&drafts=true")
		if !strings.Contains(string(body), `"status":"draft"`) {
			t.Errorf("operator search %s with drafts=true: want a draft hit: %s", q, body)
		}
	}
}

// A SKILL.md whose status cannot be read fails closed: it is a draft, hidden
// from readers, rather than published.
func TestUnreadableSkillStatusFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "global/skills/colon/SKILL.md", "---\nstatus: draft\ndescription: Deploy: build and ship\n---\n# C\n")
	writeRaw(t, dir, "global/skills/listed/SKILL.md", "---\nstatus: [draft]\n---\n# L\n")
	writeRaw(t, dir, "global/skills/unknown/SKILL.md", "---\nstatus: wip\n---\n# U\n")
	writeRaw(t, dir, "global/skills/unclosed/SKILL.md", "---\nstatus: draft\n# never closed\n")
	writeRaw(t, dir, "global/skills/plain/SKILL.md", "# No frontmatter\n")
	writeRaw(t, dir, "global/skills/pub/SKILL.md", "---\nstatus: published\n---\n# P\n")
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: dir, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)

	reader := cardList(t, srv, readerToken, "")
	for _, n := range []string{"colon", "listed", "unknown", "unclosed"} {
		if _, ok := reader[n]; ok {
			t.Errorf("reader sees %s, whose status cannot be read", n)
		}
		if status, body := itemsGet(t, srv, readerToken, "/api/v1/skills/"+n); status != http.StatusNotFound {
			t.Errorf("reader GET %s: status %d, want 404: %s", n, status, body)
		}
	}
	for _, n := range []string{"plain", "pub"} {
		if s, ok := reader[n]; !ok || s.Status != nil {
			t.Errorf("%s: listed %v with status %v, want published", n, ok, s.Status)
		}
	}
	for _, n := range []string{"colon", "listed", "unknown", "unclosed"} {
		if s := cardList(t, srv, operatorToken, "drafts=true")[n]; s.Status == nil || *s.Status != "draft" {
			t.Errorf("operator with drafts=true: %s status = %v, want draft", n, s.Status)
		}
	}
}

func TestNearerDraftSkillDoesNotHideThePublishedGlobalOne(t *testing.T) {
	srv := draftSkillServer(t)
	q := "project=" + url.QueryEscape(cardShop)
	list := cardList(t, srv, readerToken, q)
	wantCardScope(t, "deploy under a shop draft", list["deploy"], "global")
	if status, body := itemsGet(t, srv, readerToken, "/api/v1/skills/deploy?"+q); status != http.StatusOK {
		t.Errorf("reader GET deploy in the shop view: status %d, want the global one: %s", status, body)
	}
	drafts := cardList(t, srv, operatorToken, q+"&drafts=true")
	wantCardScope(t, "deploy with drafts=true", drafts["deploy"], cardShop)
	if s := drafts["odd"]; s.Status == nil || *s.Status != "draft" {
		t.Errorf("odd (draft, mistyped description): status = %v, want draft", s.Status)
	}
}
