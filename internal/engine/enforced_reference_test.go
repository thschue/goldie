package engine_test

import (
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #376, found while writing its acceptance test: a Project draft never
// shadows an enforced global Item, drafts asked for or not (#359). That holds
// for the references a load checks too. A replaced_by or a lookup line in a
// Project article that names an enforced Item resolves to the enforced global
// copy even when the Project holds a draft of the same name, so the load
// succeeds. When the global copy is not enforced, the Project's draft still
// answers a replaced_by and the load fails ("project item sees the Project's
// draft before a global published Item" in governance_test.go).

// enforcedWithProjectDraft is an enforced global Item, the shop Project's
// draft of the same name, and the referring file at name.
func enforcedWithProjectDraft(enforced []string, name, content string) map[string]string {
	return map[string]string{
		globalFile("rules"):  govArticle("Rules", enforced...),
		projectFile("rules"): govArticle("Shop rules", "status: draft"),
		name:                 content,
	}
}

// enforcedFronts are the ways a global Item enforces: published, explicitly
// or by default, and deprecated.
var enforcedFronts = map[string][]string{
	"published by default": {"enforced: true"},
	"explicit published":   {"enforced: true", "status: published"},
	"deprecated":           {"enforced: true", "status: deprecated"},
}

// assertEnforcedCopyAnswers checks that the shop Project's view of rules is
// the enforced global copy, with and without drafts.
func assertEnforcedCopyAnswers(t *testing.T, snap *engine.Snapshot) {
	t.Helper()
	for _, o := range []engine.ReadOptions{{}, {Drafts: true}} {
		a, ok := snap.ArticleIn(govProject, "rules", o)
		if !ok || a.Scope != engine.ScopeGlobal || a.Name != "Rules" {
			t.Errorf("ArticleIn(%s, rules, %+v) = %+v, %v; want the enforced global copy", govProject, o, a, ok)
		}
	}
}

func TestReplacedByAnEnforcedItemOverAProjectDraftLoads(t *testing.T) {
	for name, front := range enforcedFronts {
		for _, ref := range []string{"article:rules", "article:RULES"} {
			t.Run(name+"/"+ref, func(t *testing.T) {
				snap := load(t, enforcedWithProjectDraft(front, projectFile("old"),
					govArticle("Old", "status: deprecated", "replaced_by: "+ref)))
				old, ok := snap.ArticleIn(govProject, "old")
				if !ok || old.ReplacedBy != ref {
					t.Fatalf("ArticleIn(%s, old) = %+v, %v; want ReplacedBy %q", govProject, old, ok, ref)
				}
				assertEnforcedCopyAnswers(t, snap)
			})
		}
	}
}

func TestLookupOfAnEnforcedItemOverAProjectDraftLoads(t *testing.T) {
	for name, front := range enforcedFronts {
		for _, id := range []string{"rules", "RULES"} {
			t.Run(name+"/"+id, func(t *testing.T) {
				snap := load(t, enforcedWithProjectDraft(front, projectFile("guide"),
					docWith("Guide", fence("goldie lookup "+id))))
				assertEnforcedCopyAnswers(t, snap)
			})
		}
	}
}

// A Project that holds a published copy of an enforced global Item and also
// suppresses it fails the load either way. The suppress reference resolves in
// the Project's view, where the enforced global copy answers (#359), so the
// error names the enforced Item's file, relative to the root.
func TestSuppressingAnEnforcedItemTheProjectAlsoCopiesNamesTheEnforcedItem(t *testing.T) {
	for name, front := range enforcedFronts {
		t.Run(name, func(t *testing.T) {
			loadErr(t, map[string]string{
				globalFile("rules"):  govArticle("Rules", front...),
				projectFile("rules"): article("Shop rules"),
				supFile:              suppressYAML("article:rules"),
			}, globalFile("rules"))
		})
	}
}
