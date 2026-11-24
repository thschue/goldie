package engine_test

import (
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// #714 (slice of #34): Snapshot.DecisionChain walks the supersession history
// of a decision. The seam is the engine API.
//
// The contract fixed here, beyond the issue's wording:
//
//	func (s *Snapshot) DecisionChain(proj, name string) ([]Decision, bool)
//
// resolves name in proj's view as DecisionIn does, but in every status (a
// chain is history: superseded and rejected decisions belong in it), and
// returns the chain oldest to newest, including the decision itself. The
// bool is false when no decision of that name resolves in the view.

func datedDecision(title, status, date string, extra ...string) string {
	return decisionDoc(title, append([]string{"status: " + status, "date: " + date}, extra...)...)
}

// decisionChain calls Snapshot.DecisionChain through an interface so the file
// compiles before the method exists; it then fails, not the build.
func decisionChain(t *testing.T, snap *engine.Snapshot, proj, name string) ([]engine.Decision, bool) {
	t.Helper()
	c, ok := any(snap).(interface {
		DecisionChain(proj, name string) ([]engine.Decision, bool)
	})
	if !ok {
		t.Fatal("Snapshot has no method DecisionChain(proj, name string) ([]Decision, bool)")
	}
	return c.DecisionChain(proj, name)
}

func chainNames(t *testing.T, snap *engine.Snapshot, proj, name string) string {
	t.Helper()
	chain, ok := decisionChain(t, snap, proj, name)
	if !ok {
		t.Fatalf("DecisionChain(%q, %q) found nothing", proj, name)
	}
	return decisionNames(chain)
}

func TestDecisionChainRunsOldestToNewestAndIncludesTheDecision(t *testing.T) {
	snap := load(t, map[string]string{
		globalDecisionPath + "a.md": datedDecision("A", "superseded", "2023-01-01", "superseded_by: decision:b"),
		globalDecisionPath + "b.md": datedDecision("B", "superseded", "2023-02-01", "superseded_by: decision:c", "supersedes: [decision:a]"),
		globalDecisionPath + "c.md": datedDecision("C", "accepted", "2023-03-01", "supersedes: [decision:b]"),
		globalDecisionPath + "z.md": acceptedDecision("Unrelated"),
	})
	if got := chainNames(t, snap, "", "b"); got != "a,b,c" {
		t.Errorf("chain of b = %s, want a,b,c", got)
	}
	if got := chainNames(t, snap, "", "c"); got != "a,b,c" {
		t.Errorf("chain of the newest = %s, want a,b,c", got)
	}
	chain, _ := decisionChain(t, snap, "", "b")
	if chain[0].Status != "superseded" || chain[0].Title != "A" || chain[2].Status != "accepted" || chain[2].Scope != engine.ScopeGlobal {
		t.Errorf("chain entries = %+v, want each with its own title, status and scope", chain)
	}
	if got := chainNames(t, snap, "", "z"); got != "z" {
		t.Errorf("chain of an unrelated decision = %s, want only itself", got)
	}
}

func TestDecisionChainOrdersPredecessorsOfOneSuccessorByDateThenName(t *testing.T) {
	snap := load(t, map[string]string{
		globalDecisionPath + "a-late.md": datedDecision("Late", "superseded", "2023-06-01", "superseded_by: decision:merged"),
		globalDecisionPath + "b-tie.md":  datedDecision("Tie B", "superseded", "2023-05-01", "superseded_by: decision:merged"),
		globalDecisionPath + "a-tie.md":  datedDecision("Tie A", "superseded", "2023-05-01", "superseded_by: decision:merged"),
		globalDecisionPath + "merged.md": datedDecision("Merged", "accepted", "2023-07-01",
			"supersedes: [decision:a-late, decision:b-tie, decision:a-tie]"),
	})
	if got := chainNames(t, snap, "", "merged"); got != "a-tie,b-tie,a-late,merged" {
		t.Errorf("chain = %s, want a-tie,b-tie,a-late,merged (date first, then name)", got)
	}
}

func TestDecisionChainResolvesInTheProjectView(t *testing.T) {
	snap := load(t, map[string]string{
		projectDecisionPath + "old.md": datedDecision("Old", "superseded", "2023-01-01", "superseded_by: decision:new"),
		globalDecisionPath + "new.md":  datedDecision("New", "accepted", "2023-02-01"),
	})
	chain, ok := decisionChain(t, snap, govProject, "old")
	if !ok || decisionNames(chain) != "old,new" || chain[0].Scope != govProject || chain[1].Scope != engine.ScopeGlobal {
		t.Errorf("chain in the Project view = %+v, %v; want old (Project) then new (global)", chain, ok)
	}
	if chain, ok := decisionChain(t, snap, "", "old"); ok {
		t.Errorf("a Project decision has a chain in the global view: %+v", chain)
	}
	if got := chainNames(t, snap, "github.com/acme/unregistered", "new"); got != "new" {
		t.Errorf("chain for an unregistered Project = %s, want the global answer", got)
	}
}

func TestDecisionChainOfAnUnknownDecisionFindsNothing(t *testing.T) {
	snap := load(t, map[string]string{globalDecisionPath + "a.md": acceptedDecision("A")})
	for _, name := range []string{"nope", "", "../a", strings.Repeat("x", 300)} {
		if chain, ok := decisionChain(t, snap, "", name); ok || len(chain) != 0 {
			t.Errorf("DecisionChain(%q) = %+v, %v; want nothing", name, chain, ok)
		}
	}
}
