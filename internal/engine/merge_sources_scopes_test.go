package engine_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #478, ADR 0030: the Scope pairs of a SourceSet other than global, and
// the third conflict, a narrower-Scope Item over another Source's enforced
// wider-Scope Item.

const enforcedFront = "enforced: true"

func TestProjectItemInOneSourceOverAnEnforcedGlobalItemInAnotherIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name         string
		global, proj string // the Source that holds the global and the Project Item
	}{
		{"later Source holds the Project Item", mergeA, mergeB},
		{"earlier Source holds the Project Item", mergeB, mergeA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]map[string]string{
				mergeA: {globalFile("kept-a"): article("Kept A")},
				mergeB: {globalFile("kept-b"): article("Kept B")},
			}
			files[tc.global][globalFile("rules")] = govArticle("Enforced rules", enforcedFront)
			set, roots := mergeSetRoots(t, files)
			pollBoth(t, set)
			writeTo(t, roots[tc.proj], projectFile("rules"), article("Project rules"))
			if err := set.Poll(t.Context(), tc.proj); err == nil {
				t.Fatal("Poll succeeded though a Project Item shadows another Source's enforced global Item")
			}
			assertNamesBoth(t, set.Status(tc.proj).LastError)
			if got := nameIn(set, govProject, "rules"); got != "Enforced rules" {
				t.Errorf("rules in the Project view = %q, want the enforced global Item", got)
			}
			if _, ok := set.Merged().Project(govProject); ok {
				t.Error("the rejected Snapshot registered a Project")
			}
		})
	}
}

// mergeSetRoots is mergeSet over the given files per Source name.
func mergeSetRoots(t *testing.T, files map[string]map[string]string) (*engine.SourceSet, map[string]string) {
	t.Helper()
	roots := map[string]string{mergeA: writeFiles(t, files[mergeA]), mergeB: writeFiles(t, files[mergeB])}
	return engine.NewSourceSet(
		engine.NamedSource{Name: mergeA, Source: engine.NewDirSource(roots[mergeA])},
		engine.NamedSource{Name: mergeB, Source: engine.NewDirSource(roots[mergeB])},
	), roots
}

func TestSameProjectItemResolvesToTheEarlierSource(t *testing.T) {
	set, _, _ := mergeSet(t,
		map[string]string{projectFile("style"): article("Project style from A")},
		map[string]string{projectFile("style"): article("Project style from B")},
	)
	pollBoth(t, set)
	if got := nameIn(set, govProject, "style"); got != "Project style from A" {
		t.Errorf("Project view = %q, want the earlier Source's", got)
	}
	want := []engine.Shadowed{{Item: "article:style", Scope: govProject, By: mergeA}}
	if got := set.Status(mergeB).Shadowed; !slices.Equal(got, want) {
		t.Errorf("team-b Shadowed = %v, want %v", got, want)
	}
}

// localSet is a SourceSet whose Sources each add a local Scope from their own
// .goldie directory.
func localSet(t *testing.T, a, b map[string]string) (*engine.SourceSet, map[string]string) {
	t.Helper()
	roots := map[string]string{mergeA: writeFiles(t, a), mergeB: writeFiles(t, b)}
	src := func(n string) engine.Source {
		return engine.NewLocalSource(engine.NewDirSource(roots[n]), filepath.Join(roots[n], ".goldie"))
	}
	return engine.NewSourceSet(
		engine.NamedSource{Name: mergeA, Source: src(mergeA)},
		engine.NamedSource{Name: mergeB, Source: src(mergeB)},
	), roots
}

func TestLocalScopePairsFollowSourcePreferenceAndSuppressionsApplyAcrossSources(t *testing.T) {
	set, _ := localSet(t,
		map[string]string{
			".goldie/articles/tip.md": article("Tip from A"),
			".goldie/config.yaml":     suppressYAML("article:style"),
		},
		map[string]string{
			".goldie/articles/tip.md": article("Tip from B"),
			globalFile("style"):       article("Style from B"),
		},
	)
	pollBoth(t, set)
	if got := nameIn(set, "", "tip"); got != "Tip from A" {
		t.Errorf("local tip = %q, want the earlier Source's", got)
	}
	want := []engine.Shadowed{{Item: "article:tip", Scope: engine.ScopeLocal, By: mergeA}}
	if got := set.Status(mergeB).Shadowed; !slices.Equal(got, want) {
		t.Errorf("team-b Shadowed = %v, want %v", got, want)
	}
	if got := nameIn(set, "", "style"); got != "" {
		t.Errorf("style = %q; team-a's local Suppression hides team-b's global Item", got)
	}
	for _, n := range []string{mergeA, mergeB} {
		if w := set.Status(n).Warnings; len(w) != 0 {
			t.Errorf("%s Warnings = %v, want none", n, w)
		}
	}
}

func TestLocalItemOverAnotherSourcesEnforcedItemIsRejected(t *testing.T) {
	set, roots := localSet(t,
		map[string]string{globalFile("kept"): article("Kept")},
		map[string]string{globalFile("rules"): govArticle("Enforced rules", enforcedFront)},
	)
	pollBoth(t, set)
	writeTo(t, roots[mergeA], ".goldie/articles/rules.md", article("Local rules"))
	if err := set.Poll(t.Context(), mergeA); err == nil {
		t.Fatal("Poll succeeded though a local Item shadows another Source's enforced Item")
	}
	assertNamesBoth(t, set.Status(mergeA).LastError)
	if got := nameIn(set, "", "rules"); got != "Enforced rules" {
		t.Errorf("rules = %q, want the enforced Item", got)
	}
}
