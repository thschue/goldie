package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #420: an ADR records x-goldie-role and is listed in the ADR index. It states
// what the mark means, where it may sit, the fail-closed rule for the whole
// document, and the known limitations that stay deliberate fail-closed
// defaults. The test pins neither the Status (the ADR is accepted later) nor
// uniqueness (a later ADR, for example the auth work, may cite the extension):
// it finds the recording ADR by its stable marker, the first file in sorted
// order that mentions the extension, has a Decision section and is titled
// about roles.
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
		if file == "" && recordsExtension(string(b)) {
			file, text = e.Name(), string(b)
		}
	}
	if file == "" {
		t.Fatalf("no ADR in %s mentions %s in a Decision section under a title about roles: the ADR recording it is missing", dir, RoleExtension)
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

// recordsExtension reports whether an ADR is the one recording the extension:
// its "# " title line mentions roles, it has a Decision section, and it
// mentions the extension.
func recordsExtension(text string) bool {
	var title string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "# ") {
			title = l
			break
		}
	}
	return strings.Contains(strings.ToLower(title), "role") &&
		strings.Contains(text, "\n## Decision") &&
		strings.Contains(text, RoleExtension)
}
