package engine_test

import (
	"slices"
	"testing"
)

// Acceptance tests for #813: the engine owns the suggester behind a search
// miss. It takes the missed query and returns at most 3 keywords near it. The
// contract they fix:
//
//	// NearestKeywords is the keywords of the global Items closest to query,
//	// at most 3, nearest first, each once. A query that is close to nothing
//	// gets none. Drafts are left out unless opts asks for them (as Search).
//	func (s *Snapshot) NearestKeywords(query string, opts ...ReadOptions) []string
//
// Until it lands, this package's tests do not compile.

// Criterion 1: a misspelt query gets the keyword it was meant to be.
func TestNearestKeywordsNamesTheKeywordAMisspellingMeant(t *testing.T) {
	snap := load(t, map[string]string{
		"global/articles/a.md": article("A", "deploy", "kubernetes"),
		"global/articles/b.md": article("B", "policy"),
	})
	if hits := snap.Search("deplyo"); len(hits) != 0 {
		t.Fatalf("fixture: %q must be a miss, got %+v", "deplyo", hits)
	}
	got := snap.NearestKeywords("deplyo")
	if len(got) == 0 || got[0] != "deploy" {
		t.Errorf("NearestKeywords(deplyo) = %q, want deploy first", got)
	}
}

// Criterion 1: at most 3, however many keywords are near.
func TestNearestKeywordsReturnsAtMostThree(t *testing.T) {
	snap := load(t, map[string]string{
		"global/articles/a.md": article("A", "deploy", "deploya", "deployb", "deployc", "deployd", "deploye"),
	})
	got := snap.NearestKeywords("deploz")
	if len(got) == 0 || len(got) > 3 {
		t.Fatalf("NearestKeywords(deploz) = %q, want 1 to 3 keywords", got)
	}
	if got[0] != "deploy" {
		t.Errorf("NearestKeywords(deploz) = %q, want the closest, deploy, first", got)
	}
	seen := map[string]bool{}
	for _, k := range got {
		if seen[k] {
			t.Errorf("NearestKeywords(deploz) = %q repeats %q", got, k)
		}
		seen[k] = true
	}
}

// Criterion 1: a query close to nothing names nothing; it does not pad the
// answer with unrelated keywords.
func TestNearestKeywordsNamesNothingForAnUnrelatedQuery(t *testing.T) {
	snap := load(t, map[string]string{
		"global/articles/a.md": article("A", "deploy", "kubernetes", "policy"),
	})
	if got := snap.NearestKeywords("zzqqxxnothing"); len(got) != 0 {
		t.Errorf("NearestKeywords(zzqqxxnothing) = %q, want none", got)
	}
}

// Criterion 1: only keywords of Items that exist are named, and a keyword of
// a draft Item is not leaked to a default read.
func TestNearestKeywordsNeverNamesAKeywordOfADraft(t *testing.T) {
	snap := load(t, map[string]string{
		"global/articles/a.md": article("A", "deploy"),
		"global/articles/d.md": "---\nname: D\nstatus: draft\nkeywords: [deplot]\n---\nBody.\n",
	})
	if got := snap.NearestKeywords("deplox"); slices.Contains(got, "deplot") {
		t.Errorf("NearestKeywords(deplox) = %q names a draft Item's keyword", got)
	}
}
