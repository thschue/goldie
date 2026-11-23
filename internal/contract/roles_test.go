package contract

import (
	"strings"
	"testing"
)

func TestOperatorPathsComeFromTheSpec(t *testing.T) {
	got, err := OperatorPaths()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/v1/activity", "/api/v1/sync"} {
		if !got[p] {
			t.Errorf("%s is not an operator path", p)
		}
	}
	if got["/api/v1/search"] || len(got) != 2 {
		t.Errorf("operator paths = %v, want only activity and sync", got)
	}
}

func TestParseOperatorPathsRejectsAnUnknownRole(t *testing.T) {
	spec := "paths:\n  /a:\n    parameters: []\n    get:\n      x-goldie-role: operater\n"
	if _, err := parseOperatorPaths([]byte(spec)); err == nil {
		t.Error("a misspelt role was accepted")
	}
	spec = "paths:\n  /a:\n    get:\n      x-goldie-role: operator\n  /b:\n    get: {}\n"
	got, err := parseOperatorPaths([]byte(spec))
	if err != nil || !got["/a"] || got["/b"] {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestParseOperatorPathsRejectsAMisplacedRole(t *testing.T) {
	for name, spec := range map[string]string{
		"at path-item level":                    "paths:\n  /a:\n    x-goldie-role: operator\n    get: {}\n",
		"under parameters":                      "paths:\n  /a:\n    parameters:\n      x-goldie-role: operator\n    get: {}\n",
		"under a parameter":                     "paths:\n  /a:\n    parameters:\n      - x-goldie-role: operator\n    get: {}\n",
		"under summary":                         "paths:\n  /a:\n    summary:\n      x-goldie-role: operator\n    get: {}\n",
		"misspelt, at path-item level":          "paths:\n  /a:\n    get: {}\n    x-goldie-role: operater\n",
		"in a servers list":                     "paths:\n  /a:\n    servers:\n      - url: /x\n        x-goldie-role: operator\n    get: {}\n",
		"two levels deep under a key":           "paths:\n  /a:\n    x-foo:\n      bar:\n        - x-goldie-role: operator\n    get: {}\n",
		"under responses, beside a placed mark": "paths:\n  /a:\n    get:\n      x-goldie-role: operator\n      responses:\n        '200':\n          x-goldie-role: operator\n",
		"under responses only":                  "paths:\n  /a:\n    get:\n      responses:\n        '200':\n          x-goldie-role: operator\n",
		"under requestBody":                     "paths:\n  /a:\n    post:\n      requestBody:\n        content:\n          x-goldie-role: operator\n",
		"in a parameters list of an operation":  "paths:\n  /a:\n    get:\n      parameters:\n        - x-goldie-role: operator\n",
		"reached through an alias":              "paths:\n  /b:\n    get: &g\n      x-goldie-role: operator\n  /a:\n    x-foo: *g\n    get: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseOperatorPaths([]byte(spec))
			if err == nil {
				t.Fatalf("a misplaced %s was accepted, got %v", RoleExtension, got)
			}
			if !strings.Contains(err.Error(), "/a") || !strings.Contains(err.Error(), RoleExtension) {
				t.Errorf("error %q names neither the path /a nor %s", err, RoleExtension)
			}
		})
	}
}

func TestParseOperatorPathsStillAcceptsAPlacedRoleBesideOtherKeys(t *testing.T) {
	spec := "paths:\n  /a:\n    summary: s\n    parameters: []\n    post:\n      x-goldie-role: operator\n"
	got, err := parseOperatorPaths([]byte(spec))
	if err != nil || !got["/a"] || len(got) != 1 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestParseOperatorPathsRejectsAPathItemRef(t *testing.T) {
	spec := "paths:\n  /a:\n    $ref: '#/components/pathItems/X'\n"
	got, err := parseOperatorPaths([]byte(spec))
	if err == nil {
		t.Fatalf("a path-item $ref was accepted, got %v", got)
	}
	if !strings.Contains(err.Error(), "/a") || !strings.Contains(err.Error(), "$ref") {
		t.Errorf("error %q names neither the path /a nor $ref", err)
	}
}

func TestParseOperatorPathsYieldsNoPathForAMisplacedRole(t *testing.T) {
	spec := "paths:\n  /a:\n    get:\n      responses:\n        '200':\n          x-goldie-role: operator\n"
	got, _ := parseOperatorPaths([]byte(spec))
	if len(got) != 0 {
		t.Errorf("a misplaced mark yielded operator paths %v", got)
	}
}

func TestParseOperatorPathsAcceptsScalarsAndEmptyListsUnderOtherKeys(t *testing.T) {
	spec := "paths:\n  /a:\n    summary: s\n    description: d\n    parameters: []\n    servers: []\n    x-foo: 1\n    get: {}\n"
	got, err := parseOperatorPaths([]byte(spec))
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v, want an empty set", got, err)
	}
}

func TestParseOperatorPathsMarksOnlyTheMarkedOperationOfAPath(t *testing.T) {
	spec := "paths:\n  /a:\n    get: {}\n    post:\n      x-goldie-role: operator\n      responses:\n        '200':\n          description: ok\n"
	got, err := parseOperatorPaths([]byte(spec))
	if err != nil || !got["/a"] || len(got) != 1 {
		t.Errorf("got %v, %v", got, err)
	}
}
