package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The block is untrusted Source content: bidi and zero-width characters never
// reach the file.
func TestCLIInstructionsApplyStripsHiddenCharacters(t *testing.T) {
	bidi, zeroWidth := string(rune(0x202e)), string(rune(0x200b))
	api := newInstructionsAPI(t, "Use goldie"+bidi+" first.\nSee"+zeroWidth+" goldie_search.\n")
	root := gitRepo(t, instrOrigin)
	if out, err := runInstr(t, api, root, "apply", "--target", "claude"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	got := fileContent(t, filepath.Join(root, "CLAUDE.md"))
	if strings.Contains(got, bidi) || strings.Contains(got, zeroWidth) {
		t.Errorf("hidden characters were written:\n%q", got)
	}
	if !strings.Contains(got, "goldie_search") {
		t.Errorf("block missing:\n%q", got)
	}
}

// check on an edited block names --force, which apply needs then.
func TestCLIInstructionsCheckOnAnEditedBlockNamesForce(t *testing.T) {
	api := newInstructionsAPI(t, instrBlockV1)
	root := gitRepo(t, instrOrigin)
	path := filepath.Join(root, "CLAUDE.md")
	if out, err := runInstr(t, api, root, "apply", "--target", "claude"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	writeFile(t, path, strings.Replace(fileContent(t, path), "See goldie_search.", "Skip goldie.", 1))
	_, err := runInstr(t, api, root, "check", "--target", "claude")
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("check on an edited block: err = %v, want the hint to name --force", err)
	}
}
