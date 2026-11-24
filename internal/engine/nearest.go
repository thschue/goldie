package engine

import (
	"sort"
	"strings"
)

// MaxNearest is how many keywords NearestKeywords names at most.
const MaxNearest = 3

// NearestKeywords is the keywords of the global Items closest to query, at
// most 3, nearest first, each once. A keyword is near when one of the query's
// words is within an edit distance of a third of the word's length (at most 2;
// a transposition counts as one edit) of it. A query that is close to nothing
// gets none. Drafts are left out unless opts asks for them (as Search).
func (s *Snapshot) NearestKeywords(query string, opts ...ReadOptions) []string {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	type cand struct {
		keyword string
		dist    int
	}
	var cands []cand
	seen := map[string]bool{}
	for _, a := range s.ArticlesIn("", opts...) {
		for _, k := range a.Keywords {
			lk := strings.ToLower(k)
			if lk == "" || seen[lk] {
				continue
			}
			seen[lk] = true
			best := -1
			for _, w := range words {
				limit := min(2, len([]rune(w))/3)
				if d := editDistance(w, lk, limit); d >= 0 && (best < 0 || d < best) {
					best = d
				}
			}
			if best >= 0 {
				cands = append(cands, cand{k, best})
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].keyword < cands[j].keyword
	})
	var out []string
	for _, c := range cands[:min(len(cands), MaxNearest)] {
		out = append(out, c.keyword)
	}
	return out
}

// editDistance is the optimal string alignment distance between a and b (an
// edit is an insert, a delete, a substitution or a swap of neighbours), or -1
// when it is above limit.
func editDistance(a, b string, limit int) int {
	ra, rb := []rune(a), []rune(b)
	if diff := len(ra) - len(rb); diff > limit || -diff > limit {
		return -1
	}
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	if d := prev[len(rb)]; d <= limit {
		return d
	}
	return -1
}
