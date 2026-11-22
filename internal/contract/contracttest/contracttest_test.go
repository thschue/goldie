package contracttest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

func response(method, path string, status int, contentType, body string) (*http.Response, []byte) {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", contentType)
	rec.WriteHeader(status)
	resp := rec.Result()
	resp.Request = req
	return resp, []byte(body)
}

const goodError = `{"error":{"code":"not_found","message":"no such endpoint","details":{"hint":"Try again."}}}`

func TestCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		method, path string
		status       int
		contentType  string
		body         string
		wantErr      string // empty means the response conforms
	}{
		"declared yaml response":        {"GET", "/openapi.yaml", 200, "application/yaml", "openapi: 3.1.0", ""},
		"declared json response":        {"GET", "/openapi.json", 200, "application/json", `{"openapi":"3.1.0"}`, ""},
		"default error on an operation": {"POST", "/openapi.json", 405, "application/json", goodError, ""},
		"unknown path is an error":      {"GET", "/nope", 404, "application/json", goodError, ""},
		"unknown path answered 200":     {"GET", "/nope", 200, "application/json", `{}`, "not a client error"},
		"wrong content type":            {"GET", "/openapi.json", 200, "text/plain", `{}`, "not declared"},
		"error without details hint": {"GET", "/nope", 404, "application/json",
			`{"error":{"code":"x","message":"y","details":{}}}`, "hint"},
		"error with extra details": {"GET", "/nope", 404, "application/json",
			`{"error":{"code":"x","message":"y","details":{"hint":"z","addr":"10.0.0.1"}}}`, "addr"},
		"error that is not the envelope": {"GET", "/nope", 404, "application/json", `{"message":"x"}`, "error"},
		"body that is not JSON":          {"GET", "/openapi.json", 200, "application/json", `nope`, "not JSON"},
		// A path parameter is one segment: a "/" inside it is sent as %2F.
		"encoded slash in a path parameter": {"GET", "/api/v1/articles/platform%2Fdeploy", 200, "application/json",
			`{"id":"platform/deploy","name":"Deploy","keywords":[],"content":""}`, ""},
		"bare slash is not a path parameter": {"GET", "/api/v1/articles/platform/deploy", 200, "application/json",
			`{"id":"platform/deploy","name":"Deploy","keywords":[],"content":""}`, "not a client error"},
	} {
		t.Run(name, func(t *testing.T) {
			resp, body := response(tc.method, tc.path, tc.status, tc.contentType, tc.body)
			err := Check(resp, body)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("unexpected mismatch: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("conformed, want a mismatch mentioning %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestSpecDecodesFreshDocuments(t *testing.T) {
	a, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a["paths"].(map[string]any); !ok {
		t.Fatalf("spec has no paths object: %v", a["paths"])
	}
	delete(a, "paths")
	b, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b["paths"]; !ok {
		t.Error("Spec shares state between calls; want a fresh document each time")
	}
}

// Overlapping templates must resolve to the most specific one on every run,
// not in Go's random map order.
func TestFindOperationPrefersLiteralSegments(t *testing.T) {
	op := func(id string) map[string]any { return map[string]any{"operationId": id} }
	doc := map[string]any{"paths": map[string]any{
		"/items/{id}":         map[string]any{"get": op("byID"), "delete": op("del"), "post": op("postByID")},
		"/items/search":       map[string]any{"get": op("search")},
		"/items/{id}/{name}":  map[string]any{"get": op("idName")},
		"/items/{id}/details": map[string]any{"get": op("details")},
		"/{kind}/search":      map[string]any{"get": op("kindSearch"), "post": op("postKind")},
	}}
	for name, tc := range map[string]struct {
		method, path string
		wantID       string // empty means no operation
		wantPtr      string
	}{
		"literal beats template":              {"get", "/items/search", "search", "#/paths/~1items~1search/get"},
		"template when no literal matches":    {"get", "/items/42", "byID", "#/paths/~1items~1{id}/get"},
		"literal in a later segment wins":     {"get", "/items/42/details", "details", "#/paths/~1items~1{id}~1details/get"},
		"earlier literal segment wins":        {"post", "/items/search", "postByID", "#/paths/~1items~1{id}/post"},
		"template in a later segment":         {"get", "/items/42/other", "idName", "#/paths/~1items~1{id}~1{name}/get"},
		"most specific path lacks the method": {"delete", "/items/search", "del", "#/paths/~1items~1{id}/delete"},
		"no matching path offers the method":  {"put", "/items/search", "", ""},
		"no match":                            {"get", "/nope", "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			for range 50 {
				got, ptr := findOperation(doc, tc.method, tc.path)
				id, _ := got["operationId"].(string)
				if id != tc.wantID || ptr != tc.wantPtr {
					t.Fatalf("got %q at %q, want %q at %q", id, ptr, tc.wantID, tc.wantPtr)
				}
			}
		})
	}
}

// Spec only decodes: building the schema compiler is for Check, which validates
// responses. Decoding the raw document is the floor; Spec must stay near it.
func TestSpecOnlyDecodes(t *testing.T) {
	raw, err := contract.SpecJSON()
	if err != nil {
		t.Fatal(err)
	}
	decode := testing.AllocsPerRun(5, func() {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
	})
	spec := testing.AllocsPerRun(5, func() {
		if _, err := Spec(); err != nil {
			t.Fatal(err)
		}
	})
	if limit := decode*1.1 + 50; spec > limit {
		t.Errorf("Spec allocates %.0f times, decoding alone %.0f: it does more than decode", spec, decode)
	}
}

func TestExamplePathSubstitutesExamplesFromTheSpec(t *testing.T) {
	for _, method := range []string{"get", "GET"} {
		got, err := ExamplePath(method, "/api/v1/articles/{id}")
		if err != nil || got != "/api/v1/articles/platform%2Fdeploy" {
			t.Errorf("ExamplePath(%q) = %q, %v; want /api/v1/articles/platform%%2Fdeploy", method, got, err)
		}
	}
	if got, err := ExamplePath("get", "/api/v1/search"); err != nil || got != "/api/v1/search" {
		t.Errorf("an untemplated path = %q, %v; want it unchanged", got, err)
	}
	if got, err := ExamplePath("get", "/no/{such}/operation"); err == nil {
		t.Errorf("a template the spec has no operation for gave %q, want an error", got)
	}
}

func TestExamplePathUsesPathAndOperationParameters(t *testing.T) {
	doc := map[string]any{"paths": map[string]any{
		"/a/{id}/b/{n}": map[string]any{
			"parameters": []any{
				map[string]any{"name": "n", "in": "path", "example": "7"},
				map[string]any{"name": "id", "in": "path", "example": "from-path-item"},
			},
			"get": map[string]any{"parameters": []any{
				map[string]any{"name": "id", "in": "path", "example": "x/y z"},
			}},
			"post": map[string]any{},
		},
		"/c/{id}": map[string]any{
			"get": map[string]any{"parameters": []any{map[string]any{"name": "id", "in": "path"}}},
		},
		"/d/{id}/{other}": map[string]any{
			"get": map[string]any{"parameters": []any{map[string]any{"name": "id", "in": "path", "example": "1"}}},
		},
	}}
	for name, tc := range map[string]struct {
		method, tmpl, want string
	}{
		"path-level and operation-level, operation wins, encoded as one segment": {"GET", "/a/{id}/b/{n}", "/a/x%2Fy%20z/b/7"},
		"path-level parameter only": {"post", "/a/{id}/b/{n}", "/a/from-path-item/b/7"},
	} {
		got, err := examplePath(doc, tc.method, tc.tmpl)
		if err != nil || got != tc.want {
			t.Errorf("%s: examplePath = %q, %v; want %q", name, got, err, tc.want)
		}
	}
	for name, tc := range map[string]struct{ tmpl, param string }{
		"a parameter without an example":    {"/c/{id}", "id"},
		"a template parameter not declared": {"/d/{id}/{other}", "other"},
	} {
		got, err := examplePath(doc, "get", tc.tmpl)
		if err == nil || !strings.Contains(err.Error(), tc.param) {
			t.Errorf("%s: got %q, %v; want an error naming %q", name, got, err, tc.param)
		}
	}
}
