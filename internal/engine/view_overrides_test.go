package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #493: the engine reports which Scopes an Item overrides, so the UI can
// say "Overrides article:name from global". The seam is Snapshot.View: each
// winning Article carries Overrides, the Scopes of the lower-priority copies
// with the same identity (case-insensitive, ADR 0015), nearest Scope first in
// Resolution order (local, Project, global). An Item that shadows nothing has
// an empty Overrides. An enforced Item reports Enforced as Resolution does: it
// is the winner wherever it applies, so it overrides nothing, and a Scope it
// shadows is not reported when Resolution does not let it answer.

// viewItem returns the Article that View(proj) returns for id.
func viewItem(t *testing.T, snap *engine.Snapshot, proj, id string) engine.Article {
	t.Helper()
	for _, a := range snap.View(proj) {
		if strings.EqualFold(a.ID, id) {
			return a
		}
	}
	t.Fatalf("View(%q) has no %q: %+v", proj, id, snap.View(proj))
	return engine.Article{}
}

func wantOverrides(t *testing.T, a engine.Article, want ...string) {
	t.Helper()
	got := a.Overrides
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s (scope %s) Overrides = %q, want %q", a.ID, a.Scope, got, want)
	}
}

func TestViewReportsTheGlobalScopeAProjectItemOverrides(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("db"):  article("Global db"),
		projectFile("db"): article("Shop db"),
	})
	a := viewItem(t, snap, govProject, "db")
	if a.Scope != govProject {
		t.Fatalf("winner scope = %q, want the Project", a.Scope)
	}
	wantOverrides(t, a, engine.ScopeGlobal)
}

func TestViewReportsEveryLowerScopeForOneIdentityInThreeScopes(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalFile("style"):  article("Global style"),
		projectFile("STYLE"): article("Project style"),
		localFile("Style"):   article("Local style"),
	})
	a := viewItem(t, snap, govProject, "style")
	if a.Scope != engine.ScopeLocal {
		t.Fatalf("winner scope = %q, want local", a.Scope)
	}
	wantOverrides(t, a, govProject, engine.ScopeGlobal)

	// In a view without that Project the local copy shadows only global.
	wantOverrides(t, viewItem(t, snap, "", "style"), engine.ScopeGlobal)
	wantOverrides(t, viewItem(t, snap, otherGovProject, "style"), engine.ScopeGlobal)
}

func TestViewReportsOnlyTheScopesThatHoldTheIdentity(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalFile("gl"):  article("Global gl"),
		projectFile("pl"): article("Project pl"),
		localFile("gl"):   article("Local gl"),
		localFile("pl"):   article("Local pl"),
	})
	wantOverrides(t, viewItem(t, snap, govProject, "gl"), engine.ScopeGlobal)
	wantOverrides(t, viewItem(t, snap, govProject, "pl"), govProject)
}

func TestViewItemThatShadowsNothingReportsAnEmptyList(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalFile("only-global"):   article("Global only"),
		projectFile("only-project"): article("Project only"),
		localFile("only-local"):     article("Local only"),
		globalFile("db"):            article("Global db"),
		projectFile("db"):           article("Shop db"),
	})
	for _, id := range []string{"only-global", "only-project", "only-local"} {
		a := viewItem(t, snap, govProject, id)
		wantOverrides(t, a)
	}
	// In a view without the Project the global db is the winner and shadows
	// nothing: the Project's copy is not part of that view.
	wantOverrides(t, viewItem(t, snap, "", "db"))
}

func TestViewDoesNotReportAnotherProjectsCopyAsOverridden(t *testing.T) {
	snap := load(t, map[string]string{
		projectFile("db"):      article("Shop db"),
		otherProjectFile("db"): article("Other db"),
	})
	wantOverrides(t, viewItem(t, snap, govProject, "db"))
	wantOverrides(t, viewItem(t, snap, otherGovProject, "db"))
}

func TestViewEnforcedItemReportsStatusAsResolutionDoes(t *testing.T) {
	// An enforced Project Item over a plain global one: it wins, is enforced,
	// and overrides the global Scope.
	snap := load(t, map[string]string{
		globalFile("rules"):  article("Global rules"),
		projectFile("rules"): govArticle("Shop rules", "enforced: true"),
	})
	a := viewItem(t, snap, govProject, "rules")
	if !a.Enforced || a.Scope != govProject {
		t.Fatalf("winner = %+v, want the enforced Project copy", a)
	}
	wantOverrides(t, a, engine.ScopeGlobal)

	// An enforced global Item with a local draft: the draft never shadows it,
	// Resolution answers with the enforced copy, and it overrides nothing.
	snap = loadLocal(t, map[string]string{
		globalFile("rules"): govArticle("Global rules", "enforced: true"),
		localFile("rules"):  govArticle("Local rules", "status: draft"),
	})
	a = viewItem(t, snap, "", "rules")
	if !a.Enforced || a.Scope != engine.ScopeGlobal {
		t.Fatalf("winner = %+v, want the enforced global copy", a)
	}
	wantOverrides(t, a)
}

func TestViewDoesNotMaskAnEnforcedItemBeingShadowed(t *testing.T) {
	// Shadowing a published enforced Item is still a load error, so Overrides
	// never has an enforced lower copy to report.
	loadErr(t, map[string]string{
		globalFile("rules"):  govArticle("Rules", "enforced: true"),
		projectFile("rules"): article("Shop rules"),
	}, globalFile("rules"), projectFile("rules"))
}

func TestIdentitiesDifferingOnlyByCaseInOneScopeStillFailTheLoad(t *testing.T) {
	loadErr(t, map[string]string{
		globalFile("Rules"): article("One"),
		globalFile("rules"): article("Two"),
	}, globalFile("Rules"), globalFile("rules"))
	loadErr(t, map[string]string{
		projectFile("Rules"): article("One"),
		projectFile("rules"): article("Two"),
	}, projectFile("Rules"), projectFile("rules"))
	loadLocalErr(t, map[string]string{
		localFile("Rules"): article("One"),
		localFile("rules"): article("Two"),
	}, localFile("Rules"), localFile("rules"))
}

func TestViewOverridesIsACopy(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("db"):  article("Global db"),
		projectFile("db"): article("Shop db"),
	})
	a := viewItem(t, snap, govProject, "db")
	got := a.Overrides
	if len(got) == 0 {
		t.Fatal("Overrides is empty")
	}
	got[0] = "tampered"
	wantOverrides(t, viewItem(t, snap, govProject, "db"), engine.ScopeGlobal)
}
