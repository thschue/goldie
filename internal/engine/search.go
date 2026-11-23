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

// The Kind and Scope of every Item the dir Source loads today (CONTEXT: Kind,
// Scope). Each search result carries both (ADR 0005).
const (
	KindArticle = "article"
	ScopeGlobal = "global"
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
}

// Search scores every global article against each whitespace-separated,
// lower-cased query word. Per word and keyword, an exact match scores +4 and a
// word that is a substring of the keyword +1. A word that is a substring of the
// name scores +2. Hits sort by score, highest first, then by ID.
func (s *Snapshot) Search(query string) []Hit {
	return s.SearchIn("", query)
}

// SearchIn is Search for a Project ("host/org/repo", case-insensitive). The
// Project's articles take part together with the global ones; when both have an
// ID, the Project's article wins and the global one is dropped. On equal score
// Project hits sort before global ones, then by ID. An empty or unregistered
// Project searches the global articles only.
func (s *Snapshot) SearchIn(proj, query string) []Hit {
	words := strings.Fields(strings.ToLower(query))
	var hits []Hit
	taken := map[itemKey]bool{}
	for _, list := range s.resolutionOrder(proj) {
		var layer []itemKey
		for _, a := range list {
			key := a.key()
			if taken[key] {
				continue
			}
			layer = append(layer, key)
			if h := scoreArticle(a, words); h.Score > 0 {
				hits = append(hits, h)
			}
		}
		for _, key := range layer {
			taken[key] = true
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if gi, gj := hits[i].Scope == ScopeGlobal, hits[j].Scope == ScopeGlobal; gi != gj {
			return gj
		}
		return hits[i].ID < hits[j].ID
	})
	return hits
}

func scoreArticle(a Article, words []string) Hit {
	h := Hit{ID: a.ID, Name: a.Name, Kind: KindArticle, Scope: a.Scope, Keywords: append([]string(nil), a.Keywords...)}
	name := strings.ToLower(a.Name)
	for _, w := range words {
		for _, k := range a.Keywords {
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
	return h
}

func addOnce(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
