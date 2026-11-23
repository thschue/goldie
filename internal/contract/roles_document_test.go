package contract

import (
	"fmt"
	"strings"
	"testing"
	"time"
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

// A misspelt key (another suffix, another case) would be ignored and leave the
// path or value open, so any key matching x-goldie- case-insensitively that is
// not exactly x-goldie-role fails the parse, naming the key and the location.
func TestParseRolesRejectsAMisspeltRoleKey(t *testing.T) {
	for _, key := range []string{"x-goldie-roles", "X-Goldie-Role", "x-goldie-Role", "X-GOLDIE-ROLE", "x-goldie-rol", "x-goldie-"} {
		for name, c := range map[string]struct{ spec, where string }{
			"on an operation":          {"paths:\n  /a:\n    get:\n      " + key + ": operator\n", "/a"},
			"at path-item level":       {"paths:\n  /a:\n    " + key + ": operator\n    get:\n      x-goldie-role: operator\n", "/a"},
			"on a component parameter": {"paths:\n  /a:\n    get:\n      x-goldie-role: operator\ncomponents:\n  parameters:\n    Drafts:\n      name: drafts\n      in: query\n      " + key + ": operator\n", "Drafts"},
		} {
			t.Run(key+" "+name, func(t *testing.T) {
				got, err := parseOperatorPaths([]byte(c.spec))
				if err == nil {
					t.Fatalf("a misspelt %q was accepted, got %v", key, got)
				}
				if !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), c.where) {
					t.Errorf("error %q does not name the key %q and the location %q", err, key, c.where)
				}
				if p, err := parseOperatorParameters([]byte(c.spec)); err == nil {
					t.Errorf("parseOperatorParameters accepted the misspelt key, got %v", p)
				}
			})
		}
	}
}

// Nested anchors that each alias the previous one ten times expand to 10^9
// nodes from a few lines. The walk bound fires and the parse fails, quickly.
func TestParseRolesFailsOnAnAliasBomb(t *testing.T) {
	const fanout, levels = 10, 8 // a0 holds fanout leaves; each later anchor aliases the previous one fanout times
	nodes := 1
	for i := 0; i <= levels; i++ {
		nodes *= fanout
	}
	var b strings.Builder
	b.WriteString("paths:\n  /a:\n    get:\n      x-goldie-role: operator\nx-bomb:\n  - &a0 [0, 0, 0, 0, 0, 0, 0, 0, 0, 0]\n")
	for i := 1; i <= levels; i++ {
		fmt.Fprintf(&b, "  - &a%d [%s]\n", i, strings.TrimSuffix(strings.Repeat(fmt.Sprintf("*a%d, ", i-1), fanout), ", "))
	}
	done := make(chan error, 1)
	go func() {
		_, err := parseOperatorPaths([]byte(b.String()))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("an alias bomb of %d (10^9) nodes was accepted: the walk must stop at maxWalkSteps and fail", nodes)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the walk of an alias bomb did not stop within 5s: maxWalkSteps must bound it")
	}
}

// goldie's own header X-Goldie-Client and a schema property named like an
// extension are names, not extension keys: they stay legal as KEYS of a
// headers or properties mapping. These cases fail until the narrowed rejection
// in roles.go lands.
func TestParseRolesAcceptsXGoldieNamesAsHeaderAndPropertyKeys(t *testing.T) {
	const op = "paths:\n  /a:\n    get:\n      x-goldie-role: operator\n"
	for name, tail := range map[string]string{
		"header name in a response": "components:\n  responses:\n    R:\n      description: d\n      headers:\n        X-Goldie-Client:\n          schema:\n            type: string\n",
		"schema property name":      "components:\n  schemas:\n    S:\n      type: object\n      properties:\n        x-goldie-roles:\n          type: string\n",
	} {
		t.Run(name, func(t *testing.T) {
			spec := op + tail
			got, err := parseOperatorPaths([]byte(spec))
			if err != nil || !got["/a"] || len(got) != 1 {
				t.Errorf("paths: got %v, %v, want only /a", got, err)
			}
			if p, err := parseOperatorParameters([]byte(spec)); err != nil || len(p) != 0 {
				t.Errorf("parameters: got %v, %v, want an empty set", p, err)
			}
		})
	}
}

// The narrowing must not open a hole: a misspelt key next to those mappings, a
// role mark in the VALUES under them, and a misspelt key at the root still
// fail. Only the root case needs the roles.go change (the (root) location);
// the others must keep failing.
func TestParseRolesStillRejectsWhatTheNarrowingMustNotOpen(t *testing.T) {
	const op = "paths:\n  /a:\n    get:\n      x-goldie-role: operator\n"
	for name, c := range map[string]struct{ tail, key, where string }{
		"misspelt key next to headers":  {"components:\n  responses:\n    R:\n      description: d\n      x-goldie-roles: operator\n      headers:\n        X-Goldie-Client:\n          schema:\n            type: string\n", "x-goldie-roles", "R"},
		"mark inside a header object":   {"components:\n  responses:\n    R:\n      description: d\n      headers:\n        X-Goldie-Client:\n          x-goldie-role: operator\n          schema:\n            type: string\n", "x-goldie-role", "X-Goldie-Client"},
		"mark inside a property schema": {"components:\n  schemas:\n    S:\n      type: object\n      properties:\n        p:\n          x-goldie-role: operator\n", "x-goldie-role", "p"},
		"misspelt key at the root":      {"x-goldie-roles: operator\n", "x-goldie-roles", "(root)"},
	} {
		t.Run(name, func(t *testing.T) {
			spec := op + c.tail
			got, err := parseOperatorPaths([]byte(spec))
			if err == nil {
				t.Fatalf("accepted, got %v", got)
			}
			if !strings.Contains(err.Error(), c.key) || !strings.Contains(err.Error(), c.where) {
				t.Errorf("error %q does not name the key %q and the location %q", err, c.key, c.where)
			}
			if p, err := parseOperatorParameters([]byte(spec)); err == nil {
				t.Errorf("parseOperatorParameters accepted it, got %v", p)
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
