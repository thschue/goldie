package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// Acceptance tests for #420, the HTTP seam. The spec marks a parameter with
// x-goldie-role: operator (components/parameters/Drafts for drafts), and every
// operation that takes a marked parameter must refuse a reader token that
// sends the restricted value, in the 403 forbidden envelope. The walk reads
// the served spec, so a future operation that takes drafts (or any marked
// parameter) but forgets readOptions fails here without anyone editing this
// test.

const (
	specReaderToken   = "reader-0123456789abcdef0123456789abcdef"
	specOperatorToken = "operator-0123456789abcdef0123456789abcdef"
)

// restrictedValue is the value of a marked parameter that only an operator may
// send. Only booleans exist today; any other schema fails the test so a new
// kind of marked parameter gets its restricted value stated here.
func restrictedValue(t *testing.T, name string, schema map[string]any) string {
	t.Helper()
	if schema["type"] != "boolean" {
		t.Fatalf("marked parameter %s has schema %v: this test only knows boolean parameters, add the restricted value for the new kind", name, schema)
	}
	return "true"
}

type specParam struct {
	Name     string
	In       string
	Required bool
	Marked   bool
	Schema   map[string]any
	Example  any
}

type specDoc struct {
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Parameters map[string]map[string]any `yaml:"parameters"`
	} `yaml:"components"`
}

func loadSpec(t *testing.T) *specDoc {
	t.Helper()
	var doc specDoc
	if err := yaml.Unmarshal(contract.SpecYAML(), &doc); err != nil {
		t.Fatal(err)
	}
	return &doc
}

// resolveParam turns a parameter entry, inline or a $ref into
// components/parameters, into a specParam.
func resolveParam(t *testing.T, doc *specDoc, raw any) specParam {
	t.Helper()
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("parameter entry %v is not a mapping", raw)
	}
	if ref, ok := m["$ref"].(string); ok {
		name, found := strings.CutPrefix(ref, "#/components/parameters/")
		if !found || doc.Components.Parameters[name] == nil {
			t.Fatalf("parameter $ref %q does not point into components/parameters", ref)
		}
		m = doc.Components.Parameters[name]
	}
	p := specParam{Example: m["example"]}
	p.Name, _ = m["name"].(string)
	p.In, _ = m["in"].(string)
	p.Required, _ = m["required"].(bool)
	p.Marked = m[contract.RoleExtension] != nil
	p.Schema, _ = m["schema"].(map[string]any)
	return p
}

func specHandler(t *testing.T) http.Handler {
	t.Helper()
	// No snapshot: a reader's restricted value must be refused before the
	// not-ready check (ADR 0014), and an operator's must get past the role.
	s, err := NewServer(func() *engine.Snapshot { return nil })
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := ParseTokens("r:reader:" + specReaderToken + "\no:operator:" + specOperatorToken)
	if err != nil {
		t.Fatal(err)
	}
	return tokens.Require(RoleReader, contract.Handler(contract.NewStrictHandler(s, nil)))
}

func TestSpecMarksTheDraftsParameterAsOperatorOnly(t *testing.T) {
	doc := loadSpec(t)
	p := doc.Components.Parameters["Drafts"]
	if p == nil {
		t.Fatal("components/parameters/Drafts does not exist: drafts must be one component parameter")
	}
	if p["name"] != "drafts" || p["in"] != "query" {
		t.Errorf("components/parameters/Drafts is %v, want the query parameter drafts", p)
	}
	if p[contract.RoleExtension] != "operator" {
		t.Errorf("components/parameters/Drafts carries %s %v, want operator", contract.RoleExtension, p[contract.RoleExtension])
	}
	// Search and lookup take it through the reference, not a copy.
	for _, path := range []string{"/api/v1/search", "/api/v1/articles/{id}"} {
		get, _ := doc.Paths[path]["get"].(map[string]any)
		var refs int
		for _, raw := range asList(get["parameters"]) {
			m, _ := raw.(map[string]any)
			if m["$ref"] == "#/components/parameters/Drafts" {
				refs++
			}
			if m["name"] == "drafts" {
				t.Errorf("GET %s declares drafts inline: it must reference components/parameters/Drafts", path)
			}
		}
		if refs != 1 {
			t.Errorf("GET %s references components/parameters/Drafts %d times, want once", path, refs)
		}
	}
}

func TestEveryOperationWithAMarkedParameterRefusesAReader(t *testing.T) {
	doc := loadSpec(t)
	h := specHandler(t)
	var driven []string
	for path, item := range doc.Paths {
		pathLevel := asList(item["parameters"])
		for method, rawOp := range item {
			op, ok := rawOp.(map[string]any)
			if !ok || method == "parameters" || method == "servers" {
				continue
			}
			params := append(append([]any{}, pathLevel...), asList(op["parameters"])...)
			var all, marked []specParam
			for _, raw := range params {
				p := resolveParam(t, doc, raw)
				all = append(all, p)
				if p.Marked {
					marked = append(marked, p)
				}
			}
			for _, m := range marked {
				if m.In != "query" {
					t.Fatalf("%s %s: marked parameter %s is in %s: this test drives query parameters only", method, path, m.Name, m.In)
				}
				value := restrictedValue(t, m.Name, m.Schema)
				driven = append(driven, strings.ToUpper(method)+" "+path+" ?"+m.Name)
				target := buildURL(t, path, all, m.Name, value)

				code, body := serve(h, method, target, specReaderToken)
				if code != http.StatusForbidden || !strings.Contains(body, `"forbidden"`) {
					t.Errorf("%s %s with a reader token answered %d %s, want 403 forbidden: the operation takes a parameter marked %s: operator but does not enforce it",
						strings.ToUpper(method), target, code, body, contract.RoleExtension)
				}
				if strings.Contains(body, specReaderToken) {
					t.Errorf("the 403 for %s echoes the token: %s", target, body)
				}
				// An operator is not refused by the role.
				code, body = serve(h, method, target, specOperatorToken)
				if code == http.StatusForbidden || code == http.StatusUnauthorized {
					t.Errorf("%s %s with an operator token answered %d %s, want it past the role check", strings.ToUpper(method), target, code, body)
				}
			}
		}
	}
	sort.Strings(driven)
	if len(driven) == 0 {
		t.Fatal("the spec marks no parameter with " + contract.RoleExtension + ": drafts must be marked, or this test checks nothing")
	}
	for _, w := range []string{"GET /api/v1/articles/{id} ?drafts", "GET /api/v1/search ?drafts"} {
		found := false
		for _, d := range driven {
			found = found || d == w
		}
		if !found {
			t.Errorf("the walk did not drive %q, driven: %v", w, driven)
		}
	}
}

// A reader may still send a marked parameter without the restricted value:
// false, or no parameter, is not refused by the role.
func TestAReaderMaySendAMarkedParameterWithoutTheRestrictedValue(t *testing.T) {
	h := specHandler(t)
	for _, target := range []string{"/api/v1/search?q=x&drafts=false", "/api/v1/search?q=x", "/api/v1/articles/x?drafts=false"} {
		if code, body := serve(h, http.MethodGet, target, specReaderToken); code == http.StatusForbidden {
			t.Errorf("GET %s with a reader token answered 403: %s", target, body)
		}
	}
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func serve(h http.Handler, method, target, token string) (int, string) {
	r := httptest.NewRequest(strings.ToUpper(method), target, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

// buildURL fills the path template and every required query parameter, then
// adds the restricted one.
func buildURL(t *testing.T, path string, params []specParam, restricted, value string) string {
	t.Helper()
	for strings.Contains(path, "{") {
		i, j := strings.Index(path, "{"), strings.Index(path, "}")
		if j < i {
			t.Fatalf("bad path template %s", path)
		}
		path = path[:i] + "x" + path[j+1:]
	}
	var q []string
	for _, p := range params {
		if p.In == "query" && p.Required && p.Name != restricted {
			v := "x"
			if p.Example != nil {
				v = fmt.Sprint(p.Example)
			}
			q = append(q, p.Name+"="+strings.ReplaceAll(v, " ", "+"))
		}
	}
	q = append(q, restricted+"="+value)
	return path + "?" + strings.Join(q, "&")
}
