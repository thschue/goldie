package engine

import (
	"sort"
	"strings"
)

// Scores of the keyword search (ADR 0005).
const (
	scoreExactKeyword = 4
	scoreName         = 2
	scoreSubstring    = 1
)

// The Kind and Scope of every Item the Sources load today (CONTEXT: Kind,
// Scope). Each search result carries both (ADR 0005). A Project Item's Scope is
// its "host/org/repo", which holds a slash, so it never equals a name here.
const (
	KindArticle = "article"
	ScopeGlobal = "global"
	// ScopeLocal is the repository's own .goldie/ (NewLocalSource).
	ScopeLocal = "local"
)

// Hit is a search result: the article without its content, plus why it matched.
type Hit struct {
	ID   string
	Name string
	// Kind and Scope say what the Item is and where it applies (ADR 0005).
	Kind     string
	Scope    string
	Keywords []string
	// Score is the sum over all query words.
	Score int
	// Matched lists the keywords that matched, and "name" when the name did,
	// in first-match order without duplicates.
	Matched []string
	// Status and ReplacedBy are the Item's lifecycle, copied from the Article.
	// Status is "published" for an Item without status frontmatter.
	Status     string
	ReplacedBy string
	// Enforced is the winning Item's frontmatter enforced. Overrides lists the
	// Scopes it shadows, nearest first; it is empty when it shadows nothing.
	Enforced  bool
	Overrides []string
}

// ReadOptions widens what a read returns. The zero value is the default view:
// drafts are left out (CONTEXT: Status). A read takes it as a variadic
// argument and uses only the first value; any further values are ignored.
type ReadOptions struct {
	// Drafts includes draft Items. A draft then shadows like any other Item,
	// within winner's rule: a Project draft never shadows an enforced global
	// Item (#359).
	Drafts bool
}

func readOpts(opts []ReadOptions) ReadOptions {
	if len(opts) == 0 {
		return ReadOptions{}
	}
	return opts[0]
}

// visible reports whether an Item takes part in a read under o.
func (o ReadOptions) visible(a Article) bool { return o.Drafts || a.published() }

// Search scores every global article against each whitespace-separated,
// lower-cased query word. Per word and keyword, an exact match scores +4 and a
// word that is a substring of the keyword +1. A word that is a substring of the
// name scores +2. Hits sort by score, highest first, then by ID.
func (s *Snapshot) Search(query string, opts ...ReadOptions) []Hit {
	return s.SearchIn("", query, opts...)
}

// SearchIn is Search for a Project ("host/org/repo", case-insensitive). The
// Project's articles take part together with the global ones; when both have an
// ID, the one winner picks answers and the other is dropped. On equal score
// local hits sort before Project ones and Project ones before global ones, then
// by ID. A local Item takes part in every view (NewLocalSource). An empty or unregistered
// Project searches the global articles only. Draft Items are left out unless
// opts asks for them. Deprecated Items are served and shadow. Which copy of
// each Item answers is winner's rule.
func (s *Snapshot) SearchIn(proj, query string, opts ...ReadOptions) []Hit {
	return s.SearchKindIn(proj, KindArticle, query, opts...)
}

// SearchKindIn is SearchIn for one Kind: KindArticle, KindAgent or KindSkill.
// Skills and agents match on their name and keywords only, never on the
// description (ADR 0005). A skill's keywords are those in its SKILL.md. Any
// other kind finds nothing.
func (s *Snapshot) SearchKindIn(proj, kind, query string, opts ...ReadOptions) []Hit {
	words := strings.Fields(strings.ToLower(query))
	var hits []Hit
	switch kind {
	case KindSkill:
		hits = s.searchSkills(proj, words, readOpts(opts))
	case KindArticle, KindAgent:
		hits = s.searchItems(proj, kind, words, readOpts(opts))
	}
	sortHits(hits)
	return hits
}

// searchSkills scores the skills that answer in proj's view. Drafts are left
// out unless o asks for them.
func (s *Snapshot) searchSkills(proj string, words []string, o ReadOptions) []Hit {
	var hits []Hit
	for _, b := range s.skillBundles(proj, o) {
		status := StatusPublished
		if b.status != "" {
			status = b.status
		}
		h := Hit{ID: b.name, Name: b.name, Kind: KindSkill, Scope: b.scope, Keywords: append([]string(nil), b.keywords...), Status: status}
		scoreWords(&h, b.name, b.keywords, words)
		if h.Score > 0 {
			hits = append(hits, h)
		}
	}
	return hits
}

// searchItems scores the articles or agents that answer in proj's view.
func (s *Snapshot) searchItems(proj, kind string, words []string, o ReadOptions) []Hit {
	var hits []Hit
	seen := map[itemKey]bool{}
	for _, list := range s.resolutionOrder(proj, kind) {
		for i := range list {
			key := list[i].key()
			if seen[key] {
				continue
			}
			seen[key] = true
			if a := s.winner(proj, key, o); a != nil {
				if h := scoreArticle(*a, words); h.Score > 0 {
					h.Overrides = s.overridden(proj, key, a)
					hits = append(hits, h)
				}
			}
		}
	}
	return hits
}

func sortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if ri, rj := scopeRank(hits[i].Scope), scopeRank(hits[j].Scope); ri != rj {
			return ri < rj
		}
		return hits[i].ID < hits[j].ID
	})
}

// scopeRank orders Scopes on equal score: local, then Project, then global
// (Resolution's order).
func scopeRank(scope string) int {
	switch scope {
	case ScopeLocal:
		return 0
	case ScopeGlobal:
		return 2
	}
	return 1
}

func scoreArticle(a Article, words []string) Hit {
	h := Hit{ID: a.ID, Name: a.Name, Kind: a.Kind(), Scope: a.Scope, Keywords: append([]string(nil), a.Keywords...), Status: a.Status, ReplacedBy: a.ReplacedBy, Enforced: a.Enforced}
	scoreWords(&h, a.Name, a.Keywords, words)
	return h
}

// scoreWords adds to h the score of the words against an Item's name and
// keywords.
func scoreWords(h *Hit, itemName string, keywords, words []string) {
	name := strings.ToLower(itemName)
	for _, w := range words {
		for _, k := range keywords {
			switch {
			case k == w:
				h.Score += scoreExactKeyword
				h.Matched = addOnce(h.Matched, k)
			case strings.Contains(k, w):
				h.Score += scoreSubstring
				h.Matched = addOnce(h.Matched, k)
			}
		}
		if strings.Contains(name, w) {
			h.Score += scoreName
			h.Matched = addOnce(h.Matched, "name")
		}
	}
}

func addOnce(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
