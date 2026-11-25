package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #681: docs/content.md shows the smallest article that loads. This runs the
// file the doc shows through goldie validate and goldie search, so the guide
// cannot drift from the loader.
func TestFirstArticleGuideLoadsAndIsFound(t *testing.T) {
	doc, err := os.ReadFile("../../docs/content.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "global/articles/") {
		t.Error("docs/content.md does not give the article path")
	}
	_, after, ok := strings.Cut(string(doc), "```markdown\n")
	if !ok {
		t.Fatal("docs/content.md has no markdown block with the minimal article")
	}
	article, _, ok := strings.Cut(after, "```")
	if !ok {
		t.Fatal("the minimal article block in docs/content.md is not closed")
	}

	dir := t.TempDir()
	writeArticle(t, dir, "global/articles/deploy.md", article)
	if stdout, stderr, code := runValidate(t, dir, validateEnv(), "validate", dir); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("validate exit %d, want 0 and no output\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	stdout, stderr, code := runValidate(t, dir, validateEnv(), "search", "deploy", "--dir", dir)
	if code != 0 || !strings.Contains(stdout, "deploy") {
		t.Fatalf("search exit %d, want 0 naming the article\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "docs/content.md") {
		t.Error("README.md does not link docs/content.md")
	}
}

// #726: docs/content.md documents the optional updated key with an example, and
// the example loads through goldie validate.
func TestContentGuideDocumentsTheUpdatedKeyWithALoadingExample(t *testing.T) {
	doc, err := os.ReadFile("../../docs/content.md")
	if err != nil {
		t.Fatal(err)
	}
	var example string
	for _, block := range strings.Split(string(doc), "```")[1:] {
		if strings.Contains(block, "\nupdated: 20") && strings.Contains(block, "---\n") {
			_, example, _ = strings.Cut(block, "\n")
			break
		}
	}
	if example == "" {
		t.Fatal("docs/content.md has no example with an updated: YYYY-MM-DD frontmatter key")
	}
	dir := t.TempDir()
	writeArticle(t, dir, "global/articles/example.md", example)
	if stdout, stderr, code := runValidate(t, dir, validateEnv(), "validate", dir); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("the updated example fails validate: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

// #726: an ADR records that the author sets updated, that it is optional and a
// day, and why git history and a Snapshot diff were rejected.
func TestAnADRRecordsTheUpdatedDayDecision(t *testing.T) {
	files, err := filepath.Glob("../../docs/adr/*updated*.md")
	if err != nil || len(files) != 1 {
		t.Fatalf("want exactly one ADR with updated in its file name, got %v (%v)", files, err)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(b))
	for _, want := range []string{"`updated`", "optional", "day", "git history", "snapshot diff"} {
		if !strings.Contains(text, want) {
			t.Errorf("%s does not mention %q", files[0], want)
		}
	}
}
