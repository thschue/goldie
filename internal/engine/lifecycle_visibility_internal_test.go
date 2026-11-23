package engine

import "testing"

// Issue #270 (from #317): one Article.published() decides visibility, and an
// empty Status, as in an Article built in memory, counts as published. This is
// a same-package test because the helper and the in-memory Snapshot are
// unexported.

func TestPublishedHelper(t *testing.T) {
	for status, want := range map[string]bool{
		"":               true,
		StatusPublished:  true,
		StatusDeprecated: true,
		StatusDraft:      false,
	} {
		if got := (Article{Status: status}).published(); got != want {
			t.Errorf("Article{Status: %q}.published() = %v, want %v", status, got, want)
		}
	}
}

func TestInMemoryArticleWithoutStatusIsServedAndFound(t *testing.T) {
	const shop = "github.com/acme/shop"
	snap := newSnapshot([]Article{
		{ID: "mem", Scope: ScopeGlobal, Name: "Memory", Keywords: []string{"memo"}},
		{ID: "wip", Scope: ScopeGlobal, Name: "Wip", Keywords: []string{"memo"}, Status: StatusDraft},
	})
	snap.addProject(shop, []Article{
		{ID: "mem", Scope: shop, Name: "Shop memory", Keywords: []string{"memo"}},
	})
	for _, proj := range []string{"", shop} {
		a, ok := snap.ArticleIn(proj, "mem")
		if !ok || a.Name == "" {
			t.Errorf("ArticleIn(%q, mem) = %+v, %v; want served", proj, a, ok)
		}
		hits := snap.SearchIn(proj, "memo")
		if len(hits) != 1 || hits[0].ID != "mem" {
			t.Errorf("SearchIn(%q) = %+v; want only mem", proj, hits)
		}
		if _, ok := snap.ArticleIn(proj, "wip"); ok {
			t.Errorf("ArticleIn(%q, wip) served a draft", proj)
		}
	}
	if a, _ := snap.ArticleIn(shop, "mem"); a.Scope != shop {
		t.Errorf("project copy without Status did not win: %+v", a)
	}
}
