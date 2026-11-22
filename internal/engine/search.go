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

// Hit is a search result: the article without its content, plus why it matched.
type Hit struct {
	ID       string
	Name     string
	Keywords []string
	// Score is the sum over all query words.
	Score int
	// Matched lists the keywords that matched, and "name" when the name did,
	// in first-match order without duplicates.
	Matched []string
}

// Search scores every article against each whitespace-separated, lower-cased
// query word. Per word and keyword, an exact match scores +4 and a word that is
// a substring of the keyword +1. A word that is a substring of the name scores
// +2. Hits sort by score, highest first, then by ID.
func (s *Snapshot) Search(query string) []Hit {
	words := strings.Fields(strings.ToLower(query))
	var hits []Hit
	for _, a := range s.articles {
		h := Hit{ID: a.ID, Name: a.Name, Keywords: append([]string(nil), a.Keywords...)}
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
		if h.Score > 0 {
			hits = append(hits, h)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	return hits
}

func addOnce(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
