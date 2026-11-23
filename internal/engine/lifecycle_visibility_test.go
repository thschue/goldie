package engine_test

import (
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #270: Resolution leaves drafts out unless the call passes
// engine.ReadOptions{Drafts: true}, a draft never shadows a published Item,
// and deprecated Items are still served with Status and ReplacedBy.
//
// The options are the trailing variadic argument of SearchIn and ArticleIn, so
// existing callers keep compiling.

var withDrafts = engine.ReadOptions{Drafts: true}

func lifecycleFiles() map[string]string {
	return map[string]string{
		globalFile("pub"):   govArticle("Pub", "keywords: [lifecycle]", "status: published"),
		globalFile("draft"): govArticle("Draft", "keywords: [lifecycle]", "status: draft"),
		globalFile("old"):   govArticle("Old", "keywords: [lifecycle]", "status: deprecated", "replaced_by: article:pub"),
		globalFile("plain"): govArticle("Plain", "keywords: [lifecycle]"),
		// Registers the Project.
		projectFile("other"): govArticle("Other", "keywords: [elsewhere]"),
	}
}

func hitIDs(hits []engine.Hit) map[string]int {
	out := map[string]int{}
	for _, h := range hits {
		out[h.ID]++
	}
	return out
}

func TestDraftsLeftOutOfSearchUnlessAsked(t *testing.T) {
	snap := load(t, lifecycleFiles())
	for _, proj := range []string{"", govProject} {
		got := hitIDs(snap.SearchIn(proj, "lifecycle"))
		if got["draft"] != 0 {
			t.Errorf("SearchIn(%q) default found the draft: %v", proj, got)
		}
		for _, id := range []string{"pub", "old", "plain"} {
			if got[id] != 1 {
				t.Errorf("SearchIn(%q) default missed %s: %v", proj, id, got)
			}
		}
		if off := hitIDs(snap.SearchIn(proj, "lifecycle", engine.ReadOptions{})); len(off) != len(got) || off["draft"] != 0 {
			t.Errorf("SearchIn(%q) with Drafts false = %v, want the default %v", proj, off, got)
		}
		on := hitIDs(snap.SearchIn(proj, "lifecycle", withDrafts))
		for _, id := range []string{"pub", "old", "plain", "draft"} {
			if on[id] != 1 {
				t.Errorf("SearchIn(%q) with Drafts missed %s: %v", proj, id, on)
			}
		}
	}
	if got := hitIDs(snap.Search("lifecycle")); got["draft"] != 0 || got["pub"] != 1 {
		t.Errorf("Search found %v, want no draft", got)
	}
}

func TestDraftsLeftOutOfArticleUnlessAsked(t *testing.T) {
	snap := load(t, lifecycleFiles())
	for _, proj := range []string{"", govProject} {
		if a, ok := snap.ArticleIn(proj, "draft"); ok {
			t.Errorf("ArticleIn(%q, draft) default = %+v, want not found", proj, a)
		}
		if a, ok := snap.ArticleIn(proj, "DRAFT", engine.ReadOptions{}); ok {
			t.Errorf("ArticleIn(%q, DRAFT) with Drafts false = %+v, want not found", proj, a)
		}
		a, ok := snap.ArticleIn(proj, "draft", withDrafts)
		if !ok || a.Status != "draft" {
			t.Errorf("ArticleIn(%q, draft) with Drafts = %+v, %v; want the draft", proj, a, ok)
		}
		for _, id := range []string{"pub", "old", "plain"} {
			if _, ok := snap.ArticleIn(proj, id); !ok {
				t.Errorf("ArticleIn(%q, %s) default: not found", proj, id)
			}
		}
	}
	if _, ok := snap.Article("draft"); ok {
		t.Error("Article(draft) found a draft")
	}
}

func TestDraftProjectCopyDoesNotShadowPublishedGlobal(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("db"):  govArticle("Global DB", "keywords: [database]", "status: published"),
		projectFile("DB"): govArticle("Shop DB draft", "keywords: [database]", "status: draft"),
	})
	a, ok := snap.ArticleIn(govProject, "db")
	if !ok || a.Scope != engine.ScopeGlobal || a.Name != "Global DB" {
		t.Fatalf("ArticleIn = %+v, %v; want the published global Item", a, ok)
	}
	hits := snap.SearchIn(govProject, "database")
	if len(hits) != 1 || hits[0].Scope != engine.ScopeGlobal || hits[0].Name != "Global DB" {
		t.Fatalf("SearchIn = %+v; want the published global Item only", hits)
	}
	// With Drafts the identity still appears once.
	if hits := snap.SearchIn(govProject, "database", withDrafts); len(hits) != 1 {
		t.Fatalf("SearchIn with Drafts = %+v; want one hit for the identity", hits)
	}
	if a, ok := snap.ArticleIn(govProject, "db", withDrafts); !ok || a.Name == "" {
		t.Fatalf("ArticleIn with Drafts = %+v, %v; want an answer", a, ok)
	}
}

func TestPublishedProjectCopyWinsOverDraftGlobal(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("db"):  govArticle("Global DB draft", "keywords: [database]", "status: draft"),
		projectFile("db"): govArticle("Shop DB", "keywords: [database]", "status: published"),
	})
	for _, opts := range [][]engine.ReadOptions{nil, {withDrafts}} {
		a, ok := snap.ArticleIn(govProject, "db", opts...)
		if !ok || a.Scope != govProject || a.Name != "Shop DB" {
			t.Errorf("ArticleIn(opts=%v) = %+v, %v; want the project Item", opts, a, ok)
		}
		hits := snap.SearchIn(govProject, "database", opts...)
		if len(hits) != 1 || hits[0].Scope != govProject || hits[0].Name != "Shop DB" {
			t.Errorf("SearchIn(opts=%v) = %+v; want the project Item only", opts, hits)
		}
	}
	// A reader without the Project sees only the global draft, so nothing.
	if a, ok := snap.ArticleIn("", "db"); ok {
		t.Errorf("global ArticleIn = %+v, want not found", a)
	}
	if hits := snap.Search("database"); len(hits) != 0 {
		t.Errorf("global Search = %+v, want none", hits)
	}
}

func TestDraftWithNoPublishedCopyIsNotFoundFromProject(t *testing.T) {
	snap := load(t, map[string]string{
		projectFile("wip"): govArticle("WIP", "keywords: [wip]", "status: draft"),
	})
	if a, ok := snap.ArticleIn(govProject, "wip"); ok {
		t.Errorf("ArticleIn = %+v, want not found", a)
	}
	if hits := snap.SearchIn(govProject, "wip"); len(hits) != 0 {
		t.Errorf("SearchIn = %+v, want none", hits)
	}
	if a, ok := snap.ArticleIn(govProject, "wip", withDrafts); !ok || a.Status != "draft" {
		t.Errorf("ArticleIn with Drafts = %+v, %v", a, ok)
	}
	if hits := snap.SearchIn(govProject, "wip", withDrafts); len(hits) != 1 {
		t.Errorf("SearchIn with Drafts = %+v, want one", hits)
	}
}

func TestDeprecatedItemsAreServedWithStatusAndReplacedBy(t *testing.T) {
	snap := load(t, lifecycleFiles())
	for _, proj := range []string{"", govProject} {
		a, ok := snap.ArticleIn(proj, "old")
		if !ok || a.Status != "deprecated" || a.ReplacedBy != "article:pub" {
			t.Errorf("ArticleIn(%q, old) = %+v, %v; want deprecated, replaced by article:pub", proj, a, ok)
		}
		var found bool
		for _, h := range snap.SearchIn(proj, "lifecycle") {
			switch h.ID {
			case "old":
				found = true
				if h.Status != "deprecated" || h.ReplacedBy != "article:pub" {
					t.Errorf("Hit old = %+v; want Status deprecated, ReplacedBy article:pub", h)
				}
			case "pub":
				if h.Status != "published" || h.ReplacedBy != "" {
					t.Errorf("Hit pub = %+v; want Status published, no ReplacedBy", h)
				}
			}
		}
		if !found {
			t.Errorf("SearchIn(%q) did not return the deprecated Item", proj)
		}
	}
}

func TestDeprecatedProjectCopyStillShadowsGlobal(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("db"):  govArticle("Global DB", "keywords: [database]"),
		projectFile("db"): govArticle("Shop DB old", "keywords: [database]", "status: deprecated"),
	})
	a, ok := snap.ArticleIn(govProject, "db")
	if !ok || a.Scope != govProject || a.Status != "deprecated" {
		t.Fatalf("ArticleIn = %+v, %v; want the deprecated project Item", a, ok)
	}
	hits := snap.SearchIn(govProject, "database")
	if len(hits) != 1 || hits[0].Scope != govProject || hits[0].Status != "deprecated" {
		t.Fatalf("SearchIn = %+v; want the deprecated project Item only", hits)
	}
}
