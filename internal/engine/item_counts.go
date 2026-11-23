package engine

// ItemCount is the number of Items of one Kind in one Scope kind. Scope is
// ScopeGlobal, "project" (every Project together, never a Project id) or
// ScopeLocal; Kind is KindArticle, KindAgent, KindInstruction or KindSkill.
type ItemCount struct {
	Scope string
	Kind  string
	Count int64
}

// scopeProject is the Scope kind all Projects count under.
const scopeProject = "project"

// ItemCounts counts the Items of the Snapshot by Scope kind and Kind. It
// returns one entry for each of the 12 combinations, 0 for an empty one, in a
// fixed order. Shadowed Items count too: it counts what is held, not what a
// view resolves to. It is pure and reads the Snapshot only.
func (s *Snapshot) ItemCounts() []ItemCount {
	var global, project, local scopeTotals
	global.add(s.scopeItems)
	for _, p := range s.projects {
		project.add(p.scopeItems)
	}
	if s.local != nil {
		local.add(s.local.scopeItems)
	}
	out := make([]ItemCount, 0, 12)
	for _, sc := range []struct {
		name string
		t    scopeTotals
	}{{ScopeGlobal, global}, {scopeProject, project}, {ScopeLocal, local}} {
		out = append(out,
			ItemCount{sc.name, KindArticle, sc.t.articles},
			ItemCount{sc.name, KindAgent, sc.t.agents},
			ItemCount{sc.name, KindInstruction, sc.t.instructions},
			ItemCount{sc.name, KindSkill, sc.t.skills},
		)
	}
	return out
}

type scopeTotals struct{ articles, agents, instructions, skills int64 }

func (t *scopeTotals) add(si scopeItems) {
	t.articles += int64(len(si.articles))
	t.agents += int64(len(si.agents))
	t.instructions += int64(len(si.instructions))
	t.skills += int64(len(si.skills))
}
