package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #451: goldie server -dev on a directory with no content serves the example
// content embedded in the binary (the same set as examples/knowledge, whose
// article agent-loops is devArticle) and says so once on stderr, naming the
// directory it would read. A directory with content is served as it is: the
// examples are never mixed in.

// devExamplesLines are the stderr lines that mention the built-in examples.
func devExamplesLines(p *devProc) []string {
	_, errs := p.lines()
	var got []string
	for _, l := range errs {
		if strings.Contains(strings.ToLower(l), "example") {
			got = append(got, l)
		}
	}
	return got
}

func TestServerDevOnAnEmptyDirectoryServesTheBuiltInExamples(t *testing.T) {
	p := startDev(t, devEnv(), t.TempDir())
	if got := devStatus(t, p.addr, devArticle, p.token); got != http.StatusOK {
		t.Errorf("GET %s on an empty directory = %d, want 200 from the built-in examples\n%s", devArticle, got, p.dump())
	}
	p.stop(t)
	assertTokenOnlyInExportLine(t, p)
}

func TestServerDevOnAnEmptyDirectorySaysSoOnceOnStderrNamingTheDirectory(t *testing.T) {
	dir := t.TempDir()
	p := startDev(t, devEnv(), dir)
	devStatus(t, p.addr, devArticle, p.token)
	p.stop(t)
	lines := devExamplesLines(p)
	if len(lines) != 1 {
		t.Fatalf("stderr has %d lines about the built-in examples, want exactly 1: %q\n%s", len(lines), lines, p.dump())
	}
	if !strings.Contains(lines[0], dir) {
		t.Errorf("the examples line does not name the directory %q: %q", dir, lines[0])
	}
	out, _ := p.lines()
	for _, l := range out {
		if strings.Contains(strings.ToLower(l), "example") {
			t.Errorf("the examples line is on stdout, which carries only the export line: %q", l)
		}
	}
}

func TestServerDevOnADirectoryWithContentServesOnlyThatContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "global", "articles"), 0o755); err != nil {
		t.Fatal(err)
	}
	article := "---\nname: My own article\nkeywords: [mine]\n---\nOnly here.\n"
	if err := os.WriteFile(filepath.Join(dir, "global", "articles", "mine.md"), []byte(article), 0o644); err != nil {
		t.Fatal(err)
	}
	p := startDev(t, devEnv(), dir)
	if got := devStatus(t, p.addr, "/api/v1/articles/mine", p.token); got != http.StatusOK {
		t.Errorf("GET the directory's own article = %d, want 200\n%s", got, p.dump())
	}
	if got := devStatus(t, p.addr, devArticle, p.token); got != http.StatusNotFound {
		t.Errorf("GET %s = %d, want 404: the examples must not be mixed into a directory with content", devArticle, got)
	}
	p.stop(t)
	if lines := devExamplesLines(p); len(lines) != 0 {
		t.Errorf("stderr mentions the built-in examples for a directory with content: %q", lines)
	}
}
