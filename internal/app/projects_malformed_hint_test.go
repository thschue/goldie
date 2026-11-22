package app

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

var malformedProjects = []string{"", "acme/shop", "github.com/acme", "github.com/acme/shop/extra", "GitHub.com/acme/shop",
	"github.com/.acme/shop", "github.com/acme/sh%20op", "github.com/acme/shop%0A",
	"github.com/acme/%3Cscript%3E", "github.com/../etc/passwd"}

var malformedPaths = []string{"/api/v1/search?q=kubernetes&project=", "/api/v1/articles/platform%2Fdeploy?project="}

// malformedHint returns the hint of the 400 a malformed project gets.
func malformedHint(t *testing.T, path string) string {
	t.Helper()
	srv := newServer(t)
	resp, body := apiGet(t, srv.URL+path)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET %s: status %d, want 400: %s", path, resp.StatusCode, body)
	}
	code, hint := errorHint(t, body)
	if code != "bad_request" {
		t.Fatalf("GET %s: code %q, want bad_request", path, code)
	}
	return hint
}

// The hint gives the expected form with an example a caller can copy, and
// names no HTTP route and no client command (the style #187 set for the
// unregistered-Project hint, ADR 0014).
func TestMalformedProjectHintGivesTheFormAndAnExampleAndNamesNoRouteOrCommand(t *testing.T) {
	example := regexp.MustCompile(`[a-z0-9][a-z0-9.-]*\.[a-z]+/[A-Za-z0-9_-][A-Za-z0-9._-]*/[A-Za-z0-9_-][A-Za-z0-9._-]*`)
	for _, project := range malformedProjects {
		for _, path := range malformedPaths {
			hint := malformedHint(t, path+project)
			if !strings.Contains(hint, "host/org/repo") {
				t.Errorf("%s%s: hint %q does not give the host/org/repo form", path, project, hint)
			}
			id := example.FindString(hint)
			if id == "" || !contract.ValidProjectID(id) {
				t.Errorf("%s%s: hint %q has no example Project id such as github.com/acme/payments", path, project, hint)
			}
			if !strings.Contains(hint, "github.com/acme/payments") {
				t.Errorf("%s%s: hint %q, want the example github.com/acme/payments", path, project, hint)
			}
			for _, bad := range []string{"/api/", "api/v1", "/projects", "GET", "POST", "curl", "goldie ", "--project", "list the registered", "\n", "\r", "<", "{{", "%s"} {
				if strings.Contains(hint, bad) {
					t.Errorf("%s%s: hint contains %q: %q", path, project, bad, hint)
				}
			}
		}
	}
}

// The hint is one fixed text: it never echoes the rejected value.
func TestMalformedProjectHintIsTheSameForEveryMalformedValueAndEchoesNothing(t *testing.T) {
	want := malformedHint(t, malformedPaths[0]+"acme/shop")
	for _, project := range malformedProjects {
		for _, path := range malformedPaths {
			if got := malformedHint(t, path+project); got != want {
				t.Errorf("%s%s: hint %q, want the fixed %q", path, project, got, want)
			}
		}
	}
	for _, leak := range []string{"Injected", "script", "passwd", "sh op", "acme/shop/extra", "GitHub"} {
		if strings.Contains(want, leak) {
			t.Errorf("hint echoes %q: %q", leak, want)
		}
	}
}

// The spec carries the text the API sends, so the reference docs show a
// caller what it will read, and the spec no longer points at the route.
func TestMalformedProjectHintMatchesAnExampleInTheSpec(t *testing.T) {
	spec, err := contract.SpecJSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatal(err)
	}
	hint := malformedHint(t, malformedPaths[0]+"acme/shop")
	var found bool
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if x == hint {
				found = true
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)
	if !found {
		t.Errorf("no example in the spec equals the malformed-project hint %q", hint)
	}
}
