package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// KindInstruction is the Kind of an instruction clause: the file
// instructions/<name>.md. Clauses are not yet nameable in a reference (they
// are not in knownKinds); they resolve by name through winner, like agents. A
// clause is kept out of allItems: it has no replaced_by and no lookups, and a
// Project clause that shadows an enforced global one does not fail the load,
// winner serves the enforced one.
const KindInstruction = "instruction"

// instructionsDir is where a Source keeps its global clauses, relative to its
// root. A Project's are in <project dir>/instructions and the local Scope's in
// .goldie/instructions.
const instructionsDir = "global/instructions"

// The targets a clause may name: the file a block is written into.
const (
	TargetClaude = "claude"
	TargetAgents = "agents"
)

// InstructionVars are the values of the templated variables of a clause.
// {{project}} is the Project given to Instructions.
type InstructionVars struct {
	// MCPName is the value of {{mcp_name}}.
	MCPName string
	// CLI is the value of {{cli}}.
	CLI string
}

// defaultClause applies for a Project and target when no clause resolves for
// them. It uses the same templating as a clause.
const defaultClause = "This repository ({{project}}) uses Goldie for its golden paths.\n" +
	"Before you build, change or review something the team may have a standard for,\n" +
	"ask Goldie first, through the {{mcp_name}} MCP server or with `{{cli}} lookup <id>`.\n" +
	"Follow the golden path it returns; say so when you deviate from it."

// parseClause parses one clause file. The name is the file's slug and the
// frontmatter name must equal it. The body must hold no placeholder but the
// three known ones, so a bad clause fails the load whatever its status. It is
// held as a record of kind KindInstruction whose raw is the body.
func parseClause(name string, raw []byte) (Article, error) {
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return Article{}, err
	}
	var meta struct {
		Name          string    `yaml:"name"`
		Order         int       `yaml:"order"`
		Targets       yaml.Node `yaml:"targets"`
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
	var targets []string
	if err := meta.Targets.Decode(&targets); err != nil || len(targets) == 0 {
		return Article{}, errors.New("targets must be a list of claude and agents")
	}
	for _, t := range targets {
		if t != TargetClaude && t != TargetAgents {
			return Article{}, errors.New("targets must be a list of claude and agents")
		}
	}
	if err := checkPlaceholders(string(body)); err != nil {
		return Article{}, err
	}
	a := Article{kind: KindInstruction, ID: name, Name: name, raw: body, order: meta.Order, targets: targets}
	if err := meta.apply(&a); err != nil {
		return Article{}, err
	}
	if a.ReplacedBy != "" {
		return Article{}, errors.New("an instruction clause cannot name replaced_by")
	}
	return a, nil
}

var clauseVars = []string{"{{project}}", "{{mcp_name}}", "{{cli}}"}

// checkPlaceholders fails on any {{ in s that does not open exactly one of the
// three known variables. The error never echoes the text.
func checkPlaceholders(s string) error {
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			return nil
		}
		s = s[i:]
		known := false
		for _, v := range clauseVars {
			if strings.HasPrefix(s, v) {
				s, known = s[len(v):], true
				break
			}
		}
		if !known {
			return errors.New("holds a {{placeholder}} that is not {{project}}, {{mcp_name}} or {{cli}}")
		}
	}
}

// cleanValue makes a substituted value one visible line: every hidden rune,
// line break and Unicode line or paragraph separator becomes a space.
func cleanValue(v string) string {
	return strings.Map(func(r rune) rune {
		if HiddenRune(r) || r == 0x2028 || r == 0x2029 {
			return ' '
		}
		return r
	}, v)
}

// Instructions renders the block for proj and target ("claude" or "agents"):
// the clauses that resolve in proj's view and name target, each by winner's
// rule, sorted by order then name, joined by a blank line, with the variables
// replaced. Only when none does, the built-in default clause is the block. The
// digest is the hex SHA-256 of the block. The variables are replaced in one
// pass, after cleaning, so a value is never expanded again.
func (s *Snapshot) Instructions(proj, target string, vars InstructionVars) (block, digest string) {
	var chosen []*Article
	seen := map[itemKey]bool{}
	for _, list := range s.resolutionOrder(proj, KindInstruction) {
		for i := range list {
			key := list[i].key()
			if seen[key] {
				continue
			}
			seen[key] = true
			if a := s.winner(proj, key, ReadOptions{}); a != nil && slices.Contains(a.targets, target) {
				chosen = append(chosen, a)
			}
		}
	}
	sort.Slice(chosen, func(i, j int) bool {
		if chosen[i].order != chosen[j].order {
			return chosen[i].order < chosen[j].order
		}
		return strings.ToLower(chosen[i].ID) < strings.ToLower(chosen[j].ID)
	})
	bodies := make([]string, 0, len(chosen))
	for _, a := range chosen {
		bodies = append(bodies, strings.TrimSpace(string(a.raw)))
	}
	if len(bodies) == 0 {
		bodies = append(bodies, defaultClause)
	}
	r := strings.NewReplacer(
		"{{project}}", cleanValue(proj),
		"{{mcp_name}}", cleanValue(vars.MCPName),
		"{{cli}}", cleanValue(vars.CLI),
	)
	block = r.Replace(strings.Join(bodies, "\n\n"))
	sum := sha256.Sum256([]byte(block))
	return block, hex.EncodeToString(sum[:])
}

// addInstructions registers the global clauses. It is called only while loading.
func (s *Snapshot) addInstructions(items []Article) {
	sortArticles(items)
	s.instructions = items
	s.index.add(items)
}

// addProjectInstructions registers a Project's clauses. It is called only
// while loading, after addProject.
func (s *Snapshot) addProjectInstructions(scope string, items []Article) {
	p, _ := s.lookupProject(scope)
	sortArticles(items)
	p.instructions = items
	p.index.add(items)
	s.projects[strings.ToLower(scope)] = p
}
