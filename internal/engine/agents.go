package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// KindAgent is the Kind of an agent definition: the file agents/<name>.md.
const KindAgent = "agent"

// Agent is one Item of Kind agent: a Markdown file with frontmatter that
// defines a sub-agent. Only name, description and keywords are parsed; every other key
// (tools, model, ...) stays in Raw and is never rejected.
type Agent struct {
	// Name is the agent's identity, the file name without ".md", and equal to
	// the frontmatter name (ADR 0015). Names compare case-insensitively.
	Name string
	// Description is the frontmatter description, required.
	Description string
	// Scope is ScopeGlobal, ScopeLocal, or the Project's "host/org/repo".
	Scope string
	// Status is StatusDraft, StatusPublished (the default) or StatusDeprecated.
	Status string
	// Enforced is the frontmatter enforced: true.
	Enforced bool
	// ReplacedBy is the frontmatter replaced_by as written, or empty.
	ReplacedBy string
	// Updated is the frontmatter updated, a day YYYY-MM-DD, or empty.
	Updated string
	// Raw is the file exactly as written, for pull.
	Raw []byte
}

// Kind is KindAgent.
func (a Agent) Kind() string { return KindAgent }

// An agent is held in the Snapshot as an Article record of kind agent: the
// record carries the lifecycle, so winner, checkEnforced, checkReplacements
// and suppression resolve it by its itemKey, with no second copy of those
// rules. Agents live in their own lists, never in the article ones, so
// Articles, Search and the article readers do not see them.

// loadAgents reads every .md file directly under dir into agent records of the
// given scope. A missing dir yields none. Agents sit flat in dir: a
// subdirectory is an error, and a file that is not a regular .md file is
// skipped, as for articles.
func loadAgents(fsys fs.FS, dir, scope string) ([]Article, error) {
	return loadKindFiles(fsys, dir, scope, "agent", parseAgent)
}

// loadKindFiles is loadAgents for any Kind kept as one flat directory of .md
// files: what names the Kind in errors, and parse reads one file by its slug.
func loadKindFiles(fsys fs.FS, dir, scope, what string, parse func(name string, raw []byte) (Article, error)) ([]Article, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", dir, readErr(err))
	}
	var items []Article
	seen := map[string]string{} // lower-cased name -> file path
	for _, e := range entries {
		p := dir + "/" + e.Name()
		if e.IsDir() {
			return nil, fmt.Errorf("%s: is a directory; %ss sit directly in %s", p, what, dir)
		}
		if !e.Type().IsRegular() || path.Ext(p) != ".md" {
			continue
		}
		raw, err := readLimited(fsys, p, what)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if other, dup := seen[strings.ToLower(name)]; dup {
			return nil, fmt.Errorf("%s and %s: %s names differ only in case", other, p, what)
		}
		seen[strings.ToLower(name)] = p
		a, err := parse(name, raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		a.Scope = scope
		a.file = p
		items = append(items, a)
	}
	return items, nil
}

// parseAgent parses one agent file. The name is the file's slug, and the
// frontmatter name must equal it exactly. As for articles, the file name itself
// is not checked for hidden runes: an agent no reference can spell is still
// served by its own name, and a reference to it fails the load.
func parseAgent(name string, raw []byte) (Article, error) {
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
		Keywords      yaml.Node `yaml:"keywords"`
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
	a := Article{kind: KindAgent, ID: name, Name: name, description: desc, raw: raw, Keywords: lenientKeywords(meta.Keywords)}
	if err := meta.apply(&a); err != nil {
		return Article{}, err
	}
	return a, nil
}

// toAgent copies an agent record into the public type.
func toAgent(a Article) Agent {
	return Agent{
		Name: a.ID, Description: a.description, Scope: a.Scope, Status: a.Status, Updated: a.Updated,
		Enforced: a.Enforced, ReplacedBy: a.ReplacedBy, Raw: append([]byte(nil), a.raw...),
	}
}

// AgentIn resolves an agent for a Project ("host/org/repo", matched
// case-insensitively), local then Project then global, by winner's rule. An
// empty or unregistered Project resolves globally. Drafts are left out unless
// opts asks for them. The result is a copy.
func (s *Snapshot) AgentIn(proj, name string, opts ...ReadOptions) (Agent, bool) {
	if a := s.winner(proj, keyOf(KindAgent, name), readOpts(opts)); a != nil {
		return toAgent(*a), true
	}
	return Agent{}, false
}

// Agents returns the agents that answer in proj's view, sorted by name: one
// copy per name, the one AgentIn returns. Drafts are left out unless opts asks
// for them.
func (s *Snapshot) Agents(proj string, opts ...ReadOptions) []Agent {
	o := readOpts(opts)
	out := []Agent{}
	seen := map[itemKey]bool{}
	for _, list := range s.resolutionOrder(proj, KindAgent) {
		for i := range list {
			key := list[i].key()
			if seen[key] {
				continue
			}
			seen[key] = true
			if a := s.winner(proj, key, o); a != nil {
				out = append(out, toAgent(*a))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
