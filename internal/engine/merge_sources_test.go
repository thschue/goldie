package engine_test

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #478: several Sources form one view. Scope comes first (local,
// project, global); within a Scope the first listed Source wins and the loser
// is recorded as shadowed. An enforced Item that would lose on preference, or
// a Suppression naming an enforced Item in another Source, rejects the
// Snapshot that just loaded (ADR 0004).
//
// The seam is a SourceSet over named Sources in preference order. Each Source
// is polled by name and keeps its own last good Snapshot and SyncStatus;
// Merged is the view over every Source's current Snapshot. SyncStatus.Shadowed
// lists, for the Source that lost, each Item it lost and the Source that won it.

const (
	mergeA = "team-a"
	mergeB = "team-b"
)

// mergeSet builds a SourceSet over two directories, team-a listed first, and
// returns their roots.
func mergeSet(t *testing.T, a, b map[string]string) (set *engine.SourceSet, rootA, rootB string) {
	t.Helper()
	rootA, rootB = writeFiles(t, a), writeFiles(t, b)
	set = engine.NewSourceSet(
		engine.NamedSource{Name: mergeA, Source: engine.NewDirSource(rootA)},
		engine.NamedSource{Name: mergeB, Source: engine.NewDirSource(rootB)},
	)
	return set, rootA, rootB
}

func pollBoth(t *testing.T, set *engine.SourceSet) {
	t.Helper()
	for _, n := range []string{mergeA, mergeB} {
		if err := set.Poll(t.Context(), n); err != nil {
			t.Fatalf("Poll %s: %v", n, err)
		}
	}
}

// nameIn is the name of the Item that answers in proj's view, or "".
func nameIn(set *engine.SourceSet, proj, id string) string {
	a, ok := set.Merged().ArticleIn(proj, id)
	if !ok {
		return ""
	}
	return a.Name
}

func assertNamesBoth(t *testing.T, msg string) {
	t.Helper()
	if !strings.Contains(msg, mergeA) || !strings.Contains(msg, mergeB) {
		t.Errorf("last_error %q must name both Sources, %s and %s", msg, mergeA, mergeB)
	}
}

func TestSameScopeItemResolvesToTheEarlierSourceAndRecordsTheLoserAsShadowed(t *testing.T) {
	set, _, _ := mergeSet(t,
		map[string]string{globalFile("style"): article("Style from A")},
		map[string]string{globalFile("style"): article("Style from B"), globalFile("only-b"): article("Only B")},
	)
	pollBoth(t, set)
	if got := nameIn(set, "", "style"); got != "Style from A" {
		t.Errorf("style resolves to %q, want the earlier Source's", got)
	}
	if got := nameIn(set, "", "only-b"); got != "Only B" {
		t.Errorf("only-b = %q; a later Source still contributes what nobody else holds", got)
	}
	want := []engine.Shadowed{{Item: "article:style", Scope: engine.ScopeGlobal, By: mergeA}}
	if got := set.Status(mergeB).Shadowed; !slices.Equal(got, want) {
		t.Errorf("team-b Shadowed = %v, want %v", got, want)
	}
	if got := set.Status(mergeA).Shadowed; len(got) != 0 {
		t.Errorf("team-a won and shadows nothing of its own, Shadowed = %v", got)
	}
}

func TestProjectItemFromALaterSourceBeatsAGlobalItemFromAnEarlierOne(t *testing.T) {
	set, _, _ := mergeSet(t,
		map[string]string{globalFile("style"): article("Global style from A")},
		map[string]string{projectFile("style"): article("Project style from B")},
	)
	pollBoth(t, set)
	if got := nameIn(set, govProject, "style"); got != "Project style from B" {
		t.Errorf("project view = %q, want the Project Item from the later Source", got)
	}
	if got := nameIn(set, "", "style"); got != "Global style from A" {
		t.Errorf("global view = %q, want the global Item", got)
	}
	for _, n := range []string{mergeA, mergeB} {
		if got := set.Status(n).Shadowed; len(got) != 0 {
			t.Errorf("%s Shadowed = %v; Scope order is not Source preference", n, got)
		}
	}
}

func TestDraftItemNeitherShadowsNorIsShadowedAcrossSources(t *testing.T) {
	// A's draft does not shadow B's published copy.
	set, _, _ := mergeSet(t,
		map[string]string{globalFile("style"): govArticle("Draft from A", "status: draft")},
		map[string]string{globalFile("style"): article("Published from B")},
	)
	pollBoth(t, set)
	if got := nameIn(set, "", "style"); got != "Published from B" {
		t.Errorf("style = %q, want the later Source's published copy", got)
	}
	// B's draft is not shadowed by A's published copy.
	set, _, _ = mergeSet(t,
		map[string]string{globalFile("style"): article("Published from A")},
		map[string]string{globalFile("style"): govArticle("Draft from B", "status: draft")},
	)
	pollBoth(t, set)
	if got := nameIn(set, "", "style"); got != "Published from A" {
		t.Errorf("style = %q, want the earlier Source's published copy", got)
	}
	for _, n := range []string{mergeA, mergeB} {
		if got := set.Status(n).Shadowed; len(got) != 0 {
			t.Errorf("%s Shadowed = %v; a draft is not shadowed", n, got)
		}
	}
}

func TestEnforcedItemLosingOnPreferenceRejectsTheSnapshotThatJustLoaded(t *testing.T) {
	set, _, rootB := mergeSet(t,
		map[string]string{globalFile("rules"): article("Rules from A")},
		map[string]string{globalFile("kept"): article("Kept")},
	)
	pollBoth(t, set)
	// B now publishes an enforced Item that team-a's copy would shadow.
	writeTo(t, rootB, globalFile("rules"), govArticle("Enforced rules from B", "enforced: true"))
	writeTo(t, rootB, globalFile("late"), article("Late"))
	err := set.Poll(t.Context(), mergeB)
	if err == nil {
		t.Fatal("Poll succeeded though an enforced Item of team-b loses to team-a")
	}
	assertNamesBoth(t, set.Status(mergeB).LastError)
	assertNamesBoth(t, err.Error())
	if got := nameIn(set, "", "rules"); got != "Rules from A" {
		t.Errorf("rules = %q; the rejected Snapshot must not enter the view", got)
	}
	if got := nameIn(set, "", "kept"); got != "Kept" {
		t.Errorf("kept = %q; team-b keeps its last good Snapshot", got)
	}
	if got := nameIn(set, "", "late"); got != "" {
		t.Errorf("late = %q; it came with the rejected Snapshot", got)
	}
}

func TestEarlierSourceLoadingAnItemOverAnEnforcedOneIsTheRejectedSnapshot(t *testing.T) {
	set, rootA, _ := mergeSet(t,
		map[string]string{globalFile("kept"): article("Kept")},
		map[string]string{globalFile("rules"): govArticle("Enforced rules from B", "enforced: true")},
	)
	pollBoth(t, set)
	// A, listed first, now holds a copy that would win over B's enforced one.
	writeTo(t, rootA, globalFile("rules"), article("Rules from A"))
	if err := set.Poll(t.Context(), mergeA); err == nil {
		t.Fatal("Poll succeeded though team-a's Item would shadow an enforced Item of team-b")
	}
	assertNamesBoth(t, set.Status(mergeA).LastError)
	if got := nameIn(set, "", "rules"); got != "Enforced rules from B" {
		t.Errorf("rules = %q; the enforced Item keeps answering", got)
	}
	if got := nameIn(set, "", "kept"); got != "Kept" {
		t.Errorf("kept = %q; team-a keeps its last good Snapshot", got)
	}
}

func TestSuppressionNamingAnEnforcedItemInAnotherSourceRejectsTheSnapshot(t *testing.T) {
	set, rootA, _ := mergeSet(t,
		map[string]string{globalFile("kept"): article("Kept")},
		map[string]string{globalFile("rules"): govArticle("Enforced rules", "enforced: true")},
	)
	pollBoth(t, set)
	writeTo(t, rootA, "projects/"+govProject+"/project.yaml", suppressYAML("article:rules"))
	if err := set.Poll(t.Context(), mergeA); err == nil {
		t.Fatal("Poll succeeded though team-a suppresses an enforced Item of team-b")
	}
	assertNamesBoth(t, set.Status(mergeA).LastError)
	if got := nameIn(set, govProject, "rules"); got != "Enforced rules" {
		t.Errorf("rules in the Project view = %q; the enforced Item is not hidden", got)
	}
	if _, ok := set.Merged().Project(govProject); ok {
		t.Error("the rejected Snapshot registered a Project")
	}
}

func TestSuppressionNamingAnItemInAnotherSourceAppliesWithoutAWarning(t *testing.T) {
	set, _, _ := mergeSet(t,
		map[string]string{
			"projects/" + govProject + "/project.yaml": suppressYAML("article:style"),
		},
		map[string]string{globalFile("style"): article("Style from B")},
	)
	pollBoth(t, set)
	if got := nameIn(set, govProject, "style"); got != "" {
		t.Errorf("Project view = %q; team-a's Suppression hides team-b's global Item", got)
	}
	if got := nameIn(set, "", "style"); got != "Style from B" {
		t.Errorf("global view = %q; the Suppression is per Project", got)
	}
	for _, n := range []string{mergeA, mergeB} {
		if w := set.Status(n).Warnings; len(w) != 0 {
			t.Errorf("%s Warnings = %v; the reference names an Item in another Source", n, w)
		}
	}
}

// Two Sources change in the same tick, each to a Snapshot that is fine against
// the other's old one and in conflict with its new one: both publish an
// enforced rules Item. The check and the swap share one lock, so exactly one
// Poll is rejected and the merged view never holds an enforced Item that lost.
func TestTwoSourcesChangingInTheSameTickCannotBothBeAccepted(t *testing.T) {
	for i := range 25 {
		set, rootA, rootB := mergeSet(t,
			map[string]string{globalFile("kept-a"): article("Kept A")},
			map[string]string{globalFile("kept-b"): article("Kept B")},
		)
		pollBoth(t, set)
		writeTo(t, rootA, globalFile("rules"), govArticle("Rules from A", "enforced: true"))
		writeTo(t, rootB, globalFile("rules"), govArticle("Rules from B", "enforced: true"))

		errs := map[string]error{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, n := range []string{mergeA, mergeB} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				err := set.Poll(t.Context(), n)
				mu.Lock()
				errs[n] = err
				mu.Unlock()
			}()
		}
		close(start)
		wg.Wait()

		failed := 0
		for n, err := range errs {
			if err == nil {
				continue
			}
			failed++
			assertNamesBoth(t, set.Status(n).LastError)
		}
		if failed != 1 {
			t.Fatalf("round %d: %d of 2 Polls were rejected, want exactly 1 (a: %v, b: %v)", i, failed, errs[mergeA], errs[mergeB])
		}
		got := nameIn(set, "", "rules")
		if got != "Rules from A" && got != "Rules from B" {
			t.Fatalf("round %d: rules = %q, want the accepted Source's copy", i, got)
		}
		// The loser kept its last good Snapshot, so both Kept Items still answer.
		if nameIn(set, "", "kept-a") != "Kept A" || nameIn(set, "", "kept-b") != "Kept B" {
			t.Fatalf("round %d: a Source lost its last good Snapshot", i)
		}
	}
}
