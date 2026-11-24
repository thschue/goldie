package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #801: goldie server --dev offers to set up the repository it runs in, but
// only when stdin and stderr are terminals. These tests run the real binary,
// whose stderr is a pipe: stdout stays the export line alone, and nothing is
// offered, asked or written. The offer itself is tested at the cli boundary.

// offerRepo is a git repository whose origin names acme/widgets.
func offerRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = git@github.com:acme/widgets.git\n"
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// 1 and 2. Stdin is a character device (/dev/null) but stderr is not a
// terminal: no offer, nothing written, stdout is the export line alone.
func TestServerDevOffersNothingWithoutATerminal(t *testing.T) {
	repo := offerRepo(t)
	p := startDevIn(t, repo, devEnv(), contentDir(t))
	if got := devStatus(t, p.addr, devArticle, p.token); got != http.StatusOK {
		t.Errorf("GET with the printed token = %d, want 200", got)
	}
	time.Sleep(500 * time.Millisecond) // longer than a prompt takes to show
	p.stop(t)
	out, errs := p.lines()
	if len(out) != 1 || !devExportRE.MatchString(out[0]) {
		t.Errorf("stdout = %q, want exactly the export line\n%s", out, p.dump())
	}
	for _, l := range errs {
		if strings.Contains(l, "Set up this repository") {
			t.Errorf("the offer was printed without a terminal: %q", l)
		}
	}
	if _, err := os.Lstat(filepath.Join(repo, ".goldie")); !os.IsNotExist(err) {
		t.Errorf(".goldie was written: %v", err)
	}
	assertDevLogsOnStderr(t, p)
	assertTokenOnlyInExportLine(t, p)
}

// 6. The working directory is the served content directory and holds .goldie
// (what the init writes): the server starts, serves its content, and the
// config is not content.
func TestServerDevServesAWorkingDirectoryThatHoldsGoldie(t *testing.T) {
	repo := offerRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".goldie"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".goldie", "config.yaml"), []byte("project: github.com/acme/widgets\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	article := "---\nname: Ship it\nkeywords: [ship]\n---\nShip it.\n"
	if err := os.MkdirAll(filepath.Join(repo, "global", "articles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "global", "articles", "ship.md"), []byte(article), 0o644); err != nil {
		t.Fatal(err)
	}
	p := startDevIn(t, repo, devEnv())
	if got := devStatus(t, p.addr, "/api/v1/articles/ship", p.token); got != http.StatusOK {
		t.Errorf("GET /api/v1/articles/ship = %d, want 200\n%s", got, p.dump())
	}
	if got := devStatus(t, p.addr, "/api/v1/articles/config", p.token); got != http.StatusNotFound {
		t.Errorf("GET /api/v1/articles/config = %d, want 404: .goldie is not content", got)
	}
	p.stop(t)
}
