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
				t.Errorf("%s%s: hint %q has no example Project id such as github.com/acme/shop", path, project, hint)
			}
			if !strings.Contains(hint, "github.com/acme/shop") {
				t.Errorf("%s%s: hint %q, want the example github.com/acme/shop", path, project, hint)
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

// The exact text of the malformed-project hint: one canonical example Project
// id, a plain statement of what leaving project out does, and no route.
const malformedHintText = "Pass project as host/org/repo, for example github.com/acme/shop. Leave it out to get global guidance only."

func TestMalformedProjectHintIsTheExactText(t *testing.T) {
	for _, path := range malformedPaths {
		if got := malformedHint(t, path+"acme/shop"); got != malformedHintText {
			t.Errorf("%s: hint %q, want %q", path, got, malformedHintText)
		}
	}
}

// Leaving project out gets the global result on both routes (a search, an
// article lookup), so the hint says nothing route-specific about it and uses
// the one phrase the unregistered-Project hint uses too.
func TestMalformedProjectHintIsTrueOnBothRoutesAndUsesTheSharedPhrase(t *testing.T) {
	if !strings.Contains(malformedHintText, "global guidance only") {
		t.Errorf("hint %q lacks the phrase \"global guidance only\"", malformedHintText)
	}
	for _, path := range malformedPaths {
		hint := malformedHint(t, path+"acme/shop")
		for _, stale := range []string{"search", "global content", "global answer"} {
			if strings.Contains(hint, stale) {
				t.Errorf("%s: hint %q contains %q, which is wrong on the article route or off the shared phrase", path, hint, stale)
			}
		}
		if !strings.Contains(hint, "global guidance only") {
			t.Errorf("%s: hint %q lacks \"global guidance only\"", path, hint)
		}
	}
}

// specExamples returns the examples of components.schemas.<schema>.properties.<property>
// in the served spec, or of the schema itself when property is empty.
func specExamples(t *testing.T, schema, property string) []any {
	t.Helper()
	spec, err := contract.SpecJSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Examples   []any `json:"examples"`
				Properties map[string]struct {
					Examples []any `json:"examples"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatal(err)
	}
	s, ok := doc.Components.Schemas[schema]
	if !ok {
		t.Fatalf("the spec has no schema %q", schema)
	}
	if property == "" {
		return s.Examples
	}
	return s.Properties[property].Examples
}

// The spec carries the text the API sends, so the reference docs show a
// caller what it will read: the hint equals an example of
// components.schemas.ErrorDetails.properties.hint, and names no route.
func TestMalformedProjectHintMatchesAnExampleInTheSpec(t *testing.T) {
	hint := malformedHint(t, malformedPaths[0]+"acme/shop")
	if strings.Contains(hint, "/api/") {
		t.Errorf("hint %q contains an /api/ route", hint)
	}
	examples := specExamples(t, "ErrorDetails", "hint")
	var found bool
	for _, e := range examples {
		if e == any(hint) {
			found = true
		}
	}
	if !found {
		t.Errorf("ErrorDetails.hint examples %q hold no example equal to the malformed-project hint %q", examples, hint)
	}
}

// One canonical example Project id is used everywhere the spec shows one.
func TestSpecUsesOneCanonicalExampleProjectID(t *testing.T) {
	spec, err := contract.SpecJSON()
	if err != nil {
		t.Fatal(err)
	}
	const id = "github.com/acme/shop"
	if ex := specExamples(t, "ProjectId", ""); len(ex) == 0 || ex[0] != any(id) {
		t.Errorf("ProjectId examples = %v, want %q first", ex, id)
	}
	if raw, _ := json.Marshal(specExamples(t, "ProjectList", "projects")); !strings.Contains(string(raw), id) {
		t.Errorf("ProjectList examples do not use %q: %s", id, raw)
	}
	if strings.Contains(string(spec), "github.com/acme/payments") {
		t.Error("the spec still uses the example id github.com/acme/payments")
	}
	var hintExample bool
	for _, e := range specExamples(t, "ErrorDetails", "hint") {
		if s, _ := e.(string); strings.Contains(s, "host/org/repo") {
			hintExample = true
			if !strings.Contains(s, id) {
				t.Errorf("the malformed-project hint example %q does not use %q", s, id)
			}
		}
	}
	if !hintExample {
		t.Error("ErrorDetails.hint has no malformed-project example")
	}
}
