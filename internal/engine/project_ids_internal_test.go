package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectIDTable holds the engine's layout guard to the contract's
// ProjectId: the engine cannot import the contract, so both read the same
// table of accepted and rejected ids (ADR 0020).
func TestProjectIDTable(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "contract", "testdata", "project_ids.txt"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i, line := range strings.Split(string(b), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		verdict, id, ok := strings.Cut(line, " ")
		if !ok || (verdict != "ok" && verdict != "bad") {
			t.Fatalf("project_ids.txt:%d: want \"ok <id>\" or \"bad <id>\", got %q", i+1, line)
		}
		if got := validProjectLayout(id); got != (verdict == "ok") {
			t.Errorf("project_ids.txt:%d: engine accepts %q = %v, want %s", i+1, id, got, verdict)
		}
		n++
	}
	if n == 0 {
		t.Fatal("project_ids.txt has no entries")
	}
}

// validProjectLayout applies validProjectSegment to each of exactly three
// segments, as loading projects/<host>/<org>/<repo>/ does.
func validProjectLayout(id string) bool {
	segs := strings.Split(id, "/")
	if len(segs) != 3 {
		return false
	}
	for depth, s := range segs {
		if validProjectSegment(depth, s) != nil {
			return false
		}
	}
	return true
}
