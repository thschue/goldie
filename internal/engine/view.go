package engine

import "sort"

// View returns the articles that win in proj's view, never a draft, sorted by
// ID. It is Resolution applied to every Item at once: the same winner rule
// ArticleIn uses, so a Project copy shadows the global one, a suppressed Item
// is left out and a draft never shadows (CONTEXT: Resolution). An empty or
// unregistered Project is the global view. Only winners are returned, no
// shadowed copy and nothing of another Project.
func (s *Snapshot) View(proj string) []Article {
	var out []Article
	seen := map[itemKey]bool{}
	for _, list := range s.resolutionOrder(proj, KindArticle) {
		for i := range list {
			key := list[i].key()
			if seen[key] {
				continue
			}
			seen[key] = true
			if a := s.winner(proj, key, ReadOptions{}); a != nil {
				out = append(out, cloneArticle(*a))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
