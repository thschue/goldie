package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

// Claim is what a Source may publish (CONTEXT: Claim). The zero Claim accepts
// nothing: deny by default.
type Claim struct {
	// Global accepts the Source's global Items.
	Global bool
	// Projects are globs over a Project's "host/org/repo". * stands for one
	// path segment and matching ignores case. A Project needs a match here;
	// Global does not imply any Project.
	Projects []string
}

// accepts reports whether the Claim covers the Project scope.
func (c Claim) accepts(scope string) bool {
	scope = strings.ToLower(scope)
	for _, g := range c.Projects {
		// A malformed glob matches nothing; parsing Claims is the caller's job.
		if ok, err := path.Match(strings.ToLower(g), scope); err == nil && ok {
			return true
		}
	}
	return false
}

// ClaimedSource is a Source that loads only what its Claim covers.
type ClaimedSource struct {
	base  Source
	claim Claim
}

var (
	_ Source     = ClaimedSource{}
	_ Revisioner = ClaimedSource{}
)

// NewClaimedSource returns a Source that loads base and fails the load when it
// holds an Item outside claim. Reading the Claim from configuration is the
// caller's job.
func NewClaimedSource(base Source, claim Claim) ClaimedSource {
	return ClaimedSource{base: base, claim: claim}
}

// Revision is the base Source's revision, or empty when it has none.
func (s ClaimedSource) Revision() string {
	if r, ok := s.base.(Revisioner); ok {
		return r.Revision()
	}
	return ""
}

// Load loads the base Source. An article or agent outside the Claim fails the
// whole load, with an error naming its file relative to the Source root. A
// Project with no Items and no suppressions does not fail; one outside the
// Claim that hides global Items fails, naming its project.yaml. A base error is
// returned as it is.
func (s ClaimedSource) Load(ctx context.Context) (*Snapshot, error) {
	snap, err := s.base.Load(ctx)
	if err != nil {
		return nil, err
	}
	if snap == nil {
		return nil, errors.New("the base Source returned no Snapshot")
	}
	if !s.claim.Global {
		if a := firstItem(snap.articles, snap.agents); a != nil {
			return nil, unclaimed(a, "global Items")
		}
		if name := firstSkill(snap.skills); name != "" {
			return nil, fmt.Errorf("%s/%s: the Source does not claim global Items", skillsDir, name)
		}
	}
	for _, id := range snap.Projects() {
		p, _ := snap.lookupProject(id)
		if s.claim.accepts(p.scope) {
			continue
		}
		if a := firstItem(p.articles, p.agents); a != nil {
			return nil, unclaimed(a, "Project "+p.scope)
		}
		if name := firstSkill(p.skills); name != "" {
			return nil, fmt.Errorf("%s/%s/skills/%s: the Source does not claim Project %s",
				projectsDir, p.scope, name, p.scope)
		}
		if len(p.suppressions) > 0 {
			return nil, fmt.Errorf("%s/%s/%s: the Source does not claim Project %s",
				projectsDir, p.scope, projectFileName, p.scope)
		}
	}
	return snap, nil
}

// firstItem returns the first Item of the lists, or nil when they are empty.
func firstItem(lists ...[]Article) *Article {
	for _, l := range lists {
		if len(l) > 0 {
			return &l[0]
		}
	}
	return nil
}

// firstSkill returns the first bundle name in order, or "" for none.
func firstSkill(skills map[string]skillBundle) string {
	first := ""
	for n := range skills {
		if first == "" || n < first {
			first = n
		}
	}
	return first
}

func unclaimed(a *Article, what string) error {
	return fmt.Errorf("%s: the Source does not claim %s", articleFile(a), what)
}
