package engine

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// KindSkill is the Kind of a skill: the directory skills/<name>/.
const KindSkill = "skill"

// skillsDir is where a Source keeps its global skills, relative to its root.
const skillsDir = "global/skills"

// skillFileName is the one file every skill directory must hold.
const skillFileName = "SKILL.md"

// The caps on one skill bundle, so a Source cannot make Load read or hold
// content without bound. Each skill is held whole in the Snapshot.
const (
	maxSkillFiles   = 1000     // files in one bundle, SKILL.md included
	maxSkillBytes   = 32 << 20 // bytes of file content in one bundle
	maxSkillPathLen = 1024     // bytes in one path inside a bundle
)

// SkillFile is one file of a skill bundle.
type SkillFile struct {
	// Path is slash separated and relative to the skill directory.
	Path string
	// Content is the file's bytes as written.
	Content []byte
	// Executable is the file's executable bit (any of the mode's x bits for a
	// directory, the git file mode 100755 for git), for pull to restore.
	Executable bool
}

// Skill is one Item of Kind skill: a directory with SKILL.md and its
// supporting files. Only name and description are read from SKILL.md; every
// other key is left alone. A skill has no lifecycle: it is not a reference
// target and takes no part in suppression or enforcement.
type Skill struct {
	// Name is the directory name, equal to the frontmatter name. Names compare
	// case-insensitively (ADR 0015).
	Name string
	// Description is the frontmatter description, required.
	Description string
	// Scope is ScopeGlobal, ScopeLocal, or the Project's "host/org/repo".
	Scope string
	// Files is every file of the bundle, SKILL.md included, sorted by the
	// byte order of Path.
	Files []SkillFile
	// Digest is "sha256:" and 64 lowercase hex digits (ADR 0025).
	Digest string
	// RequiresSync is true when any file is not a .md file, or any file is
	// executable: such a bundle cannot be served as Markdown alone.
	RequiresSync bool
}

// Kind is KindSkill.
func (Skill) Kind() string { return KindSkill }

// skillSet holds one Scope's skills by lower-cased name.
type skillSet map[string]*Skill

// loadScopeSkills reads every skill directory directly under dir, behind the
// check that dir is not a symlink (fs.ReadDir follows one, and os.Root only
// stops links out of the root). A missing dir yields none. A skill directory
// that is a symlink fails the load; a loose file in dir is not a skill and is
// skipped.
func loadScopeSkills(fsys fs.FS, dir, scope, what string) (skillSet, error) {
	if exists, err := requireDir(fsys, dir, what); err != nil || !exists {
		return nil, err
	}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, readErr(err))
	}
	skills := skillSet{}
	seen := map[string]string{} // lower-cased name -> directory
	for _, e := range entries {
		p := dir + "/" + e.Name()
		if e.Type()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s: is a symlink; a skill must be a directory", esc(p))
		}
		if !e.IsDir() {
			continue
		}
		key := strings.ToLower(e.Name())
		if other, dup := seen[key]; dup {
			return nil, fmt.Errorf("%s and %s: skill names differ only in case", esc(other), esc(p))
		}
		seen[key] = p
		sk, err := loadSkill(fsys, p, e.Name(), scope)
		if err != nil {
			return nil, err
		}
		skills[key] = sk
	}
	return skills, nil
}

// loadSkill reads the skill directory dir, named name, into one bundle. It
// never follows a symlink and never opens a FIFO or device: anything but a
// regular file or a directory is skipped, as for articles and agents. Every
// error names dir or a path under it, relative to the Source root, with the
// path escaped to ASCII.
func loadSkill(fsys fs.FS, dir, name, scope string) (*Skill, error) {
	fail := func(format string, args ...any) (*Skill, error) {
		return nil, fmt.Errorf("%s: %s", esc(dir), fmt.Sprintf(format, args...))
	}
	if !validBundleName(name) {
		return fail("the skill name is not allowed")
	}
	var (
		files []SkillFile
		total int
		seen  = map[string]bool{} // lower-cased path under dir
	)
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("%s: %w", esc(p), readErr(err))
		}
		if p == dir {
			return nil
		}
		rel := strings.TrimPrefix(p, dir+"/")
		if len(rel) > maxSkillPathLen {
			return fmt.Errorf("%s: a path in the skill is longer than %d bytes", esc(dir), maxSkillPathLen)
		}
		if !validBundleName(d.Name()) {
			return fmt.Errorf("%s: the file name is not allowed", esc(p))
		}
		// Paths that differ only in case would collide on a case-insensitive
		// file system, so the Source cannot be pulled there.
		key := strings.ToLower(rel)
		if seen[key] {
			return fmt.Errorf("%s: skill paths differ only in case", esc(p))
		}
		seen[key] = true
		if !d.Type().IsRegular() {
			return nil // a directory is walked; a symlink, FIFO or device is skipped
		}
		if len(files) >= maxSkillFiles {
			return fmt.Errorf("%s: the skill has more than %d files", esc(dir), maxSkillFiles)
		}
		f, err := readSkillFile(fsys, p, rel, maxSkillBytes-total)
		if err != nil {
			if errors.Is(err, errSkillTooLarge) {
				return fmt.Errorf("%s: the skill is larger than %d bytes", esc(dir), maxSkillBytes)
			}
			return fmt.Errorf("%s: %w", esc(p), err)
		}
		total += len(f.Content)
		files = append(files, f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b SkillFile) int { return strings.Compare(a.Path, b.Path) })
	i, found := slices.BinarySearchFunc(files, skillFileName, func(f SkillFile, p string) int { return strings.Compare(f.Path, p) })
	if !found {
		return fail("has no %s", skillFileName)
	}
	desc, err := parseSkillFrontmatter(name, files[i].Content)
	if err != nil {
		return nil, fmt.Errorf("%s/%s: %w", esc(dir), skillFileName, err)
	}
	sk := &Skill{Name: name, Description: desc, Scope: scope, Files: files, Digest: digestFiles(files)}
	for _, f := range files {
		if f.Executable || path.Ext(f.Path) != ".md" {
			sk.RequiresSync = true
		}
	}
	return sk, nil
}

var errSkillTooLarge = errors.New("skill too large")

// readSkillFile reads one regular file of a bundle, at most limit bytes. The
// open handle's own mode gives the executable bit, so it describes what was
// read.
func readSkillFile(fsys fs.FS, p, rel string, limit int) (SkillFile, error) {
	f, err := fsys.Open(p)
	if err != nil {
		return SkillFile{}, readErr(err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return SkillFile{}, readErr(err)
	}
	content, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return SkillFile{}, readErr(err)
	}
	if len(content) > limit {
		return SkillFile{}, errSkillTooLarge
	}
	return SkillFile{Path: rel, Content: content, Executable: fi.Mode().Perm()&0o111 != 0}, nil
}

// parseSkillFrontmatter checks SKILL.md: the frontmatter name must equal the
// directory name exactly, and the description is required. It returns the
// description. Errors never echo the content.
func parseSkillFrontmatter(name string, raw []byte) (string, error) {
	front, _, err := splitFrontmatter(raw)
	if err != nil {
		return "", err
	}
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(front, &meta); err != nil {
		return "", yamlErr("frontmatter", err)
	}
	switch fm := strings.TrimSpace(meta.Name); {
	case fm == "":
		return "", errors.New("frontmatter has no name")
	case fm != name:
		return "", errors.New("frontmatter name does not match the directory name")
	}
	desc := strings.TrimSpace(meta.Description)
	if desc == "" {
		return "", errors.New("frontmatter has no description")
	}
	return desc, nil
}

// validBundleName holds one path segment of a skill, and the skill's name, to
// what every file system can keep and no reader is misled by: not empty, not
// . or .., no separator, no backslash and no hidden rune (HiddenRune, which
// includes NUL and every control character).
func validBundleName(n string) bool {
	if n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
		return false
	}
	return !strings.ContainsFunc(n, HiddenRune)
}

// esc escapes p to ASCII, so a hostile name cannot put a control character or
// a bidirectional override into an error.
func esc(p string) string {
	q := strconv.QuoteToASCII(p)
	return q[1 : len(q)-1]
}

// digestFiles is the Item digest of a bundle (ADR 0025): SHA-256 over the
// files in the byte order of their paths, each as
//
//	path 0x00 x 0x00 length 0x00 bytes
//
// where x is the single byte 0x01 for an executable file and 0x00 otherwise,
// and length is the decimal count of bytes. A path holds no NUL (validBundleName),
// so the first NUL ends it, and the length says where the bytes end; no choice
// of paths and contents can give two bundles one input. The result is
// "sha256:" and the lowercase hex of the sum. files must already be sorted.
func digestFiles(files []SkillFile) string {
	h := sha256.New()
	for _, f := range files {
		x := byte(0)
		if f.Executable {
			x = 1
		}
		_, _ = io.WriteString(h, f.Path)
		_, _ = h.Write([]byte{0, x, 0})
		_, _ = io.WriteString(h, strconv.Itoa(len(f.Content)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(f.Content)
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

// skill returns the skill that answers in proj's view: local, then the
// Project's, then global (CONTEXT: Resolution). The pointer is into the
// Snapshot, so callers must not change or return it.
func (s *Snapshot) skill(proj, name string) *Skill {
	key := strings.ToLower(name)
	if s.local != nil {
		if sk := s.local.skills[key]; sk != nil {
			return sk
		}
	}
	if p, ok := s.lookupProject(proj); ok {
		if sk := p.skills[key]; sk != nil {
			return sk
		}
	}
	return s.skills[key]
}

func cloneSkill(sk Skill) Skill {
	sk.Files = slices.Clone(sk.Files)
	for i := range sk.Files {
		sk.Files[i].Content = slices.Clone(sk.Files[i].Content)
	}
	return sk
}

// SkillIn resolves a skill for a Project ("host/org/repo", matched
// case-insensitively), local then Project then global; the winner's bundle is
// served whole. An empty or unregistered Project resolves globally. A skill has
// no Status, so opts is accepted for symmetry with AgentIn and changes nothing.
// The result is a copy.
func (s *Snapshot) SkillIn(proj, name string, _ ...ReadOptions) (Skill, bool) {
	if sk := s.skill(proj, name); sk != nil {
		return cloneSkill(*sk), true
	}
	return Skill{}, false
}

// SkillFile returns one file of the skill SkillIn would resolve. p is matched
// exactly against the bundle's paths, so no path outside the bundle can be
// named: there is no path joining or cleaning. The result is a copy.
func (s *Snapshot) SkillFile(proj, name, p string, _ ...ReadOptions) (SkillFile, bool) {
	sk := s.skill(proj, name)
	if sk == nil {
		return SkillFile{}, false
	}
	i, found := slices.BinarySearchFunc(sk.Files, p, func(f SkillFile, p string) int { return strings.Compare(f.Path, p) })
	if !found {
		return SkillFile{}, false
	}
	f := sk.Files[i]
	f.Content = slices.Clone(f.Content)
	return f, true
}
