package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #420: the PR adds the ADR recording x-goldie-role (status proposed, next
// free number, listed in the ADR index). It states what the mark means, where
// it may sit, the fail-closed rule for the whole document, and the known
// limitations that stay deliberate fail-closed defaults.
func TestADRRecordsTheRoleExtension(t *testing.T) {
	const dir = "../../docs/adr"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var file, text string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "README.md" || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), RoleExtension) {
			if file != "" {
				t.Fatalf("both %s and %s mention %s: one ADR records it", file, e.Name(), RoleExtension)
			}
			file, text = e.Name(), string(b)
		}
	}
	if file == "" {
		t.Fatalf("no ADR in %s mentions %s: the PR must add the ADR recording it", dir, RoleExtension)
	}
	if !strings.Contains(text, "Status: proposed") {
		t.Errorf("ADR %s lacks the line \"Status: proposed\"", file)
	}
	lower := strings.ToLower(text)
	for _, want := range []string{
		"operator", "fail", "drafts", "components/parameters", "components/responses",
		"$ref", "merge key", "additionaloperations", "query", "startup",
	} {
		if !strings.Contains(lower, want) {
			t.Errorf("ADR %s does not state %q", file, want)
		}
	}
	index, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "("+file+")") {
		t.Errorf("docs/adr/README.md does not list %s", file)
	}
}
