package engine

import "sort"

// View returns the articles that win in proj's view, never a draft, sorted by
// ID. It is ArticlesIn without options. See ArticlesIn for the rules.
func (s *Snapshot) View(proj string) []Article {
	return s.ArticlesIn(proj)
}

// ArticlesIn returns the articles that win in proj's view, sorted by ID. It is
// Resolution applied to every Item at once: the same winner rule ArticleIn
// uses, so a Project copy shadows the global one, a suppressed Item is left
// out and an enforced global Item still wins (CONTEXT: Resolution). Drafts are
// left out unless opts asks for them. An empty or unregistered Project is the
// global view. Only winners are returned, no shadowed copy and nothing of
// another Project. Each result is a copy with Overrides set.
func (s *Snapshot) ArticlesIn(proj string, opts ...ReadOptions) []Article {
	o := readOpts(opts)
	var out []Article
	seen := map[itemKey]bool{}
	for _, list := range s.resolutionOrder(proj, KindArticle) {
		for i := range list {
			key := list[i].key()
			if seen[key] {
				continue
			}
			seen[key] = true
			if a := s.winner(proj, key, o); a != nil {
				c := cloneArticle(*a)
				c.Overrides = s.overridden(proj, key, a)
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
