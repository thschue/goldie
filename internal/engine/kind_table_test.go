package engine

import (
	"slices"
	"testing"
)

// The Kinds a reference may name come from one table. The agent Kind is loaded
// (#405), so agent:x and article:x are accepted; a Kind with no loader is not.
func TestParseRefKnownKindsTable(t *testing.T) {
	for ref, want := range map[string]itemKey{
		"article:x": keyOf(KindArticle, "x"),
		"agent:x":   keyOf("agent", "x"),
		"agent:X":   keyOf("agent", "x"),
	} {
		if key, err := parseRef(ref); err != nil || key != want {
			t.Fatalf("parseRef(%s) = %v, %v; want %v", ref, key, err, want)
		}
	}
	for _, ref := range []string{"skill:x", "loop:x", "instruction:x", "x:y", "Agent:x"} {
		if _, err := parseRef(ref); err == nil {
			t.Fatalf("%s accepted while its Kind is not loaded", ref)
		}
	}
	if !slices.Contains(knownKinds, "agent") {
		t.Fatalf("knownKinds = %v, want agent in the table", knownKinds)
	}
}

func TestAgentCarriesItsKind(t *testing.T) {
	a := Agent{Name: "Team-Bot"}
	if a.Kind() != "agent" {
		t.Fatalf("Kind %q", a.Kind())
	}
}

func TestArticleCarriesItsKind(t *testing.T) {
	a := Article{ID: "Team/X"}
	if a.Kind() != KindArticle || a.key() != keyOf(KindArticle, "team/x") {
		t.Fatalf("Kind %q key %v", a.Kind(), a.key())
	}
	if got := itemRef(&a); got != "article:Team/X" {
		t.Fatalf("itemRef = %q", got)
	}
}
