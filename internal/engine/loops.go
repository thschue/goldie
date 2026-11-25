package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// KindLoop is the Kind of a loop definition: the file loops/<name>.md.
const KindLoop = "loop"

// Loop is one Item of Kind loop: a Markdown file whose frontmatter says how a
// repeated task runs (interval or dynamic, the agents and skills it needs,
// when it stops) and whose body is the iteration prompt. A Loop read from a
// Snapshot carries its closure resolved in the view that was asked for.
type Loop struct {
	// Name is the loop's identity, the file name without ".md".
	Name string
	// Description is the frontmatter description, required.
	Description string
	// Scope is ScopeGlobal, ScopeLocal, or the Project's "host/org/repo".
	Scope string
	// Status is StatusDraft, StatusPublished or StatusDeprecated.
	Status string
	// Updated is the frontmatter updated, a day YYYY-MM-DD, or empty.
	Updated string
	// Interval is the frontmatter interval as written, or empty for a dynamic loop.
	Interval string
	// Dynamic is true for a loop that paces itself.
	Dynamic bool
	// Stop is the frontmatter stop condition, required.
	Stop string
	// Content is the iteration prompt, the file's body.
	Content string
	// Agents and Skills are the closure: what the loop names, as it resolves in
	// the view, sorted by name.
	Agents, Skills []LoopRef
	// Digest is the hex SHA-256 over the loop file and every byte of the agent
	// and skill files in the closure.
	Digest string
}

// LoopRef is one agent or skill of a loop's closure and the Scope that
// answered for it.
type LoopRef struct {
	Name  string
	Scope string
}

// loopSpec is what a loop record holds beyond an Article's own fields. It is
// never changed after parsing, so records share it.
type loopSpec struct {
	interval       string
	dynamic        bool
	stop           string
	body           string
	agents, skills []string
}

// A loop is held in the Snapshot as an Article record of kind loop, as an
// agent is: the record carries the lifecycle, so winner and the load checks
// resolve it by its itemKey. Its raw is the whole file. Content stays empty, so
// no `goldie lookup` line in a prompt is checked as one in an article is.

// loadLoops is loadScopeKind for the loops directory of a Scope.
func loadLoops(fsys fs.FS, root, scope, what string) ([]Article, error) {
	return loadScopeKind(fsys, root+"/loops", scope, what+" loops", "loop", parseLoop)
}

// parseLoop parses one loop file. The name is the file's slug and the
// frontmatter name must equal it. Exactly one of interval and dynamic: true
// must be set, and stop must say when to stop. The errors name the key and
// never echo its value.
func parseLoop(name string, raw []byte) (Article, error) {
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return Article{}, err
	}
	if err := checkBody(body); err != nil {
		return Article{}, err
	}
	var meta struct {
		Name          string    `yaml:"name"`
		Description   string    `yaml:"description"`
		Interval      yaml.Node `yaml:"interval"`
		Dynamic       yaml.Node `yaml:"dynamic"`
		Agents        yaml.Node `yaml:"agents"`
		Skills        yaml.Node `yaml:"skills"`
		Stop          yaml.Node `yaml:"stop"`
		lifecycleMeta `yaml:",inline"`
	}
	if err := yaml.Unmarshal(front, &meta); err != nil {
		return Article{}, yamlErr("frontmatter", err)
	}
	switch fm := strings.TrimSpace(meta.Name); {
	case fm == "":
		return Article{}, errors.New("frontmatter has no name")
	case fm != name:
		return Article{}, errors.New("frontmatter name does not match the file name")
	}
	desc := strings.TrimSpace(meta.Description)
	if desc == "" {
		return Article{}, errors.New("frontmatter has no description")
	}
	spec := &loopSpec{body: strings.TrimLeft(string(body), "\n")}
	if spec.interval, spec.dynamic, err = loopTiming(meta.Interval, meta.Dynamic); err != nil {
		return Article{}, err
	}
	stop, ok := scalar(meta.Stop, "!!str")
	if stop = strings.TrimSpace(stop); !ok || stop == "" || strings.ContainsAny(stop, "\r\n") {
		return Article{}, errors.New("stop must be one line saying when the loop ends")
	}
	if err := checkBody([]byte(stop)); err != nil {
		return Article{}, errors.New("stop holds a hidden or control character")
	}
	spec.stop = stop
	if spec.agents, err = loopNames(meta.Agents, "agents"); err != nil {
		return Article{}, err
	}
	if spec.skills, err = loopNames(meta.Skills, "skills"); err != nil {
		return Article{}, err
	}
	a := Article{kind: KindLoop, ID: name, Name: name, description: desc, raw: raw, loop: spec}
	if err := meta.apply(&a); err != nil {
		return Article{}, err
	}
	return a, nil
}

// loopTiming reads interval and dynamic: exactly one of a positive duration
// (time.ParseDuration syntax, such as 30m) and dynamic: true. dynamic: false
// counts as not set.
func loopTiming(interval, dynamic yaml.Node) (string, bool, error) {
	dyn := false
	if present(dynamic) {
		v, ok := scalar(dynamic, "!!bool")
		switch v = strings.ToLower(v); {
		case ok && v == "true":
			dyn = true
		case ok && v == "false":
		default:
			return "", false, errors.New("dynamic must be true or false")
		}
	}
	switch {
	case dyn && present(interval):
		return "", false, errors.New("set interval or dynamic, not both")
	case dyn:
		return "", true, nil
	case !present(interval):
		return "", false, errors.New("set interval or dynamic: true")
	}
	v, ok := scalar(interval, "!!str")
	v = strings.TrimSpace(v)
	// The value is never echoed: time.ParseDuration's error quotes it.
	if d, err := time.ParseDuration(v); !ok || err != nil || d <= 0 {
		return "", false, errors.New("interval must be a positive duration such as 30m")
	}
	return v, false, nil
}

// loopNames reads a list of agent or skill names. A missing or empty key is an
// empty list. A name is a plain name, as a reference's: one segment, no
// duplicate ignoring case.
func loopNames(n yaml.Node, key string) ([]string, error) {
	if !present(n) || (n.Kind == yaml.ScalarNode && n.Tag == "!!null") {
		return nil, nil
	}
	bad := fmt.Errorf("%s must be a list of names", key)
	if n.Kind != yaml.SequenceNode {
		return nil, bad
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range n.Content {
		v, ok := scalar(*c, "!!str")
		if !ok {
			return nil, bad
		}
		// parseRef holds the one rule for a name's characters; the Kind is
		// only a prefix to satisfy it.
		if _, err := parseRef(KindArticle + ":" + v); err != nil || strings.Contains(v, "/") {
			return nil, fmt.Errorf("%s names %s, which is not a plain name", key, quoteRef(v))
		}
		if k := strings.ToLower(v); seen[k] {
			return nil, fmt.Errorf("%s names %s twice", key, quoteRef(v))
		} else {
			seen[k] = true
		}
		out = append(out, v)
	}
	return out, nil
}

// checkLoops fails when an agent or skill a loop names does not resolve, as a
// published Item, in the loop's own view: a Project loop sees its Project then
// global, a global one global only, less what the Project suppresses. all is
// the Items to check, as for checkLookups. The error names the loop's file and
// quotes the missing name, never a content path.
func (s *Snapshot) checkLoops(all []*Article) error {
	for _, a := range all {
		if a.loop == nil {
			continue
		}
		view := s.viewFor(a)
		for _, n := range a.loop.agents {
			if s.resolvePublished(view, keyOf(KindAgent, n)) == nil {
				return fmt.Errorf("%s: agents names %s, which does not resolve to an agent this loop can see", articleFile(a), quoteRef(n))
			}
		}
		for _, n := range a.loop.skills {
			if _, ok := s.skillBundleFor(view, n, ReadOptions{}); !ok {
				return fmt.Errorf("%s: skills names %s, which does not resolve to a skill this loop can see", articleFile(a), quoteRef(n))
			}
		}
	}
	return nil
}

// loopIn builds the public Loop for record a with its closure resolved in
// proj's view under o. It reports false when a name in the closure does not
// resolve there (a Project can suppress an agent a global loop names), so a
// loop that could not be pulled is not served.
func (s *Snapshot) loopIn(proj string, a *Article, o ReadOptions) (Loop, bool) {
	sp := a.loop
	l := Loop{
		Name: a.ID, Description: a.description, Scope: a.Scope, Status: a.Status, Updated: a.Updated,
		Interval: sp.interval, Dynamic: sp.dynamic, Stop: sp.stop, Content: sp.body,
		Agents: []LoopRef{}, Skills: []LoopRef{},
	}
	type agentDep struct {
		ref LoopRef
		raw []byte
	}
	type skillDep struct {
		ref LoopRef
		b   skillBundle
	}
	var agents []agentDep
	for _, n := range sp.agents {
		ag := s.winner(proj, keyOf(KindAgent, n), o)
		if ag == nil {
			return Loop{}, false
		}
		agents = append(agents, agentDep{LoopRef{Name: ag.ID, Scope: ag.Scope}, ag.raw})
	}
	var skills []skillDep
	for _, n := range sp.skills {
		b, ok := s.skillBundleFor(proj, n, o)
		if !ok {
			return Loop{}, false
		}
		skills = append(skills, skillDep{LoopRef{Name: b.name, Scope: b.scope}, b})
	}
	slices.SortFunc(agents, func(a, b agentDep) int { return strings.Compare(a.ref.Name, b.ref.Name) })
	slices.SortFunc(skills, func(a, b skillDep) int { return strings.Compare(a.ref.Name, b.ref.Name) })
	// Each part is length-prefixed, so no two closures hash alike by shifting
	// bytes between neighbours.
	h := newLenHash(sha256.New())
	h.put([]byte(KindLoop))
	h.put(a.raw)
	for _, d := range agents {
		l.Agents = append(l.Agents, d.ref)
		h.put([]byte(KindAgent))
		h.put([]byte(d.ref.Name))
		h.put(d.raw)
	}
	for _, d := range skills {
		l.Skills = append(l.Skills, d.ref)
		h.put([]byte(KindSkill))
		h.put([]byte(d.ref.Name))
		for _, f := range slices.Sorted(maps.Keys(d.b.files)) {
			h.put([]byte(f))
			h.putBool(d.b.exec[f])
			h.put(d.b.files[f])
		}
	}
	l.Digest = hex.EncodeToString(h.Sum(nil))
	return l, true
}

// LoopIn resolves a loop for a Project ("host/org/repo", matched
// case-insensitively), local then Project then global, by winner's rule, and
// resolves its closure in that same view. An empty or unregistered Project
// resolves globally. Drafts are left out unless opts asks for them, in the
// loop and in its closure. A loop whose closure does not resolve in the view
// is not found.
func (s *Snapshot) LoopIn(proj, name string, opts ...ReadOptions) (Loop, bool) {
	o := readOpts(opts)
	if a := s.winner(proj, keyOf(KindLoop, name), o); a != nil {
		return s.loopIn(proj, a, o)
	}
	return Loop{}, false
}

// Loops returns the loops that answer in proj's view, sorted by name: one per
// name, the one LoopIn returns. Drafts are left out unless opts asks for them.
func (s *Snapshot) Loops(proj string, opts ...ReadOptions) []Loop {
	o := readOpts(opts)
	out := []Loop{}
	seen := map[itemKey]bool{}
	for _, list := range s.resolutionOrder(proj, KindLoop) {
		for i := range list {
			key := list[i].key()
			if seen[key] {
				continue
			}
			seen[key] = true
			if a := s.winner(proj, key, o); a != nil {
				if l, ok := s.loopIn(proj, a, o); ok {
					out = append(out, l)
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b Loop) int { return strings.Compare(a.Name, b.Name) })
	return out
}
