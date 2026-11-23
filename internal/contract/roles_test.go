package contract

import "testing"

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
