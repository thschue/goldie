package engine

import (
	"fmt"
	"strings"
	"testing"
)

// Issue #376: one Snapshot.winner decides which copy of an Item answers in a
// Project's view, the Project's or the global one, applying visibility
// (#353, ReadOptions.Drafts), suppression (#360) and enforcement (#370,
// #359). This is a same-package test because winner and the in-memory
// Snapshot are unexported. The signature it fixes is
//
//	func (s *Snapshot) winner(proj string, key itemKey, o ReadOptions) *Article
//
// The table covers every combination of the Project copy, the global copy,
// whether the Project suppresses the global Item, whether the global copy is
// enforced, and whether drafts are readable. Some combinations cannot come out
// of a load (a published Project copy over a published enforced global one, a
// suppression of an enforced or a draft Item); winner still answers for them,
// as ArticleIn and SearchIn do today. The expectations are today's ArticleIn
// and SearchIn behaviour, which TestWinnerTableIsTodaysReadBehaviour proves.

type winnerCopy int

const (
	copyAbsent winnerCopy = iota
	copyPublished
	copyDraft
)

func (c winnerCopy) String() string {
	return [...]string{"absent", "published", "draft"}[c]
}

type winnerCase struct {
	project, global winnerCopy
	suppressed      bool // the Project suppresses the global Item
	enforced        bool // the global copy has enforced: true
	drafts          bool // ReadOptions{Drafts: true}
	want            string
}

func (c winnerCase) String() string {
	return fmt.Sprintf("project=%v/global=%v/suppressed=%v/enforced=%v/drafts=%v", c.project, c.global, c.suppressed, c.enforced, c.drafts)
}

const winnerShop = "github.com/acme/shop"

var winnerCases = []winnerCase{
	// project, global, suppressed, enforced, drafts, which copy answers
	{copyAbsent, copyAbsent, false, false, false, "none"},
	{copyAbsent, copyAbsent, false, false, true, "none"},
	{copyAbsent, copyAbsent, false, true, false, "none"},
	{copyAbsent, copyAbsent, false, true, true, "none"},
	{copyAbsent, copyAbsent, true, false, false, "none"},
	{copyAbsent, copyAbsent, true, false, true, "none"},
	{copyAbsent, copyAbsent, true, true, false, "none"},
	{copyAbsent, copyAbsent, true, true, true, "none"},
	{copyAbsent, copyPublished, false, false, false, "global"},
	{copyAbsent, copyPublished, false, false, true, "global"},
	{copyAbsent, copyPublished, false, true, false, "global"},
	{copyAbsent, copyPublished, false, true, true, "global"},
	{copyAbsent, copyPublished, true, false, false, "none"},
	{copyAbsent, copyPublished, true, false, true, "none"},
	{copyAbsent, copyPublished, true, true, false, "none"},
	{copyAbsent, copyPublished, true, true, true, "none"},
	{copyAbsent, copyDraft, false, false, false, "none"},
	{copyAbsent, copyDraft, false, false, true, "global"},
	{copyAbsent, copyDraft, false, true, false, "none"},
	{copyAbsent, copyDraft, false, true, true, "global"},
	{copyAbsent, copyDraft, true, false, false, "none"},
	{copyAbsent, copyDraft, true, false, true, "none"},
	{copyAbsent, copyDraft, true, true, false, "none"},
	{copyAbsent, copyDraft, true, true, true, "none"},
	{copyPublished, copyAbsent, false, false, false, "project"},
	{copyPublished, copyAbsent, false, false, true, "project"},
	{copyPublished, copyAbsent, false, true, false, "project"},
	{copyPublished, copyAbsent, false, true, true, "project"},
	{copyPublished, copyAbsent, true, false, false, "project"},
	{copyPublished, copyAbsent, true, false, true, "project"},
	{copyPublished, copyAbsent, true, true, false, "project"},
	{copyPublished, copyAbsent, true, true, true, "project"},
	{copyPublished, copyPublished, false, false, false, "project"},
	{copyPublished, copyPublished, false, false, true, "project"},
	{copyPublished, copyPublished, false, true, false, "global"},
	{copyPublished, copyPublished, false, true, true, "global"},
	{copyPublished, copyPublished, true, false, false, "project"},
	{copyPublished, copyPublished, true, false, true, "project"},
	{copyPublished, copyPublished, true, true, false, "none"},
	{copyPublished, copyPublished, true, true, true, "none"},
	{copyPublished, copyDraft, false, false, false, "project"},
	{copyPublished, copyDraft, false, false, true, "project"},
	{copyPublished, copyDraft, false, true, false, "project"},
	{copyPublished, copyDraft, false, true, true, "project"},
	{copyPublished, copyDraft, true, false, false, "project"},
	{copyPublished, copyDraft, true, false, true, "project"},
	{copyPublished, copyDraft, true, true, false, "project"},
	{copyPublished, copyDraft, true, true, true, "project"},
	{copyDraft, copyAbsent, false, false, false, "none"},
	{copyDraft, copyAbsent, false, false, true, "project"},
	{copyDraft, copyAbsent, false, true, false, "none"},
	{copyDraft, copyAbsent, false, true, true, "project"},
	{copyDraft, copyAbsent, true, false, false, "none"},
	{copyDraft, copyAbsent, true, false, true, "project"},
	{copyDraft, copyAbsent, true, true, false, "none"},
	{copyDraft, copyAbsent, true, true, true, "project"},
	{copyDraft, copyPublished, false, false, false, "global"},
	{copyDraft, copyPublished, false, false, true, "project"},
	{copyDraft, copyPublished, false, true, false, "global"},
	{copyDraft, copyPublished, false, true, true, "global"},
	{copyDraft, copyPublished, true, false, false, "none"},
	{copyDraft, copyPublished, true, false, true, "project"},
	{copyDraft, copyPublished, true, true, false, "none"},
	{copyDraft, copyPublished, true, true, true, "none"},
	{copyDraft, copyDraft, false, false, false, "none"},
	{copyDraft, copyDraft, false, false, true, "project"},
	{copyDraft, copyDraft, false, true, false, "none"},
	{copyDraft, copyDraft, false, true, true, "project"},
	{copyDraft, copyDraft, true, false, false, "none"},
	{copyDraft, copyDraft, true, false, true, "project"},
	{copyDraft, copyDraft, true, true, false, "none"},
	{copyDraft, copyDraft, true, true, true, "project"},
}

// winnerItemKey is the Item every fixture holds, spelled differently from its
// ID, since identity ignores case (ADR 0015).
var winnerItemKey = keyOf(KindArticle, "Team/Item")

// winnerFixture builds the in-memory Snapshot for c, as
// TestInMemoryArticleWithoutStatusIsServedAndFound does. The suppression is
// set on the Project directly, so that combinations a load rejects can be
// built too.
func winnerFixture(c winnerCase) *Snapshot {
	status := func(w winnerCopy) string {
		if w == copyDraft {
			return StatusDraft
		}
		return StatusPublished
	}
	var global, local []Article
	if c.global != copyAbsent {
		global = append(global, Article{ID: "team/item", Scope: ScopeGlobal, Name: "Global item", Keywords: []string{"item"}, Status: status(c.global), Enforced: c.enforced})
	}
	if c.project != copyAbsent {
		local = append(local, Article{ID: "team/item", Scope: winnerShop, Name: "Project item", Keywords: []string{"item"}, Status: status(c.project)})
	}
	snap := newSnapshot(global)
	snap.addProject(winnerShop, local)
	if c.suppressed {
		p := snap.projects[strings.ToLower(winnerShop)]
		p.suppressed = map[itemKey]bool{winnerItemKey: true}
		p.suppressions = []string{"article:team/item"}
		snap.projects[strings.ToLower(winnerShop)] = p
	}
	return snap
}

// answeredBy names the copy a: "project", "global" or "none".
func answeredBy(a *Article) string {
	switch {
	case a == nil:
		return "none"
	case a.Scope == ScopeGlobal && a.Name == "Global item":
		return "global"
	case a.Scope == winnerShop && a.Name == "Project item":
		return "project"
	}
	return fmt.Sprintf("an unexpected Article %+v", *a)
}

// globalViewWant is what answers outside the Project (an empty or unregistered
// Project id): the global copy when it is readable. A Project's suppression
// does not reach outside it, and enforcement only stops Project copies.
func globalViewWant(c winnerCase) string {
	if c.global == copyPublished || (c.global == copyDraft && c.drafts) {
		return "global"
	}
	return "none"
}

func TestWinnerTableCoversEveryCombination(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range winnerCases {
		key := c.String()
		if seen[key] {
			t.Errorf("case %s is in the table twice", key)
		}
		seen[key] = true
	}
	if want := 3 * 3 * 2 * 2 * 2; len(seen) != want {
		t.Fatalf("the table has %d distinct cases, want %d", len(seen), want)
	}
}

func TestWinner(t *testing.T) {
	for _, c := range winnerCases {
		t.Run(c.String(), func(t *testing.T) {
			snap := winnerFixture(c)
			o := ReadOptions{Drafts: c.drafts}
			// The Project id matches case-insensitively (ADR 0020).
			for _, proj := range []string{winnerShop, strings.ToUpper(winnerShop)} {
				if got := answeredBy(snap.winner(proj, winnerItemKey, o)); got != c.want {
					t.Errorf("winner(%q) answered with %s, want %s", proj, got, c.want)
				}
			}
			// Outside the Project only the global copy can answer.
			for _, proj := range []string{"", "github.com/acme/other"} {
				if got, want := answeredBy(snap.winner(proj, winnerItemKey, o)), globalViewWant(c); got != want {
					t.Errorf("winner(%q) answered with %s, want %s", proj, got, want)
				}
			}
		})
	}
}

// TestWinnerTableIsTodaysReadBehaviour runs the same table through the public
// read paths winner replaces, ArticleIn and SearchIn, so the expectations are
// the behaviour the refactor must keep.
func TestWinnerTableIsTodaysReadBehaviour(t *testing.T) {
	for _, c := range winnerCases {
		t.Run(c.String(), func(t *testing.T) {
			snap := winnerFixture(c)
			o := ReadOptions{Drafts: c.drafts}
			views := []struct{ proj, want string }{
				{winnerShop, c.want},
				{strings.ToUpper(winnerShop), c.want},
				{"", globalViewWant(c)},
				{"github.com/acme/other", globalViewWant(c)},
			}
			for _, v := range views {
				var got *Article
				if a, ok := snap.ArticleIn(v.proj, "TEAM/ITEM", o); ok {
					got = &a
				}
				if g := answeredBy(got); g != v.want {
					t.Errorf("ArticleIn(%q) answered with %s, want %s", v.proj, g, v.want)
				}
				hits := snap.SearchIn(v.proj, "item", o)
				search := "none"
				switch {
				case len(hits) > 1:
					search = fmt.Sprintf("%d hits", len(hits))
				case len(hits) == 1:
					search = answeredBy(&Article{Scope: hits[0].Scope, Name: hits[0].Name})
				}
				if search != v.want {
					t.Errorf("SearchIn(%q) answered with %s, want %s", v.proj, search, v.want)
				}
			}
		})
	}
}
