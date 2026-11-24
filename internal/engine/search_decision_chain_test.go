package engine

import "testing"

// The loader rejects a superseded_by that does not resolve, so a missing link
// and a cycle cannot reach search through a served snapshot; chainEnd still
// ends without a hit on both.
func TestChainEndStopsAtMissingLinkAndCycle(t *testing.T) {
	byName := map[string]Decision{
		"a":    {Name: "a", Status: StatusSuperseded, SupersededBy: "decision:B"},
		"b":    {Name: "b", Status: StatusSuperseded, SupersededBy: "decision:a"},
		"lost": {Name: "lost", Status: StatusSuperseded, SupersededBy: "decision:nowhere"},
		"bad":  {Name: "bad", Status: StatusSuperseded, SupersededBy: "article:x"},
		"ok":   {Name: "ok", Status: StatusSuperseded, SupersededBy: "decision:Fin"},
		"fin":  {Name: "Fin", Status: StatusAccepted},
	}
	for _, name := range []string{"a", "lost", "bad"} {
		if _, ok := chainEnd(byName[name], byName); ok {
			t.Errorf("chainEnd(%s) found an end, want none", name)
		}
	}
	if end, ok := chainEnd(byName["ok"], byName); !ok || end.Name != "Fin" {
		t.Errorf("chainEnd(ok) = %v, %v, want Fin through a mixed-case reference", end.Name, ok)
	}
}
