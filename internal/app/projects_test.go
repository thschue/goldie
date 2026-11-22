package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// projectBody is the slice of a search or lookup response the project tests read.
type projectBody struct {
	Hint    *string `json:"hint"`
	Name    string  `json:"name"`
	Results []struct {
		ID    string `json:"id"`
		Scope string `json:"scope"`
	} `json:"results"`
}

func getProject(t *testing.T, path string, status int) projectBody {
	t.Helper()
	srv := newServer(t)
	resp, body := apiGet(t, srv.URL+path)
	if resp.StatusCode != status {
		t.Fatalf("GET %s: status %d, want %d: %s", path, resp.StatusCode, status, body)
	}
	var got projectBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestProjectsListsTheRegisteredProjects(t *testing.T) {
	srv := newServer(t)
	resp, body := apiGet(t, srv.URL+"/api/v1/projects")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if got, want := strings.TrimSpace(string(body)), `{"projects":["github.com/acme/shop"]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestSearchWithProjectPutsProjectItemsFirst(t *testing.T) {
	got := getProject(t, "/api/v1/search?q=kubernetes&project=github.com/acme/shop", http.StatusOK)
	if got.Hint != nil {
		t.Errorf("hint = %q for a registered Project", *got.Hint)
	}
	if len(got.Results) != 2 || got.Results[0].ID != "platform/deploy" || got.Results[0].Scope != "github.com/acme/shop" ||
		got.Results[1].ID != "platform/notes" || got.Results[1].Scope != "global" {
		t.Errorf("results = %+v, want the Project's deploy before the global notes", got.Results)
	}
}

func TestSearchMatchesTheProjectIgnoringCase(t *testing.T) {
	got := getProject(t, "/api/v1/search?q=kubernetes&project=github.com/ACME/Shop", http.StatusOK)
	if len(got.Results) == 0 || got.Results[0].Scope != "github.com/acme/shop" || got.Hint != nil {
		t.Errorf("results = %+v, hint %v, want the registered spelling as scope", got.Results, got.Hint)
	}
}

func TestSearchWithUnknownProjectIsGlobalWithAHint(t *testing.T) {
	got := getProject(t, "/api/v1/search?q=kubernetes&project=github.com/acme/none", http.StatusOK)
	if got.Hint == nil || !strings.Contains(*got.Hint, "/api/v1/projects") || strings.Contains(*got.Hint, "acme") {
		t.Errorf("hint = %v, want a next step that does not echo the Project", got.Hint)
	}
	if len(got.Results) != 2 || got.Results[0].Scope != "global" {
		t.Errorf("results = %+v, want the global results", got.Results)
	}
}

func TestSearchWithoutProjectHasNoHint(t *testing.T) {
	if got := getProject(t, "/api/v1/search?q=kubernetes", http.StatusOK); got.Hint != nil {
		t.Errorf("hint = %q without a project", *got.Hint)
	}
}

func TestLookupWithProject(t *testing.T) {
	if got := getProject(t, "/api/v1/articles/platform%2Fdeploy?project=github.com/acme/shop", http.StatusOK); got.Name != "Shop deploy" || got.Hint != nil {
		t.Errorf("registered Project: %+v, want its own article and no hint", got)
	}
	if got := getProject(t, "/api/v1/articles/platform%2Fdeploy?project=github.com/acme/none", http.StatusOK); got.Name != "Deploying to Kubernetes" || got.Hint == nil {
		t.Errorf("unknown Project: %+v, want the global article with a hint", got)
	}
	if got := getProject(t, "/api/v1/articles/platform%2Fdeploy", http.StatusOK); got.Name != "Deploying to Kubernetes" || got.Hint != nil {
		t.Errorf("no Project: %+v, want the global article and no hint", got)
	}
}

func TestMalformedProjectIsABadRequestWithAHint(t *testing.T) {
	srv := newServer(t)
	projects := []string{"", "acme/shop", "github.com/acme", "github.com/acme/shop/extra", "GitHub.com/acme/shop",
		"github.com/.acme/shop", "github.com/acme/sh%20op", "github.com/acme/shop%0A"}
	for _, project := range projects {
		for _, path := range []string{"/api/v1/search?q=kubernetes&project=", "/api/v1/articles/platform%2Fdeploy?project="} {
			resp, body := apiGet(t, srv.URL+path+project)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s%s: status %d, want 400", path, project, resp.StatusCode)
				continue
			}
			if code, hint := errorHint(t, body); code != "bad_request" || !strings.Contains(hint, "host/org/repo") {
				t.Errorf("%s%s: code %q, hint %q", path, project, code, hint)
			}
		}
	}
}

func TestProjectsNeedsTheToken(t *testing.T) {
	srv := newServer(t)
	if resp, _ := do(t, http.MethodGet, srv.URL+"/api/v1/projects", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", resp.StatusCode)
	}
}
