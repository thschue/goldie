package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #274 (slice F of #9): GET /api/v1/projects/{id} answers
// {id, suppressions}, with the id as one percent-encoded path segment (ADR
// 0016), and a lookup of a Suppressed Item answers 404 with a hint that names
// the Suppression and the project.yaml that sets it. Search leaves it out.
//
// The content (testdata/suppress-content) has three global Items
// (platform/deploy, platform/notes, style) and three Projects:
//   - github.com/acme/shop suppresses platform/notes and style, and a
//     reference that names nothing (article:team/gone, a sync warning);
//   - github.com/acme/other suppresses platform/deploy;
//   - github.com/acme/plain has no project.yaml.

const suppressContent = "testdata/suppress-content"

const (
	shopDetail  = "/api/v1/projects/github.com%2Facme%2Fshop"
	shopProject = "github.com/acme/shop"
	shopYAML    = "projects/github.com/acme/shop/project.yaml"
)

func suppressServerURL(t *testing.T) string {
	t.Helper()
	return newServerWith(t, platform.Config{ContentDir: suppressContent}).URL
}

type projectDetail struct {
	ID           string   `json:"id"`
	Suppressions []string `json:"suppressions"`
}

func getDetail(t *testing.T, base, path string) projectDetail {
	t.Helper()
	resp, body := apiGet(t, base+path)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, resp.StatusCode, body)
	}
	var d projectDetail
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("GET %s: body %q is not JSON: %v", path, body, err)
	}
	return d
}

func TestProjectDetailReturnsIdAndSortedSuppressions(t *testing.T) {
	base := suppressServerURL(t)
	d := getDetail(t, base, shopDetail)
	if d.ID != shopProject {
		t.Errorf("id = %q, want %q", d.ID, shopProject)
	}
	want := []string{"article:platform/notes", "article:style"}
	if strings.Join(d.Suppressions, "|") != strings.Join(want, "|") {
		t.Errorf("suppressions = %q, want %q (sorted; the reference that names nothing is not listed)", d.Suppressions, want)
	}
	// The other Project's own list is its own.
	other := getDetail(t, base, "/api/v1/projects/github.com%2Facme%2Fother")
	if len(other.Suppressions) != 1 || other.Suppressions[0] != "article:platform/deploy" {
		t.Errorf("other suppressions = %q, want [article:platform/deploy]", other.Suppressions)
	}
}

// A Project that suppresses nothing answers an empty list, never null.
func TestProjectDetailWithoutSuppressionsIsAnEmptyList(t *testing.T) {
	base := suppressServerURL(t)
	resp, body := apiGet(t, base+"/api/v1/projects/github.com%2Facme%2Fplain")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw["suppressions"])); got != "[]" {
		t.Errorf("suppressions = %s, want []", got)
	}
	if got := strings.TrimSpace(string(raw["id"])); got != `"github.com/acme/plain"` {
		t.Errorf("id = %s, want github.com/acme/plain", got)
	}
}

// ADR 0020: the Project is matched ignoring case; the id answered is the one
// the server stores, as in the project list.
func TestProjectDetailMatchesIgnoringCaseAndAnswersTheStoredSpelling(t *testing.T) {
	base := suppressServerURL(t)
	d := getDetail(t, base, "/api/v1/projects/github.com%2FACME%2FShop")
	if d.ID != shopProject || len(d.Suppressions) != 2 {
		t.Errorf("detail = %+v, want %s with its two suppressions", d, shopProject)
	}
}

func TestProjectDetailOfAnUnregisteredProjectIsNotFoundWithAHint(t *testing.T) {
	base := suppressServerURL(t)
	resp, body := apiGet(t, base+"/api/v1/projects/github.com%2Facme%2Fnone")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", resp.StatusCode, body)
	}
	code, hint := errorHint(t, body)
	if code != "not_found" || strings.TrimSpace(hint) == "" {
		t.Errorf("code %q hint %q, want not_found with a hint (ADR 0014)", code, hint)
	}
	const want = "List the registered Projects, then use one of them. " +
		"To register one, add a projects/host/org/repo/ directory to one of goldie's Sources, usually through the platform team."
	if hint != want {
		t.Errorf("hint %q, want the client-neutral %q (#539)", hint, want)
	}
}

// ADR 0016: the id is one encoded segment. A bare-slash path is not a Project
// detail and answers the not-found envelope.
func TestProjectDetailWithBareSlashesIsNotFound(t *testing.T) {
	base := suppressServerURL(t)
	resp, body := apiGet(t, base+"/api/v1/projects/github.com/acme/shop")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", resp.StatusCode, body)
	}
	if code, hint := errorHint(t, body); code != "not_found" || hint == "" {
		t.Errorf("code %q hint %q, want the not-found envelope with a hint", code, hint)
	}
}

// A malformed or hostile id is the 400 envelope and is never echoed.
func TestProjectDetailRejectsAMalformedIdWithoutEchoingIt(t *testing.T) {
	base := suppressServerURL(t)
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
		resp, body := apiGet(t, base+"/api/v1/projects/"+id)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", id, resp.StatusCode, body)
			continue
		}
		if code, _ := errorHint(t, body); code != "bad_request" {
			t.Errorf("%s: code %q, want bad_request", id, code)
		}
		for _, leak := range []string{"Injected", "script", "passwd", "just-a-name", "GET /api"} {
			if strings.Contains(string(body), leak) {
				t.Errorf("%s: body echoes %q: %s", id, leak, body)
			}
		}
	}
}

// The detail is behind the API token like every other route.
func TestProjectDetailNeedsTheToken(t *testing.T) {
	base := suppressServerURL(t)
	for name, auth := range map[string]func(*http.Request){
		"no header":   nil,
		"wrong token": bearer("wrong-token-presented-here"),
	} {
		resp, body := do(t, http.MethodGet, base+shopDetail, auth)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401: %s", name, resp.StatusCode, body)
		}
		if strings.Contains(string(body), "article:") || strings.Contains(string(body), "suppress") {
			t.Errorf("%s: the 401 body shows Suppressions: %s", name, body)
		}
	}
}

// Lookup of a suppressed Item, with the Project: 404, and a hint that names
// the Suppression and the project.yaml that sets it (PM decision, tier 2).
func TestLookupOfASuppressedItemIsNotFoundAndNamesTheSuppressionAndFile(t *testing.T) {
	base := suppressServerURL(t)
	for _, project := range []string{shopProject, "github.com/ACME/shop"} {
		for item, ref := range map[string]string{
			"platform%2Fnotes": "article:platform/notes",
			"style":            "article:style",
		} {
			resp, body := apiGet(t, base+"/api/v1/articles/"+item+"?project="+project)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s in %s: status %d, want 404: %s", item, project, resp.StatusCode, body)
				continue
			}
			code, hint := errorHint(t, body)
			if code != "not_found" {
				t.Errorf("%s in %s: code %q, want not_found", item, project, code)
			}
			if !strings.Contains(hint, ref) {
				t.Errorf("%s in %s: hint %q does not name the Suppression %q", item, project, hint, ref)
			}
			if !strings.Contains(hint, shopYAML) {
				t.Errorf("%s in %s: hint %q does not name %s", item, project, hint, shopYAML)
			}
			if strings.ContainsAny(hint, "\r\n<>") {
				t.Errorf("%s in %s: hint holds line breaks or markup: %q", item, project, hint)
			}
			// 404, not a hint on a 200: no article content leaks.
			if strings.Contains(string(body), "opts out") || strings.Contains(string(body), "Keep manifests") {
				t.Errorf("%s in %s: the 404 body carries the Item's content: %s", item, project, body)
			}
		}
	}
}

// Only the Project that suppresses is affected: no Project, another Project and
// an unregistered Project still read the global Item.
func TestSuppressedItemStillResolvesOutsideTheSuppressingProject(t *testing.T) {
	base := suppressServerURL(t)
	for _, query := range []string{
		"",
		"?project=github.com/acme/plain",
		"?project=github.com/acme/other",
		"?project=github.com/acme/none",
	} {
		resp, body := apiGet(t, base+"/api/v1/articles/platform%2Fnotes"+query)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("platform/notes%s: status %d, want 200: %s", query, resp.StatusCode, body)
		}
	}
	// other suppresses platform/deploy, which shop still reads.
	resp, body := apiGet(t, base+"/api/v1/articles/platform%2Fdeploy?project="+shopProject)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("platform/deploy in shop: status %d, want 200: %s", resp.StatusCode, body)
	}
	resp, body = apiGet(t, base+"/api/v1/articles/platform%2Fdeploy?project=github.com/acme/other")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("platform/deploy in other: status %d, want 404: %s", resp.StatusCode, body)
	} else if _, hint := errorHint(t, body); !strings.Contains(hint, "projects/github.com/acme/other/project.yaml") {
		t.Errorf("platform/deploy in other: hint %q does not name the other Project's project.yaml", hint)
	}
}

// An id that is not suppressed gets the ordinary not-found hint, and neither
// a Suppression nor a project.yaml is named: the hint never invents one, and
// it does not reveal another Project's file.
func TestOrdinaryNotFoundDoesNotMentionSuppressions(t *testing.T) {
	base := suppressServerURL(t)
	for _, path := range []string{
		"/api/v1/articles/no-such-article?project=" + shopProject,
		"/api/v1/articles/no-such-article",
		"/api/v1/articles/team%2Fgone?project=" + shopProject, // a reference that names nothing
	} {
		resp, body := apiGet(t, base+path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404: %s", path, resp.StatusCode, body)
			continue
		}
		_, hint := errorHint(t, body)
		for _, bad := range []string{"project.yaml", "uppress", "article:"} {
			if strings.Contains(hint, bad) {
				t.Errorf("%s: hint %q mentions %q for an Item no Suppression hides", path, hint, bad)
			}
		}
	}
}

// Search leaves a suppressed Item out, for the Project and for the same
// Project spelled in another case; everyone else still finds it.
func TestSearchLeavesSuppressedItemsOut(t *testing.T) {
	base := suppressServerURL(t)
	ids := func(query string) map[string]bool {
		resp, body := apiGet(t, base+"/api/v1/search?q=kubernetes"+query)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("search%s: status %d: %s", query, resp.StatusCode, body)
		}
		var got searchResults
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, r := range got.Results {
			m[r.ID] = true
		}
		return m
	}
	for _, q := range []string{"&project=" + shopProject, "&project=github.com/ACME/shop"} {
		got := ids(q)
		if got["platform/notes"] || got["style"] || !got["platform/deploy"] {
			t.Errorf("search%s ids = %v, want platform/deploy only", q, got)
		}
	}
	for _, q := range []string{"", "&project=github.com/acme/plain", "&project=github.com/acme/none"} {
		got := ids(q)
		if !got["platform/notes"] || !got["style"] || !got["platform/deploy"] {
			t.Errorf("search%s ids = %v, want all three global Items", q, got)
		}
	}
}
