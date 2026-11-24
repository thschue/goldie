package engine

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// KindDecision is the Kind of a decision record: the file decisions/<name>.md.
const KindDecision = "decision"

// The status values of a decision. Only an accepted decision is served by
// default; a missing or unknown status fails the load.
const (
	StatusProposed   = "proposed"
	StatusAccepted   = "accepted"
	StatusRejected   = "rejected"
	StatusSuperseded = "superseded"
)

// Decision is one Item of Kind decision: a Markdown file recording a choice.
type Decision struct {
	// Name is the decision's identity, the file name without ".md". Title is
	// for display only.
	Name  string
	Title string
	// Scope is ScopeGlobal, ScopeLocal, or the Project's "host/org/repo".
	Scope string
	// Status is proposed, accepted, rejected, deprecated or superseded.
	Status string
	// Date is the frontmatter date, YYYY-MM-DD.
	Date string
	// Enforced is the frontmatter enforced: true.
	Enforced bool
	// Supersedes lists the references the frontmatter names, as written.
	Supersedes []string
	// SupersededBy is the reference of the decision that replaces this one, or empty.
	SupersededBy string
	Keywords     []string
	// Content is the file's body.
	Content string
}

// decisionSpec is what a decision record holds beyond an Article's own fields.
type decisionSpec struct {
	date       string
	supersedes []string
}

// A decision is held in the Snapshot as an Article record of kind decision, as
// a loop is: superseded_by is the record's ReplacedBy, so checkReplacements
// resolves and checks it, and the record's title is its Name.

// loadDecisions is loadScopeKind for the decisions directory of a Scope.
func loadDecisions(fsys fs.FS, root, scope, what string) ([]Article, error) {
	return loadScopeKind(fsys, root+"/decisions", scope, what+" decisions", "decision", parseDecision)
}

// parseDecision parses one decision file. The identity is the file stem; title,
// status and date are required. The errors name the key and never echo its value.
func parseDecision(name string, raw []byte) (Article, error) {
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return Article{}, err
	}
	if err := checkBody(body); err != nil {
		return Article{}, err
	}
	var meta struct {
		Title         string    `yaml:"title"`
		Date          yaml.Node `yaml:"date"`
		Supersedes    yaml.Node `yaml:"supersedes"`
		SupersededBy  yaml.Node `yaml:"superseded_by"`
		Keywords      yaml.Node `yaml:"keywords"`
		lifecycleMeta `yaml:",inline"`
	}
	if err := yaml.Unmarshal(front, &meta); err != nil {
		return Article{}, yamlErr("frontmatter", err)
	}
	title := strings.TrimSpace(meta.Title)
	if title == "" {
		return Article{}, errors.New("frontmatter has no title")
	}
	if err := checkBody([]byte(title)); err != nil {
		return Article{}, errors.New("title holds a hidden or control character")
	}
	status, ok := scalar(meta.Status, "!!str")
	if !ok || !slices.Contains([]string{StatusProposed, StatusAccepted, StatusRejected, StatusDeprecated, StatusSuperseded}, status) {
		return Article{}, errors.New("status must be proposed, accepted, rejected, deprecated or superseded")
	}
	date, ok := scalar(meta.Date, "!!timestamp")
	if _, err := time.Parse(time.DateOnly, date); !ok || err != nil {
		return Article{}, errors.New("date must be a day such as 2023-05-02")
	}
	spec := &decisionSpec{date: date}
	if spec.supersedes, err = decisionRefs(meta.Supersedes); err != nil {
		return Article{}, err
	}
	by := ""
	if present(meta.SupersededBy) {
		v, ok := scalar(meta.SupersededBy, "!!str")
		if key, err := parseRef(v); !ok || err != nil || key.kind != KindDecision {
			return Article{}, errors.New("superseded_by must be a reference of the form decision:<name>")
		}
		by = v
	}
	switch {
	case status == StatusSuperseded && by == "":
		return Article{}, errors.New("status superseded needs superseded_by")
	case status != StatusSuperseded && by != "":
		return Article{}, errors.New("superseded_by needs status superseded")
	}
	// lifecycleMeta.apply owns enforced; the decision statuses and the
	// replacement are read above, so apply sees neither.
	meta.Status, meta.ReplacedBy = yaml.Node{}, yaml.Node{}
	a := Article{kind: KindDecision, ID: name, Name: title, Content: strings.TrimLeft(string(body), "\n"),
		Keywords: lenientKeywords(meta.Keywords), raw: raw, decision: spec}
	if err := meta.apply(&a); err != nil {
		return Article{}, err
	}
	a.Status, a.ReplacedBy = status, by
	return a, nil
}

// decisionRefs reads supersedes: a list of decision references.
func decisionRefs(n yaml.Node) ([]string, error) {
	if !present(n) || (n.Kind == yaml.ScalarNode && n.Tag == "!!null") {
		return nil, nil
	}
	bad := errors.New("supersedes must be a list of references of the form decision:<name>")
	if n.Kind != yaml.SequenceNode {
		return nil, bad
	}
	var out []string
	for _, c := range n.Content {
		v, ok := scalar(*c, "!!str")
		if key, err := parseRef(v); !ok || err != nil || key.kind != KindDecision {
			return nil, bad
		}
		out = append(out, v)
	}
	return out, nil
}

// toDecision copies a decision record into the public type.
func toDecision(a Article) Decision {
	return Decision{
		Name: a.ID, Title: a.Name, Scope: a.Scope, Status: a.Status, Date: a.decision.date,
		Enforced: a.Enforced, Supersedes: slices.Clone(a.decision.supersedes), SupersededBy: a.ReplacedBy,
		Keywords: slices.Clone(a.Keywords), Content: a.Content,
	}
}

// DecisionIn resolves a decision for a Project ("host/org/repo", matched
// case-insensitively), local then Project then global, by winner's rule. An
// empty or unregistered Project resolves globally. Only an accepted decision is
// served unless opts asks for drafts, which serves every status.
func (s *Snapshot) DecisionIn(proj, name string, opts ...ReadOptions) (Decision, bool) {
	if a := s.winner(proj, keyOf(KindDecision, name), readOpts(opts)); a != nil {
		return toDecision(*a), true
	}
	return Decision{}, false
}

// Decisions returns the decisions that answer in proj's view, sorted by name:
// one per name, the one DecisionIn returns.
func (s *Snapshot) Decisions(proj string, opts ...ReadOptions) []Decision {
	o := readOpts(opts)
	out := []Decision{}
	seen := map[itemKey]bool{}
	for _, list := range s.resolutionOrder(proj, KindDecision) {
		for i := range list {
			key := list[i].key()
			if seen[key] {
				continue
			}
			seen[key] = true
			if a := s.winner(proj, key, o); a != nil {
				out = append(out, toDecision(*a))
			}
		}
	}
	slices.SortFunc(out, func(a, b Decision) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// DecisionChain returns the supersession chain of a decision, oldest to
// newest, including the decision itself. The decision resolves in proj's view
// as DecisionIn does, but in every status: a chain is history. The links are
// resolved in the same view. The predecessors of one decision (those it
// supersedes and those that name it in superseded_by) are ordered by date,
// then name. The bool is false when no decision of that name resolves.
func (s *Snapshot) DecisionChain(proj, name string) ([]Decision, bool) {
	all := s.Decisions(proj, ReadOptions{Drafts: true})
	byName := make(map[string]Decision, len(all))
	for _, d := range all {
		byName[d.Name] = d
	}
	start, ok := byName[name]
	if !ok {
		return nil, false
	}
	const prefix = "decision:"
	predecessors := func(d Decision) []Decision {
		var out []Decision
		for _, o := range all {
			if o.SupersededBy == prefix+d.Name || slices.Contains(d.Supersedes, prefix+o.Name) {
				out = append(out, o)
			}
		}
		slices.SortFunc(out, func(a, b Decision) int {
			if c := strings.Compare(a.Date, b.Date); c != 0 {
				return c
			}
			return strings.Compare(a.Name, b.Name)
		})
		return out
	}
	var chain []Decision
	seen := map[string]bool{}
	// visit appends d after its predecessors; seen also stops a cycle.
	var visit func(d Decision)
	visit = func(d Decision) {
		if seen[d.Name] {
			return
		}
		seen[d.Name] = true
		for _, p := range predecessors(d) {
			visit(p)
		}
		chain = append(chain, d)
	}
	visit(start)
	for cur := start; ; {
		next, ok := byName[strings.TrimPrefix(cur.SupersededBy, prefix)]
		if !ok || seen[next.Name] {
			break
		}
		visit(next)
		cur = next
	}
	return chain, true
}
