package engine_test

import (
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// #713 (slice of #34): a decision is an Item Kind. decisions/<name>.md loads
// at every Scope, the default read serves accepted decisions only, and
// supersession is checked at load. The seam is the engine API.

const (
	globalDecisionPath  = "global/decisions/"
	projectDecisionPath = "projects/" + govProject + "/decisions/"
	localDecisionPath   = ".goldie/decisions/"
)

// decisionDoc is a decision file: title, then the frontmatter lines in front.
func decisionDoc(title string, front ...string) string {
	return "---\ntitle: " + title + "\n" + strings.Join(front, "\n") + "\n---\nWe decided.\n"
}

func acceptedDecision(title string, extra ...string) string {
	return decisionDoc(title, append([]string{"status: accepted", "date: 2023-05-02"}, extra...)...)
}

func proposedDecision(title string) string {
	return decisionDoc(title, "status: proposed", "date: 2023-05-02")
}

func supersededDecision(by string) string {
	return decisionDoc("T", "status: superseded", "date: 2023-05-02", "superseded_by: "+by)
}

func decisionNames(ds []engine.Decision) string {
	names := []string{}
	for _, d := range ds {
		names = append(names, d.Name)
	}
	return strings.Join(names, ",")
}

func TestDecisionLoadsAtEveryScopeByItsFileStem(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalDecisionPath + "g-one.md":      acceptedDecision("Global One"),
		projectDecisionPath + "p-one.md":     acceptedDecision("Project One"),
		localDecisionPath + "l-one.md":       acceptedDecision("Local One"),
		globalDecisionPath + "keyworded.md":  acceptedDecision("Keyworded", "keywords: [db, queue]", "supersedes: [decision:g-one]"),
		projectDecisionPath + "unrelated.md": acceptedDecision("Unrelated"),
	})
	for _, tc := range []struct{ name, scope, title string }{
		{"g-one", engine.ScopeGlobal, "Global One"},
		{"p-one", govProject, "Project One"},
		{"l-one", engine.ScopeLocal, "Local One"},
	} {
		d, ok := snap.DecisionIn(govProject, tc.name)
		if !ok || d.Scope != tc.scope || d.Title != tc.title || d.Name != tc.name || d.Status != "accepted" {
			t.Errorf("DecisionIn(%s) = %+v, %v; want scope %s, title %q, accepted", tc.name, d, ok, tc.scope, tc.title)
		}
	}
	if _, ok := snap.DecisionIn("", "p-one"); ok {
		t.Error("a Project decision is served in the global view")
	}
	if got := decisionNames(snap.Decisions(govProject)); got != "g-one,keyworded,l-one,p-one,unrelated" {
		t.Errorf("Decisions(project) = %s", got)
	}
}

func TestDecisionIdentityIsTheFileStemNotTheTitle(t *testing.T) {
	snap := load(t, map[string]string{
		globalDecisionPath + "use-postgres.md": acceptedDecision("Something else entirely"),
	})
	d, ok := snap.DecisionIn("", "use-postgres")
	if !ok || d.Name != "use-postgres" || d.Title != "Something else entirely" {
		t.Fatalf("DecisionIn(use-postgres) = %+v, %v", d, ok)
	}
	if d, ok := snap.DecisionIn("", "Something else entirely"); ok {
		t.Errorf("a decision is found by its title: %+v", d)
	}
}

func TestDecisionNamesDifferingOnlyInCaseFailTheLoad(t *testing.T) {
	loadErr(t, map[string]string{
		globalDecisionPath + "Dup.md": acceptedDecision("a"),
		globalDecisionPath + "dup.md": acceptedDecision("b"),
	}, "Dup.md", "dup.md")
}

func TestDecisionSubdirectoryFailsTheLoad(t *testing.T) {
	loadErr(t, map[string]string{
		globalDecisionPath + "sub/x.md": acceptedDecision("x"),
	}, "global/decisions/sub")
}

func TestDecisionDefaultReadServesAcceptedOnly(t *testing.T) {
	snap := load(t, map[string]string{
		globalDecisionPath + "yes.md":   acceptedDecision("Yes"),
		globalDecisionPath + "maybe.md": proposedDecision("Maybe"),
		globalDecisionPath + "old.md":   supersededDecision("decision:yes"),
	})
	if got := decisionNames(snap.Decisions("")); got != "yes" {
		t.Errorf("Decisions default = %s, want yes", got)
	}
	if got := decisionNames(snap.Decisions("", engine.ReadOptions{})); got != "yes" {
		t.Errorf("Decisions(ReadOptions{}) = %s, want yes", got)
	}
	for _, name := range []string{"maybe", "old"} {
		if d, ok := snap.DecisionIn("", name); ok {
			t.Errorf("DecisionIn(%s) in the default read = %+v, want none", name, d)
		}
	}
	if got := decisionNames(snap.Decisions("", engine.ReadOptions{Drafts: true})); got != "maybe,old,yes" {
		t.Errorf("Decisions(Drafts) = %s, want maybe,old,yes", got)
	}
	if d, ok := snap.DecisionIn("", "maybe", engine.ReadOptions{Drafts: true}); !ok || d.Status != "proposed" {
		t.Errorf("DecisionIn(maybe, Drafts) = %+v, %v", d, ok)
	}
}

func TestDecisionStatusIsRequiredAndKnown(t *testing.T) {
	cases := map[string]string{
		"missing":   decisionDoc("T", "date: 2023-05-02"),
		"unknown":   decisionDoc("T", "status: SECRETVALUE", "date: 2023-05-02"),
		"empty":     decisionDoc("T", "status:", "date: 2023-05-02"),
		"list":      decisionDoc("T", "status: [accepted]", "date: 2023-05-02"),
		"published": decisionDoc("T", "status: published", "date: 2023-05-02"),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{globalDecisionPath + "x.md": doc}, "global/decisions/x.md", "status")
			if strings.Contains(msg, "SECRETVALUE") {
				t.Errorf("error echoes the status value: %q", msg)
			}
		})
	}
}

func TestDecisionSupersessionIsChecked(t *testing.T) {
	t.Run("a chain of three loads", func(t *testing.T) {
		snap := load(t, map[string]string{
			globalDecisionPath + "a.md": supersededDecision("decision:b"),
			globalDecisionPath + "b.md": supersededDecision("decision:c"),
			globalDecisionPath + "c.md": acceptedDecision("C"),
		})
		if d, ok := snap.DecisionIn("", "a", engine.ReadOptions{Drafts: true}); !ok || d.Status != "superseded" {
			t.Errorf("DecisionIn(a) = %+v, %v", d, ok)
		}
		if got := decisionNames(snap.Decisions("")); got != "c" {
			t.Errorf("Decisions = %s, want c", got)
		}
	})
	t.Run("superseded needs superseded_by", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalDecisionPath + "a.md": decisionDoc("T", "status: superseded", "date: 2023-05-02"),
		}, "global/decisions/a.md", "superseded_by")
	})
	t.Run("superseded_by needs status superseded", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalDecisionPath + "a.md": acceptedDecision("A", "superseded_by: decision:b"),
			globalDecisionPath + "b.md": acceptedDecision("B"),
		}, "global/decisions/a.md", "superseded_by")
	})
	t.Run("dangling", func(t *testing.T) {
		loadErr(t, map[string]string{globalDecisionPath + "a.md": supersededDecision("decision:nope")}, "global/decisions/a.md")
	})
	t.Run("self", func(t *testing.T) {
		loadErr(t, map[string]string{globalDecisionPath + "a.md": supersededDecision("decision:a")}, "global/decisions/a.md")
	})
	t.Run("cycle", func(t *testing.T) {
		msg := loadErr(t, map[string]string{
			globalDecisionPath + "a.md": supersededDecision("decision:b"),
			globalDecisionPath + "b.md": supersededDecision("decision:a"),
		})
		if !strings.Contains(msg, "global/decisions/a.md") && !strings.Contains(msg, "global/decisions/b.md") {
			t.Errorf("error %q names neither file", msg)
		}
	})
	t.Run("a global decision cannot name a Project one", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalDecisionPath + "a.md":  supersededDecision("decision:p"),
			projectDecisionPath + "p.md": acceptedDecision("P"),
		}, "global/decisions/a.md")
	})
	t.Run("a Project decision may name a global one", func(t *testing.T) {
		load(t, map[string]string{
			projectDecisionPath + "a.md": supersededDecision("decision:g"),
			globalDecisionPath + "g.md":  acceptedDecision("G"),
		})
	})
}

func TestDecisionResolvesByScope(t *testing.T) {
	files := map[string]string{
		globalDecisionPath + "x.md":  acceptedDecision("Global"),
		projectDecisionPath + "x.md": acceptedDecision("Project"),
		localDecisionPath + "x.md":   acceptedDecision("Local"),
	}
	snap := loadLocal(t, files)
	if d, ok := snap.DecisionIn(govProject, "x"); !ok || d.Scope != engine.ScopeLocal || d.Title != "Local" {
		t.Errorf("with all three, DecisionIn = %+v, %v; want local", d, ok)
	}
	delete(files, localDecisionPath+"x.md")
	snap = loadLocal(t, files)
	if d, ok := snap.DecisionIn(govProject, "x"); !ok || d.Scope != govProject {
		t.Errorf("project over global: DecisionIn = %+v, %v", d, ok)
	}
	if d, ok := snap.DecisionIn("", "x"); !ok || d.Scope != engine.ScopeGlobal {
		t.Errorf("global view: DecisionIn = %+v, %v", d, ok)
	}
}

func TestProposedProjectDecisionDoesNotHideAnAcceptedGlobalOne(t *testing.T) {
	snap := load(t, map[string]string{
		globalDecisionPath + "x.md":  acceptedDecision("Global"),
		projectDecisionPath + "x.md": proposedDecision("Project"),
	})
	d, ok := snap.DecisionIn(govProject, "x")
	if !ok || d.Scope != engine.ScopeGlobal || d.Status != "accepted" {
		t.Errorf("DecisionIn = %+v, %v; want the accepted global decision", d, ok)
	}
	if got := decisionNames(snap.Decisions(govProject)); got != "x" {
		t.Errorf("Decisions = %s, want x", got)
	}
}

func TestEnforcedGlobalDecisionCannotBeShadowed(t *testing.T) {
	t.Run("project over global", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalDecisionPath + "x.md":  acceptedDecision("G", "enforced: true"),
			projectDecisionPath + "x.md": acceptedDecision("P"),
		}, "global/decisions/x.md", "projects/"+govProject+"/decisions/x.md")
	})
	t.Run("local over global", func(t *testing.T) {
		loadLocalErr(t, map[string]string{
			globalDecisionPath + "x.md": acceptedDecision("G", "enforced: true"),
			localDecisionPath + "x.md":  acceptedDecision("L"),
		}, "decision:x", localDecisionPath+"x.md")
	})
	t.Run("a proposed copy is no shadow", func(t *testing.T) {
		snap := load(t, map[string]string{
			globalDecisionPath + "x.md":  acceptedDecision("G", "enforced: true"),
			projectDecisionPath + "x.md": proposedDecision("P"),
		})
		if d, ok := snap.DecisionIn(govProject, "x"); !ok || d.Scope != engine.ScopeGlobal {
			t.Errorf("DecisionIn = %+v, %v; want the enforced global", d, ok)
		}
	})
}

func TestTeamSourceWithoutGlobalClaimCannotPublishAGlobalDecision(t *testing.T) {
	files := map[string]string{
		globalDecisionPath + "x.md":  acceptedDecision("G"),
		projectDecisionPath + "y.md": acceptedDecision("P"),
	}
	claimLoadErr(t, engine.Claim{Projects: []string{claimAcmeGlob}}, files, "global/decisions/x.md")
	snap := claimLoad(t, engine.Claim{Global: true, Projects: []string{claimAcmeGlob}}, files)
	if _, ok := snap.DecisionIn(govProject, "x"); !ok {
		t.Error("a claimed global decision is not served")
	}
}

func TestDecisionIsAKnownKindInReferences(t *testing.T) {
	old := "---\nname: Old\nstatus: deprecated\nreplaced_by: decision:new\n---\nBody\n"
	snap := load(t, map[string]string{
		globalDecisionPath + "new.md": acceptedDecision("New"),
		"global/articles/old.md":      old,
	})
	if a, ok := snap.ArticleIn("", "old", engine.ReadOptions{Drafts: true}); !ok || a.ReplacedBy != "decision:new" {
		t.Errorf("article old = %+v, %v; want replaced_by decision:new", a, ok)
	}
	// The reference is checked as a known Kind: a missing target fails the
	// load with the file named, not "unknown kind".
	loadErr(t, map[string]string{"global/articles/old.md": old}, "global/articles/old.md")
}

func TestDecisionTitleWithAHiddenRuneFailsTheLoad(t *testing.T) {
	for _, r := range []string{"‮", "​", "⁦"} {
		doc := "---\ntitle: \"Use" + r + "Postgres\"\nstatus: accepted\ndate: 2023-05-02\n---\nBody\n"
		msg := loadErr(t, map[string]string{globalDecisionPath + "x.md": doc}, "global/decisions/x.md")
		if strings.Contains(msg, r) {
			t.Errorf("error echoes the hidden rune %U", []rune(r)[0])
		}
	}
}
