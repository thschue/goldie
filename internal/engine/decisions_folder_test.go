package engine_test

import (
	"strings"
	"testing"
)

// The decisions folder's warnings come from file names, which are untrusted,
// and `path: .` reads the repository root itself.

func TestDecisionsFolderWarningEscapesAHostileFileName(t *testing.T) {
	name := "0004: evil\n\x1b[31m\u202e" + strings.Repeat("x", 200) + ".md"
	snap, _ := loadDecisionsFolder(t, map[string]string{
		".goldie/config.yaml":           "decisions:\n  path: docs/adr\n",
		"docs/adr/0002-use-postgres.md": folderMADR,
		"docs/adr/" + name:              "---\ntitle: Broken\nstatus: bogus\ndate: 2024-06-01\n---\nBroken.\n",
	})
	ws := snap.Warnings()
	if len(ws) != 1 {
		t.Fatalf("warnings = %q, want one", ws)
	}
	w := ws[0]
	for _, r := range w {
		if r < 0x20 || r > 0x7e {
			t.Fatalf("warning %q holds a non-printable or non-ASCII rune", w)
		}
	}
	if len(w) > 1024 {
		t.Errorf("warning is %d bytes, want at most 1 KiB", len(w))
	}
	file, _, _ := strings.Cut(w, ": ")
	if !strings.HasPrefix(file, `docs/adr/0004:\x20evil\n`) {
		t.Errorf("the file part of %q does not name the escaped file", w)
	}
}

func TestDecisionsFolderOfTheRepositoryRootLoadsItsADRs(t *testing.T) {
	snap, _ := loadDecisionsFolder(t, map[string]string{
		".goldie/config.yaml":  "decisions:\n  path: .\n",
		"0002-use-postgres.md": folderMADR,
		"README.md":            "# Notes\n\nNothing here.\n",
	})
	if got := decisionNames(snap.Decisions("")); got != "0002-use-postgres" {
		t.Errorf("Decisions = %q, want 0002-use-postgres", got)
	}
	if w := snap.Warnings(); len(w) != 0 {
		t.Errorf("warnings = %q, want none", w)
	}
}

func TestDecisionsFolderWithOnlyNonDecisionsWarnsOnce(t *testing.T) {
	snap, _ := loadDecisionsFolder(t, map[string]string{
		".goldie/config.yaml": "decisions:\n  path: docs/adr\n",
		"docs/adr/README.md":  "# ADRs\n",
		"docs/adr/notes.md":   "# Notes\n\nNo status here.\n",
	})
	ws := snap.Warnings()
	if len(ws) != 1 || !strings.Contains(ws[0], "found 1 files but none read as a decision") || !strings.Contains(ws[0], "docs/adr") {
		t.Errorf("warnings = %q, want one that says none read as a decision", ws)
	}
}

func TestDecisionsFolderWithOnlyReadmeAndTemplateIsQuiet(t *testing.T) {
	snap, _ := loadDecisionsFolder(t, map[string]string{
		".goldie/config.yaml":       "decisions:\n  path: docs/adr\n",
		"docs/adr/README.md":        "# ADRs\n",
		"docs/adr/0000-template.md": "# [short title]\n\n* Status: {proposed | accepted}\n* Date: {YYYY-MM-DD}\n",
	})
	if w := snap.Warnings(); len(w) != 0 {
		t.Errorf("warnings = %q, want none", w)
	}
}
