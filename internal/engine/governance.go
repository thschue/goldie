package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// The lifecycle values of an Item's frontmatter status (CONTEXT: Status).
// An Item without status is published.
const (
	StatusDraft      = "draft"
	StatusPublished  = "published"
	StatusDeprecated = "deprecated"
)

// itemKey is an Item's identity, Kind plus name (ADR 0015). Both parts are
// lower-cased by keyOf, so comparing keys compares identities ignoring case.
type itemKey struct{ kind, name string }

func keyOf(kind, name string) itemKey {
	return itemKey{kind: strings.ToLower(kind), name: strings.ToLower(name)}
}

func (a Article) key() itemKey { return keyOf(KindArticle, a.ID) }

// published reports whether the Item is served by default: every status but
// draft. A deprecated Item is still served.
func (a Article) published() bool { return a.Status != StatusDraft }

// lifecycleMeta is the governance part of an Item's frontmatter. The fields are
// nodes, not typed values, because yaml.v3 decodes yes, on and "true" into a
// bool, and those must fail the load.
type lifecycleMeta struct {
	Status     yaml.Node `yaml:"status"`
	Enforced   yaml.Node `yaml:"enforced"`
	ReplacedBy yaml.Node `yaml:"replaced_by"`
}

// apply validates the lifecycle frontmatter and sets it on a. The errors name
// the field but never echo its value, which may carry anything.
func (m lifecycleMeta) apply(a *Article) error {
	a.Status = StatusPublished
	if present(m.Status) {
		v, ok := scalar(m.Status, "!!str")
		if !ok || (v != StatusDraft && v != StatusPublished && v != StatusDeprecated) {
			return errors.New("status must be draft, published or deprecated")
		}
		a.Status = v
	}
	if present(m.Enforced) {
		v, ok := scalar(m.Enforced, "!!bool")
		switch v = strings.ToLower(v); {
		case ok && v == "true":
			a.Enforced = true
		case ok && v == "false":
		default:
			return errors.New("enforced must be true or false")
		}
	}
	if present(m.ReplacedBy) {
		v, ok := scalar(m.ReplacedBy, "!!str")
		if !ok {
			return errors.New("replaced_by must be a reference of the form <kind>:<name>")
		}
		if _, err := parseRef(v); err != nil {
			return fmt.Errorf("replaced_by %s: %w", quoteRef(v), err)
		}
		a.ReplacedBy = v
	}
	return nil
}

// present reports whether the field was written at all; an empty value such as
// "status:" is present and fails validation.
func present(n yaml.Node) bool { return n.Kind != 0 }

// scalar returns a scalar node's value when its resolved tag is tag.
func scalar(n yaml.Node, tag string) (string, bool) {
	if n.Kind != yaml.ScalarNode || n.Tag != tag {
		return "", false
	}
	return n.Value, true
}

// maxRefBytes caps a reference, Kind and name together, like the Project id
// cap in projectDirs.
const maxRefBytes = 256

// maxQuotedRefRunes is how much of a reference an error message quotes.
const maxQuotedRefRunes = 64

// parseRef parses an Item reference, <kind>:<name> (ADR 0021), of at most
// maxRefBytes: a known Kind in lower-case letters, then a slash-separated name
// whose segments are neither empty nor dot segments. The name holds only
// printable characters other than spaces (no Unicode space, control, format or
// bidi character), and no colon or backslash.
func parseRef(ref string) (itemKey, error) {
	if len(ref) > maxRefBytes {
		return itemKey{}, fmt.Errorf("is longer than %d bytes", maxRefBytes)
	}
	kind, name, ok := strings.Cut(ref, ":")
	if !ok || kind == "" || name == "" {
		return itemKey{}, errors.New("is not of the form <kind>:<name>")
	}
	if strings.Trim(kind, "abcdefghijklmnopqrstuvwxyz") != "" {
		return itemKey{}, errors.New("has a kind that is not lower-case letters")
	}
	if kind != KindArticle {
		return itemKey{}, errors.New("has a kind goldie does not know; the known kind is article")
	}
	for _, r := range name {
		if r == ':' || r == '\\' || r == ' ' || r == utf8.RuneError || !unicode.IsPrint(r) {
			return itemKey{}, errors.New("has a name with a space, control, format, colon or backslash character")
		}
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return itemKey{}, errors.New("has a name with an empty, . or .. segment")
		}
	}
	return keyOf(kind, name), nil
}

// quoteRef renders a reference for an error message: at most
// maxQuotedRefRunes runes, then an ellipsis. Quoting escapes control
// characters, and redact strips anything that looks like a credential, path or
// address, since the message reaches sync status and logs.
func quoteRef(ref string) string {
	if utf8.RuneCountInString(ref) > maxQuotedRefRunes {
		ref = string([]rune(ref)[:maxQuotedRefRunes]) + "…"
	}
	return redact(strconv.Quote(ref))
}

// articleFile is the path of a's file relative to the Source root.
func articleFile(a *Article) string {
	if a.Scope == ScopeGlobal {
		return articlesDir + "/" + a.ID + ".md"
	}
	return projectsDir + "/" + a.Scope + "/articles/" + a.ID + ".md"
}

// checkReplacements fails when a replaced_by does not resolve in its
// referrer's view, names the Item itself, names a draft or is part of a cycle.
// A global Item sees global only; a project Item sees its Project, then global.
func (s *Snapshot) checkReplacements() error {
	all := s.allArticles()
	next := make(map[*Article]*Article, len(all))
	for _, a := range all {
		if a.ReplacedBy == "" {
			continue
		}
		key, err := parseRef(a.ReplacedBy)
		if err != nil { // parseArticle has already checked it
			return fmt.Errorf("%s: replaced_by %s: %w", articleFile(a), quoteRef(a.ReplacedBy), err)
		}
		view := a.Scope
		if view == ScopeGlobal {
			view = ""
		}
		target := s.resolve(view, key)
		switch {
		case target == nil:
			return fmt.Errorf("%s: replaced_by %s does not resolve to an Item this one can see", articleFile(a), quoteRef(a.ReplacedBy))
		case target == a:
			return fmt.Errorf("%s: replaced_by names the Item itself", articleFile(a))
		case !target.published():
			return fmt.Errorf("%s: replaced_by %s names a draft", articleFile(a), quoteRef(a.ReplacedBy))
		}
		next[a] = target
	}
	// Follow each chain once: an Item already known to end is not walked again.
	const (
		walking = 1
		ends    = 2
	)
	state := make(map[*Article]int, len(next))
	for _, start := range all {
		var path []*Article
		for a := start; a != nil && state[a] != ends; a = next[a] {
			if state[a] == walking {
				return fmt.Errorf("%s: replaced_by forms a cycle", articleFile(a))
			}
			state[a] = walking
			path = append(path, a)
		}
		for _, a := range path {
			state[a] = ends
		}
	}
	return nil
}

// checkEnforced fails when a Project article shadows a published enforced
// global Item (ADR 0021). A deprecated enforced Item still enforces; a draft
// does not, and a draft Project copy is left out of Resolution (#353), so it
// shadows nothing. Identity ignores case (ADR 0015).
func (s *Snapshot) checkEnforced() error {
	for _, a := range s.allArticles() {
		if a.Scope == ScopeGlobal || !a.published() {
			continue
		}
		if g := s.index[a.key()]; g != nil && g.Enforced && g.published() {
			return fmt.Errorf("%s shadows the enforced Item %s", articleFile(a), articleFile(g))
		}
	}
	return nil
}

// allArticles lists pointers to every Article in the Snapshot, global first,
// then each Project's in Project order, so errors are deterministic.
func (s *Snapshot) allArticles() []*Article {
	var out []*Article
	for i := range s.articles {
		out = append(out, &s.articles[i])
	}
	for _, id := range s.Projects() {
		p, _ := s.lookupProject(id)
		for i := range p.articles {
			out = append(out, &p.articles[i])
		}
	}
	return out
}
