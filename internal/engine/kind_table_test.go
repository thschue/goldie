package engine

import "testing"

// The Kinds a reference may name come from one table. While no agent Kind is
// loaded, agent:x is rejected; the article Kind is accepted.
func TestParseRefKnownKindsTable(t *testing.T) {
	if _, err := parseRef("agent:x"); err == nil {
		t.Fatal("agent:x accepted while no agent Kind is loaded")
	}
	key, err := parseRef("article:x")
	if err != nil || key != keyOf(KindArticle, "x") {
		t.Fatalf("parseRef(article:x) = %v, %v", key, err)
	}

	old := knownKinds
	defer func() { knownKinds = old }()
	knownKinds = append(append([]string{}, old...), "agent")
	if key, err := parseRef("agent:x"); err != nil || key != keyOf("agent", "x") {
		t.Fatalf("parseRef(agent:x) with agent in the table = %v, %v", key, err)
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
