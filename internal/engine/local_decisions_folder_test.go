package engine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #739: the local config.yaml maps a decisions folder,
// `decisions: {path: docs/adr}`, relative to the repository root. The engine
// loads the files in it that parse as decisions into the local Scope. A file
// that does not look like a decision at all (README.md, a template) is
// skipped without a warning; a malformed ADR is a warning that names the file
// (not its path) and does not stop the others from loading.

const folderMADR = "# Use PostgreSQL\n\n* Status: accepted\n* Date: 2024-03-05\n\n## Decision Outcome\n\nUse it.\n"

func loadDecisionsFolder(t *testing.T, files map[string]string) (*engine.Snapshot, string) {
	t.Helper()
	repo := writeFiles(t, files)
	base := writeFiles(t, map[string]string{})
	snap, err := engine.NewLocalSource(engine.NewDirSource(base), filepath.Join(repo, ".goldie")).Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return snap, repo
}

func TestDecisionsFolderLoadsTheValidADRsAndNamesTheMalformedOne(t *testing.T) {
	snap, repo := loadDecisionsFolder(t, map[string]string{
		".goldie/config.yaml":           "decisions:\n  path: docs/adr\n",
		"docs/adr/0002-use-postgres.md": folderMADR,
		"docs/adr/0003-use-queue.md":    "---\ntitle: Use a queue\nstatus: accepted\ndate: 2024-06-01\n---\nQueue.\n",
		"docs/adr/0004-broken.md":       "---\ntitle: Broken\nstatus: bogus\ndate: 2024-06-01\n---\nBroken.\n",
		"docs/adr/README.md":            "# Architecture decisions\n\nThe ADRs live here.\n",
		"docs/adr/0000-template.md":     "# [short title]\n\n* Status: {proposed | accepted}\n* Date: {YYYY-MM-DD}\n",
	})
	if got := decisionNames(snap.Decisions("")); got != "0002-use-postgres,0003-use-queue" {
		t.Errorf("Decisions = %s, want the two valid ADRs", got)
	}
	d, ok := snap.DecisionIn("", "0002-use-postgres")
	if !ok || d.Scope != engine.ScopeLocal || d.Title != "Use PostgreSQL" {
		t.Errorf("DecisionIn = %+v, %v, want the local MADR decision", d, ok)
	}
	var named bool
	for _, w := range snap.Warnings() {
		if strings.Contains(w, "0004-broken.md") {
			named = true
		}
		for _, skipped := range []string{"README.md", "0000-template.md"} {
			if strings.Contains(w, skipped) {
				t.Errorf("warning %q is about a skipped file", w)
			}
		}
		for _, p := range []string{repo, filepath.Dir(repo)} {
			if strings.Contains(w, p) {
				t.Errorf("warning %q leaks the host path %s", w, p)
			}
		}
	}
	if !named {
		t.Errorf("warnings %q do not name 0004-broken.md", snap.Warnings())
	}
}
