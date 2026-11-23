package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// View is the Items of one Project's resolved view as an upstream server
// exported them (GET /api/v1/projects/{id}/items), the input of a view
// Source. Items are the winning copies, global ones and the Project's own,
// with ID, Scope, Name, Keywords, Content, Status, Enforced and ReplacedBy
// set; Suppressions are the global Items the Project hides, as <kind>:<id>.
// It comes from the network, so NewViewSource validates it like a file.
type View struct {
	Items        []Article
	Suppressions []string
}

// ViewSource is a Source over a View that fetch returns. It does no I/O of
// its own: fetch is its only input, and the caller owns the transport, so the
// engine holds no client (ADR 0002).
type ViewSource struct {
	fetch func(ctx context.Context) (View, string, error)

	mu  sync.Mutex
	rev string
}

var (
	_ Source     = (*ViewSource)(nil)
	_ Revisioner = (*ViewSource)(nil)
)

// NewViewSource returns a Source that calls fetch on every Load and builds a
// Snapshot from the View it returns. fetch also returns the revision the View
// stands for, which Revision reports after a successful Load.
func NewViewSource(fetch func(ctx context.Context) (View, string, error)) Source {
	return &ViewSource{fetch: fetch}
}

// Revision is the revision fetch returned for the last successful Load, or
// empty before one.
func (s *ViewSource) Revision() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

// Load fetches the View and builds a Snapshot. A fetch error is returned as it
// is. A View that is not valid fails the load: an Item whose Scope is not
// global or one Project's id (an upstream cannot make local Items, or Items
// of two Projects), an id, name, status or reference that is not one a file
// may have, two copies of one Item in a Scope, a reference or lookup that
// does not resolve, or a Project Item that shadows an enforced one. The error
// names an Item as article:<id> and never the upstream.
func (s *ViewSource) Load(ctx context.Context) (*Snapshot, error) {
	if ctx.Err() != nil {
		return nil, errLoadCancelled
	}
	v, rev, err := s.fetch(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := buildView(v)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.rev = rev
	s.mu.Unlock()
	return snap, nil
}

// buildView validates v and builds the Snapshot, ending with the same
// shadowing, replacement and lookup checks loadFS runs.
func buildView(v View) (*Snapshot, error) {
	var global, project []Article
	scope := ""
	seen := map[string]bool{} // lower-cased scope + "\x00" + id
	for _, in := range v.Items {
		a, err := checkViewItem(in)
		if err != nil {
			return nil, err
		}
		if a.Scope != ScopeGlobal {
			if scope != "" && !strings.EqualFold(scope, a.Scope) {
				return nil, errors.New("upstream view: Items of more than one Project")
			}
			scope = a.Scope
		}
		id := strings.ToLower(a.Scope) + "\x00" + strings.ToLower(a.ID)
		if seen[id] {
			return nil, fmt.Errorf("%s: the view holds the Item twice in one Scope", a.file)
		}
		seen[id] = true
		if a.Scope == ScopeGlobal {
			global = append(global, a)
		} else {
			project = append(project, a)
		}
	}
	snap := newSnapshot(global)
	if scope != "" {
		snap.addProject(scope, project)
		snap.viewProject = scope
		if err := snap.hideViewSuppressions(scope, v.Suppressions); err != nil {
			return nil, err
		}
	}
	if err := snap.checkEnforced(); err != nil {
		return nil, err
	}
	if err := snap.checkReplacements(snap.allItems()); err != nil {
		return nil, err
	}
	if err := snap.checkLookups(snap.allItems()); err != nil {
		return nil, err
	}
	return snap, nil
}

// checkViewItem returns a validated copy of one Item of a View. Its file is
// "upstream <ref>", so errors name the Item and never an address.
func checkViewItem(in Article) (Article, error) {
	a := cloneArticle(in)
	a.kind, a.description, a.raw = "", "", nil
	if _, err := parseRef(KindArticle + ":" + a.ID); err != nil {
		return Article{}, fmt.Errorf("upstream view: an Item id %s: %w", quoteRef(a.ID), err)
	}
	a.file = "upstream " + itemRef(&a)
	if a.Scope != ScopeGlobal {
		if err := checkViewScope(a.Scope); err != nil {
			return Article{}, fmt.Errorf("%s: %w", a.file, err)
		}
	}
	if strings.TrimSpace(a.Name) == "" {
		return Article{}, fmt.Errorf("%s: has no name", a.file)
	}
	if len(a.Content) > maxArticleBytes {
		return Article{}, fmt.Errorf("%s: content is larger than %d bytes", a.file, maxArticleBytes)
	}
	switch a.Status {
	case StatusDraft, StatusPublished, StatusDeprecated:
	default:
		return Article{}, fmt.Errorf("%s: status must be draft, published or deprecated", a.file)
	}
	if a.ReplacedBy != "" {
		if _, err := parseRef(a.ReplacedBy); err != nil {
			return Article{}, fmt.Errorf("%s: replaced_by %s: %w", a.file, quoteRef(a.ReplacedBy), err)
		}
	}
	a.Name = strings.TrimSpace(a.Name)
	keywords := a.Keywords[:0]
	for _, k := range a.Keywords {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			keywords = append(keywords, k)
		}
	}
	a.Keywords = keywords
	return a, nil
}

// checkViewScope accepts a Project id, host/org/repo, as a Source's directory
// names it.
func checkViewScope(scope string) error {
	parts := strings.Split(scope, "/")
	if len(scope) > maxProjectIDLen || len(parts) != 3 {
		return errors.New("the Scope must be global or a Project id host/org/repo")
	}
	for depth, p := range parts {
		if err := validProjectSegment(depth, p); err != nil {
			return fmt.Errorf("the Scope is not a Project id: %w", err)
		}
	}
	return nil
}

// hideViewSuppressions records the global Items the exported Project hides.
// The view has already left them out, so this only lets the Project say so.
func (s *Snapshot) hideViewSuppressions(scope string, refs []string) error {
	if len(refs) > maxSuppressions {
		return fmt.Errorf("upstream view: more than %d suppressions", maxSuppressions)
	}
	hidden := map[itemKey]bool{}
	var listed []string
	for _, ref := range refs {
		key, err := parseRef(ref)
		if err != nil {
			return fmt.Errorf("upstream view: suppression %s: %w", quoteRef(ref), err)
		}
		if !hidden[key] {
			hidden[key] = true
			listed = append(listed, key.kind+":"+key.name)
		}
	}
	sort.Strings(listed)
	p, _ := s.lookupProject(scope)
	p.suppressed, p.suppressions = hidden, listed
	s.projects[strings.ToLower(scope)] = p
	return nil
}
