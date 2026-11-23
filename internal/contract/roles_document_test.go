package contract

import (
	"strings"
	"testing"
)

// #420: the guard is fail-closed for the whole document. A mark in a component
// an operation reaches through $ref is not applied through the reference, so
// it must fail rather than leave the path open. The one mark allowed outside
// paths sits directly on a components/parameters entry.
//
// Seam assumed (does not exist yet): parseOperatorParameters(spec []byte)
// (map[string]bool, error) returns the components/parameters names whose
// object carries the mark, e.g. {"Drafts": true}, and fails on every mark
// that is not on an operation or directly on a component parameter;
// parseOperatorPaths accepts the component-parameter mark and rejects every
// other mark outside an operation.
const paramMarkSpec = "paths:\n  /a:\n    get:\n      x-goldie-role: operator\ncomponents:\n  parameters:\n    Drafts:\n      name: drafts\n      in: query\n      x-goldie-role: operator\n"

func TestParseOperatorPathsAcceptsAMarkOnAComponentParameter(t *testing.T) {
	got, err := parseOperatorPaths([]byte(paramMarkSpec))
	if err != nil || !got["/a"] || len(got) != 1 {
		t.Errorf("paths: got %v, %v", got, err)
	}
	params, err := parseOperatorParameters([]byte(paramMarkSpec))
	if err != nil || !params["Drafts"] || len(params) != 1 {
		t.Errorf("parameters: got %v, %v, want only Drafts", params, err)
	}
}

func TestParseOperatorParametersReportsNoneWhenNothingIsMarked(t *testing.T) {
	spec := "paths:\n  /a:\n    get:\n      x-goldie-role: operator\ncomponents:\n  parameters:\n    Plain:\n      name: p\n      in: query\n"
	params, err := parseOperatorParameters([]byte(spec))
	if err != nil || len(params) != 0 {
		t.Errorf("got %v, %v, want an empty set", params, err)
	}
}

func TestParseOperatorPathsRejectsAMarkOutsideAnOperationOrParameter(t *testing.T) {
	const op = "paths:\n  /a:\n    get:\n      x-goldie-role: operator\n"
	for name, tail := range map[string]string{
		"in components/responses":               "components:\n  responses:\n    X:\n      x-goldie-role: operator\n",
		"in components/requestBodies":           "components:\n  requestBodies:\n    X:\n      x-goldie-role: operator\n",
		"in components/schemas":                 "components:\n  schemas:\n    X:\n      x-goldie-role: operator\n",
		"in components/headers":                 "components:\n  headers:\n    X:\n      x-goldie-role: operator\n",
		"in components/pathItems":               "components:\n  pathItems:\n    X:\n      x-goldie-role: operator\n",
		"under a component parameter schema":    "components:\n  parameters:\n    X:\n      name: x\n      in: query\n      schema:\n        x-goldie-role: operator\n",
		"under a component parameter content":   "components:\n  parameters:\n    X:\n      name: x\n      in: query\n      content:\n        x-goldie-role: operator\n",
		"misspelt on a component parameter":     "components:\n  parameters:\n    X:\n      name: x\n      in: query\n      x-goldie-role: operater\n",
		"in components directly":                "components:\n  x-goldie-role: operator\n",
		"on components/parameters itself":       "components:\n  parameters:\n    x-goldie-role: operator\n",
		"at the document root":                  "x-goldie-role: operator\n",
		"in info":                               "info:\n  x-goldie-role: operator\n",
		"in webhooks":                           "webhooks:\n  w:\n    post:\n      x-goldie-role: operator\n",
		"in a security scheme":                  "components:\n  securitySchemes:\n    b:\n      x-goldie-role: operator\n",
		"in an extension list under components": "components:\n  x-foo:\n    - x-goldie-role: operator\n",
	} {
		t.Run(name, func(t *testing.T) {
			spec := op + tail
			got, err := parseOperatorPaths([]byte(spec))
			if err == nil {
				t.Fatalf("a misplaced %s was accepted, got %v", RoleExtension, got)
			}
			if !strings.Contains(err.Error(), RoleExtension) {
				t.Errorf("error %q does not name %s", err, RoleExtension)
			}
			if p, err := parseOperatorParameters([]byte(spec)); err == nil {
				t.Errorf("parseOperatorParameters accepted the misplaced mark, got %v", p)
			}
		})
	}
}

// A mark in a component an operation reaches through $ref (or an alias) is an
// error: the mark does not apply through the reference.
func TestParseOperatorPathsRejectsAMarkInAComponentAnOperationReferences(t *testing.T) {
	for name, spec := range map[string]string{
		"response":    "paths:\n  /a:\n    get:\n      responses:\n        '200':\n          $ref: '#/components/responses/X'\ncomponents:\n  responses:\n    X:\n      x-goldie-role: operator\n",
		"requestBody": "paths:\n  /a:\n    post:\n      requestBody:\n        $ref: '#/components/requestBodies/X'\ncomponents:\n  requestBodies:\n    X:\n      x-goldie-role: operator\n",
		"alias":       "components:\n  responses:\n    X: &x\n      x-goldie-role: operator\npaths:\n  /a:\n    get:\n      responses:\n        '200': *x\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseOperatorPaths([]byte(spec))
			if err == nil {
				t.Fatalf("accepted, got %v", got)
			}
			if len(got) != 0 {
				t.Errorf("an erroring parse yielded paths %v", got)
			}
		})
	}
}

func TestEmbeddedSpecMarksDraftsAsAnOperatorParameter(t *testing.T) {
	if _, err := OperatorPaths(); err != nil {
		t.Fatalf("the embedded spec fails the whole-document check: %v", err)
	}
	params, err := parseOperatorParameters(specYAML)
	if err != nil || !params["Drafts"] || len(params) != 1 {
		t.Errorf("embedded spec marks parameters %v, %v, want only Drafts", params, err)
	}
}
