package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The apply errors for a damaged or edited block name the file, and the
// edited-block error names the next command (#483).
func TestCLIInstructionsApplyErrorsNameTheFile(t *testing.T) {
	api := newInstructionsAPI(t, instrBlockV1)
	root := gitRepo(t, instrOrigin)
	path := filepath.Join(root, "CLAUDE.md")

	damaged := instrNotes + "<!-- goldie:begin digest=sha256:" + strings.Repeat("a", 64) + " -->\nno end marker\n"
	writeFile(t, path, damaged)
	if _, err := runInstr(t, api, root, "apply"); err == nil || !strings.Contains(err.Error(), "CLAUDE.md") {
		t.Errorf("damaged file: err = %v, want one that names CLAUDE.md", err)
	}

	writeFile(t, path, instrNotes)
	if out, err := runInstr(t, api, root, "apply"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	writeFile(t, path, strings.Replace(fileContent(t, path), "goldie_search", "other", 1))
	api.set(instrBlockV2)
	_, err := runInstr(t, api, root, "apply")
	if err == nil || !strings.Contains(err.Error(), "CLAUDE.md") || !strings.Contains(err.Error(), "goldie instructions apply --target claude --force") {
		t.Errorf("edited block: err = %v, want one that names CLAUDE.md and the next command", err)
	}
}
