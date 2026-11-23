package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

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
}

// skillBundle is a loaded bundle: every file's bytes as written.
type skillBundle struct {
	scope string
	files map[string][]byte
}

// errNoSkillFile is what SkillFile returns for anything that is not a file of
// the bundle that answers. It names nothing, so it cannot leak a path.
var errNoSkillFile = errors.New("not a file of the skill")

// loadSkills reads each skill bundle directly under dir. A missing dir yields
// none. An entry of dir that is a symlink fails the load, a regular file is
// skipped, and a bundle without SKILL.md at its top fails the load. Inside a
// bundle only regular files are kept: a symlink is never followed. Every file
// is read through readLimited, so the article size cap applies.
func loadSkills(fsys fs.FS, dir, scope, what string) (map[string]skillBundle, error) {
	if exists, err := requireDir(fsys, dir, what); err != nil || !exists {
		return nil, err
	}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, readErr(err))
	}
	bundles := map[string]skillBundle{}
	for _, e := range entries {
		p := dir + "/" + e.Name()
		if e.Type()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s: is a symlink; a skill must be a directory", p)
		}
		if !e.IsDir() {
			continue
		}
		files := map[string][]byte{}
		if err := readBundle(fsys, p, "", files); err != nil {
			return nil, err
		}
		if _, ok := files[skillFileName]; !ok {
			return nil, fmt.Errorf("%s: has no %s", p, skillFileName)
		}
		bundles[e.Name()] = skillBundle{scope: scope, files: files}
	}
	return bundles, nil
}

// readBundle reads the regular files under dir/rel into files, keyed by their
// path relative to the bundle.
func readBundle(fsys fs.FS, dir, rel string, files map[string][]byte) error {
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
			if err := readBundle(fsys, dir, name, files); err != nil {
				return err
			}
		case e.Type().IsRegular():
			p := dir + "/" + name
			raw, err := readLimited(fsys, p, "skill file")
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			files[name] = raw
		}
	}
	return nil
}

// skillBundleFor finds the bundle that answers for name in proj's view: local,
// then Project, then global.
func (s *Snapshot) skillBundleFor(proj, name string) (skillBundle, bool) {
	if s.local != nil {
		if b, ok := s.local.skills[name]; ok {
			return b, true
		}
	}
	if p, ok := s.lookupProject(proj); ok {
		if b, ok := p.skills[name]; ok {
			return b, true
		}
	}
	b, ok := s.skills[name]
	return b, ok
}

// SkillIn resolves a skill bundle for a Project ("host/org/repo", matched
// case-insensitively), local then Project then global. An empty or
// unregistered Project resolves globally.
func (s *Snapshot) SkillIn(proj, name string) (Skill, bool) {
	b, ok := s.skillBundleFor(proj, name)
	if !ok {
		return Skill{}, false
	}
	files := make([]string, 0, len(b.files))
	for f := range b.files {
		files = append(files, f)
	}
	slices.Sort(files)
	return Skill{Name: name, Scope: b.scope, Files: files}, true
}

// SkillFile returns a copy of one file of the bundle that answers for name in
// proj's view, as written. A path that is not a file of that bundle, whether
// missing, absolute, leaving the bundle with "..", or a symlink (never
// loaded), is an error.
func (s *Snapshot) SkillFile(proj, name, file string) ([]byte, error) {
	b, ok := s.skillBundleFor(proj, name)
	if !ok || strings.ContainsRune(file, 0) {
		return nil, errNoSkillFile
	}
	raw, ok := b.files[file]
	if !ok {
		return nil, errNoSkillFile
	}
	return append([]byte(nil), raw...), nil
}
