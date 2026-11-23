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
		"at path-item level":           "paths:\n  /a:\n    x-goldie-role: operator\n    get: {}\n",
		"under parameters":             "paths:\n  /a:\n    parameters:\n      x-goldie-role: operator\n    get: {}\n",
		"under a parameter":            "paths:\n  /a:\n    parameters:\n      - x-goldie-role: operator\n    get: {}\n",
		"under summary":                "paths:\n  /a:\n    summary:\n      x-goldie-role: operator\n    get: {}\n",
		"misspelt, at path-item level": "paths:\n  /a:\n    get: {}\n    x-goldie-role: operater\n",
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
