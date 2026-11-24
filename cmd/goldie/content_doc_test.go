package main

import (
	"os"
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
