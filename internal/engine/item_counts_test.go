package engine_test

import (
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Acceptance tests for #595: (*Snapshot).ItemCounts counts the Items of a
// Snapshot by Scope kind (global, project, local) and Kind (article, agent,
// instruction, skill). It is pure and the source of the goldie.items gauge.
//
// The API the tests assume, following the Snapshot accessors (the issue names
// the call but not its types):
//
//	type ItemCount struct {
//		Scope string // ScopeGlobal, "project" or ScopeLocal
//		Kind  string // KindArticle, KindAgent, KindLoop, KindInstruction or KindSkill
//		Count int64
//	}
//	func (s *Snapshot) ItemCounts() []ItemCount
//
// It returns one entry for each of the 15 combinations, with Count 0 for an
// empty one, so an operator sees every series from the first Snapshot on.

// itemKinds is every Kind an Item can have; ItemCounts has one series per Scope
// kind and Kind.
var itemKinds = []string{engine.KindArticle, engine.KindAgent, engine.KindLoop, engine.KindInstruction, engine.KindSkill}

const otherProject = "github.com/acme/other"

func projectFileOf(proj, dir, name string) string {
	return "projects/" + proj + "/" + dir + "/" + name
}

// countsOf flattens ItemCounts to "scope/kind" -> count, failing on a repeated
// or unknown series, so every Scope kind and Kind combination appears exactly once.
func countsOf(t *testing.T, snap *engine.Snapshot) map[string]int64 {
	t.Helper()
	scopes := map[string]bool{engine.ScopeGlobal: true, "project": true, engine.ScopeLocal: true}
	kinds := map[string]bool{}
	for _, k := range itemKinds {
		kinds[k] = true
	}
	out := map[string]int64{}
	for _, c := range snap.ItemCounts() {
		if !scopes[c.Scope] || !kinds[c.Kind] {
			t.Errorf("ItemCounts has the series %q/%q, outside the scope and kind values", c.Scope, c.Kind)
		}
		key := c.Scope + "/" + c.Kind
		if _, dup := out[key]; dup {
			t.Errorf("ItemCounts repeats the series %s", key)
		}
		out[key] = c.Count
	}
	if want := len(scopes) * len(kinds); len(out) != want {
		t.Errorf("ItemCounts has %d series %v, want all %d", len(out), out, want)
	}
	return out
}

// assertCounts fails unless every series equals its value in want, with 0 for
// a series want does not name.
func assertCounts(t *testing.T, got, want map[string]int64) {
	t.Helper()
	for _, scope := range []string{engine.ScopeGlobal, "project", engine.ScopeLocal} {
		for _, kind := range itemKinds {
			key := scope + "/" + kind
			if got[key] != want[key] {
				t.Errorf("%s = %d, want %d", key, got[key], want[key])
			}
		}
	}
}

// Criterion: Items are counted by Scope kind and Kind; all Projects count under
// "project"; a Project Item that shadows a global one does not subtract it.
func TestItemCountsSplitByScopeKindAndSumProjects(t *testing.T) {
	snap := load(t, map[string]string{
		"global/articles/style.md":                          article("Style"),
		"global/articles/other.md":                          article("Other"),
		globalAgentFile("bot"):                              agentDoc("bot", "d"),
		globalClauseFile("rule"):                            clause("rule", 1, "claude", "body"),
		globalSkillFile("deploy", "SKILL.md"):               "# Deploy\n",
		globalSkillFile("deploy", "scripts/run.sh"):         "echo\n",
		projectFileOf(govProject, "articles", "style.md"):   article("Style"), // shadows a global Item
		projectAgentFile("shop-bot"):                        agentDoc("shop-bot", "d"),
		projectFileOf(otherProject, "articles", "x.md"):     article("X"),
		projectFileOf(otherProject, "skills", "s/SKILL.md"): "# S\n",
	})
	assertCounts(t, countsOf(t, snap), map[string]int64{
		"global/article": 2, "global/agent": 1, "global/instruction": 1, "global/skill": 1,
		"project/article": 2, "project/agent": 1, "project/skill": 1,
	})
}

// Criterion: the local Scope counts under "local".
func TestItemCountsCountTheLocalScope(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		"global/articles/g.md":               article("G"),
		localFile("a"):                       article("A"),
		localFile("b"):                       article("B"),
		localAgentFile("bot"):                agentDoc("bot", "d"),
		localClauseFile("rule"):              clause("rule", 1, "claude", "body"),
		localSkillFile("deploy", "SKILL.md"): "# Deploy\n",
	})
	assertCounts(t, countsOf(t, snap), map[string]int64{
		"global/article": 1,
		"local/article":  2, "local/agent": 1, "local/instruction": 1, "local/skill": 1,
	})
}

// Criterion: from the first Snapshot on, all 15 series appear, with 0 for an
// empty one.
func TestItemCountsOfAnEmptySnapshotAreAllZero(t *testing.T) {
	snap := load(t, map[string]string{"global/articles/only.md": article("Only")})
	assertCounts(t, countsOf(t, snap), map[string]int64{"global/article": 1})
}
