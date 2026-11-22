package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectIDTable checks ValidProjectID against the shared table of
// accepted and rejected Project ids; the engine's layout guard reads the same
// table (internal/engine).
func TestProjectIDTable(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "project_ids.txt"))
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
		if got := ValidProjectID(id); got != (verdict == "ok") {
			t.Errorf("project_ids.txt:%d: ValidProjectID(%q) = %v, want %s", i+1, id, got, verdict)
		}
		n++
	}
	if n == 0 {
		t.Fatal("project_ids.txt has no entries")
	}
}

func TestValidProjectIDReadsTheLengthFromTheSpec(t *testing.T) {
	if projectID.maxLength != 512 {
		t.Fatalf("maxLength = %d, want the spec's 512", projectID.maxLength)
	}
	head := "github.com/acme/"
	if id := head + strings.Repeat("r", projectID.maxLength-len(head)); !ValidProjectID(id) {
		t.Errorf("a ProjectId of exactly maxLength bytes is rejected")
	}
	if id := head + strings.Repeat("r", projectID.maxLength-len(head)+1); ValidProjectID(id) {
		t.Errorf("a ProjectId longer than maxLength is accepted")
	}
}

func TestParseProjectIDSchemaNeedsPatternAndLength(t *testing.T) {
	for _, spec := range []string{
		"components: {schemas: {ProjectId: {maxLength: 5}}}",
		"components: {schemas: {ProjectId: {pattern: '^a$'}}}",
		"components: {schemas: {ProjectId: {pattern: '(', maxLength: 5}}}",
	} {
		if _, err := parseProjectIDSchema([]byte(spec)); err == nil {
			t.Errorf("parseProjectIDSchema(%q) succeeded", spec)
		}
	}
}
