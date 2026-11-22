package contracttest

import (
	"strings"
	"testing"
)

func TestExamplePathRejectsAnEmptyExample(t *testing.T) {
	doc := map[string]any{"paths": map[string]any{
		"/c/{id}": map[string]any{
			"get": map[string]any{"parameters": []any{map[string]any{"name": "id", "in": "path", "example": ""}}},
		},
		"/e/{id}/{n}": map[string]any{
			"parameters": []any{map[string]any{"name": "n", "in": "path", "example": ""}},
			"get":        map[string]any{"parameters": []any{map[string]any{"name": "id", "in": "path", "example": "ok"}}},
		},
	}}
	for name, tc := range map[string]struct{ tmpl, param string }{
		"an empty example on the operation": {"/c/{id}", "id"},
		"an empty example on the path item": {"/e/{id}/{n}", "n"},
	} {
		got, err := examplePath(doc, "get", tc.tmpl)
		if err == nil || !strings.Contains(err.Error(), tc.param) {
			t.Errorf("%s: got %q, %v; want an error naming %q", name, got, err, tc.param)
		}
	}
}

// A backslash in a literal segment is part of the path, not regexp quoting.
func TestExamplePathKeepsBackslashesInLiteralSegments(t *testing.T) {
	doc := map[string]any{"paths": map[string]any{
		`/a\b/{id}`: map[string]any{
			"get": map[string]any{"parameters": []any{map[string]any{"name": "id", "in": "path", "example": "x"}}},
		},
		`/c\{d}`: map[string]any{
			"get": map[string]any{"parameters": []any{map[string]any{"name": "d", "in": "path", "example": "v"}}},
		},
		`/plain\`: map[string]any{"get": map[string]any{}},
	}}
	for tmpl, want := range map[string]string{
		`/a\b/{id}`: `/a\b/x`,
		`/c\{d}`:    `/c\v`,
		`/plain\`:   `/plain\`,
	} {
		got, err := examplePath(doc, "get", tmpl)
		if err != nil || got != want {
			t.Errorf("examplePath(%q) = %q, %v; want %q", tmpl, got, err, want)
		}
	}
}
