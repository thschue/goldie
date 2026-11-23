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
// printable characters other than spaces: no Unicode space, no private-use or
// unassigned code point, no rune HiddenRune names (control, format, bidi and
// the invisible fillers), and no colon or backslash.
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
		if r == ':' || r == '\\' || r == ' ' || r == utf8.RuneError || !unicode.IsPrint(r) || HiddenRune(r) {
			return itemKey{}, errors.New("has a name with a space, colon, backslash, or hidden or non-printing character")
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
// characters and every other rune HiddenRune names, such as a Hangul filler
// strconv.Quote leaves raw, and redact strips anything that looks like a
// credential, path or address, since the message reaches sync status and logs.
func quoteRef(ref string) string {
	if utf8.RuneCountInString(ref) > maxQuotedRefRunes {
		ref = string([]rune(ref)[:maxQuotedRefRunes]) + "…"
	}
	return redact(escapeHidden(strconv.Quote(ref)))
}

// escapeHidden rewrites each rune HiddenRune names in a quoted string as a \u
// or \U escape. strconv.Quote has escaped everything else that needs it, so
// the result is still a valid quoted string.
func escapeHidden(quoted string) string {
	if !strings.ContainsFunc(quoted, HiddenRune) {
		return quoted
	}
	var b strings.Builder
	for _, r := range quoted {
		switch {
		case !HiddenRune(r):
			b.WriteRune(r)
		case r > 0xffff:
			fmt.Fprintf(&b, `\U%08x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// articleFile is the path of a's file relative to the Source root, as
// loadArticles found it.
func articleFile(a *Article) string { return a.file }

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
		if key := a.key(); s.enforces(key) {
			return fmt.Errorf("%s shadows the enforced Item %s", articleFile(a), articleFile(s.index[key]))
		}
	}
	return nil
}

// enforces reports whether the global Item key is published and enforced, so
// no Project Item shadows it (ADR 0021). checkEnforced fails a load in which a
// published Project copy does; reads skip a draft copy, which only the drafts
// view would otherwise let shadow it (#359).
func (s *Snapshot) enforces(key itemKey) bool {
	g := s.index[key]
	return g != nil && g.Enforced && g.published()
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
