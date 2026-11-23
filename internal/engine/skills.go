package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// KindSkill is the Kind of a skill bundle: the directory skills/<name>/.
const KindSkill = "skill"

// skillsDir is where a Source keeps its global skill bundles, relative to its
// root. A Project's are in <project dir>/skills and the local Scope's in
// .goldie/skills.
const skillsDir = "global/skills"

// skillFileName is the file every bundle must have at its top.
const skillFileName = "SKILL.md"

// Skill is one skill bundle: the directory skills/<name>/ with a SKILL.md and
// its supporting files. The nearest Scope wins with its whole bundle.
type Skill struct {
	// Name is the bundle's directory name.
	Name string
	// Scope is ScopeGlobal, ScopeLocal, or the Project's "host/org/repo".
	Scope string
	// Files lists the bundle's regular files as slash paths relative to the
	// bundle, sorted, SKILL.md included.
	Files []string
	// Sizes is each file's size in bytes, keyed by the paths in Files.
	Sizes map[string]int
}

// skillBundle is a loaded bundle: every file's bytes as written.
type skillBundle struct {
	// name is the directory's name as written; the maps key it lower-cased.
	name  string
	scope string
	files map[string][]byte
	// exec marks the files with an executable bit, keyed as files is.
	exec map[string]bool
	// keywords are the keywords in the frontmatter of SKILL.md, lower case.
	keywords []string
}

// skillKeywords reads the keywords from the frontmatter of a SKILL.md. A
// SKILL.md without well-formed frontmatter has none: the file is served as
// written either way, so it never fails a load.
func skillKeywords(raw []byte) []string {
	front, _, err := splitFrontmatter(raw)
	if err != nil {
		return nil
	}
	var meta struct {
		Keywords yaml.Node `yaml:"keywords"`
	}
	if yaml.Unmarshal(front, &meta) != nil {
		return nil
	}
	return lenientKeywords(meta.Keywords)
}

// errNoSkillFile is what SkillFile returns for anything that is not a file of
// the bundle that answers. It names nothing, so it cannot leak a path.
var errNoSkillFile = errors.New("not a file of the skill")

// loadSkills reads each skill bundle directly under dir. A missing dir yields
// none. An entry of dir that is a symlink fails the load, a regular file is
// skipped, and a bundle without SKILL.md at its top fails the load. Inside a
// bundle only regular files are kept: a symlink is never followed. Every file
// is read through readLimited, so the article size cap applies. The result is
// keyed by lower-cased name, and two directories whose names differ only in
// case fail the load, as two agent files do.
func loadSkills(fsys fs.FS, dir, scope, what string) (map[string]skillBundle, error) {
	if exists, err := requireDir(fsys, dir, what); err != nil || !exists {
		return nil, err
	}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, readErr(err))
	}
	bundles := map[string]skillBundle{}
	seen := map[string]string{}
	for _, e := range entries {
		p := dir + "/" + e.Name()
		if e.Type()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s: is a symlink; a skill must be a directory", p)
		}
		if !e.IsDir() {
			continue
		}
		key := strings.ToLower(e.Name())
		if other, dup := seen[key]; dup {
			return nil, fmt.Errorf("%s and %s: skill names differ only in case", other, p)
		}
		seen[key] = p
		files := map[string][]byte{}
		exec := map[string]bool{}
		if err := readBundle(fsys, p, "", files, exec); err != nil {
			return nil, err
		}
		skill, ok := files[skillFileName]
		if !ok {
			return nil, fmt.Errorf("%s: has no %s", p, skillFileName)
		}
		bundles[key] = skillBundle{name: e.Name(), scope: scope, files: files, exec: exec, keywords: skillKeywords(skill)}
	}
	return bundles, nil
}

// readBundle reads the regular files under dir/rel into files, keyed by their
// path relative to the bundle.
func readBundle(fsys fs.FS, dir, rel string, files map[string][]byte, exec map[string]bool) error {
	cur := dir
	if rel != "" {
		cur = dir + "/" + rel
	}
	entries, err := fs.ReadDir(fsys, cur)
	if err != nil {
		return fmt.Errorf("%s: %w", cur, readErr(err))
	}
	for _, e := range entries {
		name := e.Name()
		if rel != "" {
			name = rel + "/" + name
		}
		switch {
		case e.IsDir():
			if err := readBundle(fsys, dir, name, files, exec); err != nil {
				return err
			}
		case e.Type().IsRegular():
			p := dir + "/" + name
			raw, err := readLimited(fsys, p, "skill file")
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			files[name] = raw
			if info, err := e.Info(); err == nil && info.Mode().Perm()&0o111 != 0 {
				exec[name] = true
			}
		}
	}
	return nil
}

// skillScopes lists the bundle maps that can answer in proj's view, nearest
// Scope first: local, then Project, then global. Both skillBundleFor and
// Skills walk it, so a get and a list cannot disagree on the order.
func (s *Snapshot) skillScopes(proj string) []map[string]skillBundle {
	var scopes []map[string]skillBundle
	if s.local != nil {
		scopes = append(scopes, s.local.skills)
	}
	if p, ok := s.lookupProject(proj); ok {
		scopes = append(scopes, p.skills)
	}
	return append(scopes, s.skills)
}

// skillBundleFor finds the bundle that answers for name in proj's view.
func (s *Snapshot) skillBundleFor(proj, name string) (skillBundle, bool) {
	key := strings.ToLower(name)
	for _, scope := range s.skillScopes(proj) {
		if b, ok := scope[key]; ok {
			return b, true
		}
	}
	return skillBundle{}, false
}

// SkillIn resolves a skill bundle for a Project ("host/org/repo") by name, both
// matched case-insensitively; the Skill carries the directory's own spelling.
// Local then Project then global. An empty or
// unregistered Project resolves globally.
func (s *Snapshot) SkillIn(proj, name string) (Skill, bool) {
	b, ok := s.skillBundleFor(proj, name)
	if !ok {
		return Skill{}, false
	}
	files := make([]string, 0, len(b.files))
	sizes := make(map[string]int, len(b.files))
	for f, raw := range b.files {
		files = append(files, f)
		sizes[f] = len(raw)
	}
	slices.Sort(files)
	return Skill{Name: b.name, Scope: b.scope, Files: files, Sizes: sizes}, true
}

// Skills returns the skills that answer in proj's view, sorted by name: one
// per name, the bundle SkillIn returns.
func (s *Snapshot) Skills(proj string) []Skill {
	names := map[string]bool{}
	for _, scope := range s.skillScopes(proj) {
		for n := range scope {
			names[n] = true
		}
	}
	out := make([]Skill, 0, len(names))
	for n := range names {
		if sk, ok := s.SkillIn(proj, n); ok {
			out = append(out, sk)
		}
	}
	slices.SortFunc(out, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// SkillFile returns a copy of one file of the bundle that answers for name in
// proj's view, as written. A path that is not a file of that bundle, whether
// missing, absolute, leaving the bundle with "..", or a symlink (never
// loaded), is an error.
func (s *Snapshot) SkillFile(proj, name, file string) ([]byte, error) {
	b, ok := s.skillBundleFor(proj, name)
	if !ok {
		return nil, errNoSkillFile
	}
	raw, ok := b.files[file]
	if !ok {
		return nil, errNoSkillFile
	}
	return append([]byte(nil), raw...), nil
}
