package engine_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #406 (slice 3 of #15): a skill is an Item of Kind skill, a directory
// skills/<name>/ holding SKILL.md and its supporting files, in the three
// Scopes: global/skills/, projects/<host>/<org>/<repo>/skills/ and
// .goldie/skills/. It resolves local > project > global as agents do.
//
// The API the tests assume, chosen from the agents API (#405):
//
//	type SkillFile struct {
//		Path       string // slash-separated, relative to the skill directory
//		Content    []byte // the bytes as written
//		Executable bool   // the file's executable bit, for pull
//	}
//	type Skill struct {
//		Name         string      // the directory name, equal to the frontmatter name
//		Description  string      // frontmatter description
//		Scope        string      // ScopeGlobal, ScopeLocal or the Project
//		Files        []SkillFile // every file, SKILL.md included, sorted by byte order of Path
//		Digest       string      // "sha256:" + lowercase hex, see the Item-digest ADR
//		RequiresSync bool        // true when a file is not .md or any file is executable
//	}
//	func (k Skill) Kind() string // "skill"
//	func (s *Snapshot) SkillIn(proj, name string, opts ...ReadOptions) (Skill, bool)
//	func (s *Snapshot) SkillFile(proj, name, path string, opts ...ReadOptions) (SkillFile, bool)
//
// Choices the issue leaves open, fixed here:
//   - A symlink inside a skill directory is never followed: Load refuses it, or
//     loads without it, and nothing it points at is served (as for agents).
//     The same holds for a FIFO.
//   - The caps are not numbers in the issue. The tests assume the file count cap
//     is below 5000 and the total bundle bytes cap is below 80 MiB.
//   - The digest's exact bytes are the ADR's; the tests pin its format and what
//     it depends on, not the hash of a known bundle.

func globalSkillFile(name, file string) string { return "global/skills/" + name + "/" + file }

func projectSkillFile(name, file string) string {
	return "projects/" + govProject + "/skills/" + name + "/" + file
}

func localSkillFile(name, file string) string { return ".goldie/skills/" + name + "/" + file }

// skillDoc is a SKILL.md with the given extra frontmatter lines.
func skillDoc(name, description string, front ...string) string {
	extra := ""
	if len(front) > 0 {
		extra = strings.Join(front, "\n") + "\n"
	}
	return "---\nname: " + name + "\ndescription: " + description + "\n" + extra + "---\nHow to " + name + ".\n"
}

// skillAnswer says which copy of name answers in proj, by Scope and
// Description, or "none".
func skillAnswer(snap *engine.Snapshot, proj, name string) string {
	s, ok := snap.SkillIn(proj, name)
	if !ok {
		return "none"
	}
	return s.Scope + "/" + s.Description
}

func skillPaths(s engine.Skill) []string {
	out := []string{}
	for _, f := range s.Files {
		out = append(out, f.Path)
	}
	return out
}

var digestForm = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// bundle is the files of one global skill "x", SKILL.md included, plus extra
// (paths relative to the skill directory).
func bundle(extra map[string]string) map[string]string {
	files := map[string]string{globalSkillFile("x", "SKILL.md"): skillDoc("x", "d")}
	for p, c := range extra {
		files[globalSkillFile("x", p)] = c
	}
	return files
}

func chmodX(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.Chmod(filepath.Join(root, filepath.FromSlash(n)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// digestOf loads files from a directory, with the named paths executable, and
// returns the digest of the global skill x.
func digestOf(t *testing.T, files map[string]string, exec ...string) string {
	t.Helper()
	root := writeFiles(t, files)
	chmodX(t, root, exec...)
	snap, err := engine.NewDirSource(root).Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s, ok := snap.SkillIn("", "x")
	if !ok {
		t.Fatal("SkillIn(x) found nothing")
	}
	return s.Digest
}

// commitWithExec commits files on main with the named paths executable.
func commitWithExec(t *testing.T, r *gitRepo, files map[string]string, exec ...string) {
	t.Helper()
	r.commit("main", files)
	wt, err := r.repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range exec {
		if err := os.Chmod(filepath.Join(r.dir, filepath.FromSlash(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	sig := &object.Signature{Name: "test", Email: "test@example.invalid", When: time.Unix(1700001000, 0)}
	if _, err := wt.Commit("exec bits", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatal(err)
	}
}

// --- Criterion 1: skills/<name>/SKILL.md plus its files, in all three Scopes ---

func TestSkillLoadsWithItsFilesFromTheGlobalScope(t *testing.T) {
	snap := load(t, map[string]string{
		globalSkillFile("deploy", "SKILL.md"):        skillDoc("deploy", "Ship it"),
		globalSkillFile("deploy", "scripts/run.txt"): "run\n",
		globalSkillFile("deploy", "refs/deep/a.md"):  "# a\n",
		globalSkillFile("other", "SKILL.md"):         skillDoc("other", "Other"),
		globalFile("deploy"):                         article("An article called deploy"),
	})
	s, ok := snap.SkillIn("", "deploy")
	if !ok {
		t.Fatal("SkillIn(deploy) found nothing")
	}
	if s.Name != "deploy" || s.Description != "Ship it" || s.Scope != engine.ScopeGlobal || s.Kind() != "skill" {
		t.Errorf("skill = %+v (kind %q)", s, s.Kind())
	}
	want := []string{"SKILL.md", "refs/deep/a.md", "scripts/run.txt"}
	if got := skillPaths(s); !slices.Equal(got, want) {
		t.Errorf("Files = %v, want %v (sorted by byte order of the path)", got, want)
	}
	f, ok := snap.SkillFile("", "deploy", "scripts/run.txt")
	if !ok || string(f.Content) != "run\n" || f.Path != "scripts/run.txt" || f.Executable {
		t.Errorf("SkillFile(scripts/run.txt) = %+v, %v", f, ok)
	}
	if f, ok := snap.SkillFile("", "deploy", "SKILL.md"); !ok || string(f.Content) != skillDoc("deploy", "Ship it") {
		t.Errorf("SkillFile(SKILL.md) = %+v, %v", f, ok)
	}
	if _, ok := snap.SkillFile("", "deploy", "nope.md"); ok {
		t.Error("SkillFile(nope.md) found a file")
	}
	if _, ok := snap.SkillFile("", "missing", "SKILL.md"); ok {
		t.Error("SkillFile of a missing skill found a file")
	}
	if _, ok := snap.SkillIn("", "missing"); ok {
		t.Error("SkillIn(missing) found a skill")
	}
	// A skill is not an article, and the Kind is part of the identity.
	if a, ok := snap.Article("deploy"); !ok || a.Name != "An article called deploy" {
		t.Errorf("Article(deploy) = %+v, %v", a, ok)
	}
	if got := len(snap.Articles()); got != 1 {
		t.Errorf("Articles() = %d, want only the article", got)
	}
}

func TestSkillLookupIgnoresCase(t *testing.T) {
	snap := load(t, map[string]string{globalSkillFile("deploy", "SKILL.md"): skillDoc("deploy", "d")})
	for _, name := range []string{"Deploy", "DEPLOY"} {
		if s, ok := snap.SkillIn("", name); !ok || s.Name != "deploy" {
			t.Errorf("SkillIn(%q) = %+v, %v", name, s, ok)
		}
		if _, ok := snap.SkillFile("", name, "SKILL.md"); !ok {
			t.Errorf("SkillFile(%q, SKILL.md) found nothing", name)
		}
	}
	// The path inside the bundle is exact.
	if _, ok := snap.SkillFile("", "deploy", "skill.md"); ok {
		t.Error("SkillFile(skill.md) matched SKILL.md")
	}
}

func TestSkillsLoadFromAProjectAndOnlyShowInThatView(t *testing.T) {
	snap := load(t, map[string]string{
		globalSkillFile("g", "SKILL.md"):               skillDoc("g", "global"),
		projectSkillFile("shop", "SKILL.md"):           skillDoc("shop", "shop only"),
		projectSkillFile("shop", "notes.md"):           "notes\n",
		"projects/github.com/acme/other/articles/x.md": article("X"),
	})
	for _, proj := range []string{govProject, strings.ToUpper(govProject)} {
		if got := skillAnswer(snap, proj, "shop"); got != govProject+"/shop only" {
			t.Errorf("SkillIn(%q, shop) = %s", proj, got)
		}
		if f, ok := snap.SkillFile(proj, "shop", "notes.md"); !ok || string(f.Content) != "notes\n" {
			t.Errorf("SkillFile(%q, shop, notes.md) = %+v, %v", proj, f, ok)
		}
		if got := skillAnswer(snap, proj, "g"); got != "global/global" {
			t.Errorf("SkillIn(%q, g) = %s, want the global skill", proj, got)
		}
	}
	for _, proj := range []string{"", "github.com/acme/other", "github.com/acme/unregistered"} {
		if got := skillAnswer(snap, proj, "shop"); got != "none" {
			t.Errorf("SkillIn(%q, shop) = %s; a Project skill is not in another view", proj, got)
		}
		if _, ok := snap.SkillFile(proj, "shop", "notes.md"); ok {
			t.Errorf("SkillFile(%q, shop, notes.md) served a Project file in another view", proj)
		}
	}
}

func TestLocalSkillsAnswerInEveryView(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalSkillFile("g", "SKILL.md"):    skillDoc("g", "global"),
		localSkillFile("mine", "SKILL.md"):  skillDoc("mine", "local"),
		localSkillFile("mine", "extra.txt"): "extra",
	})
	for _, proj := range localViews {
		if got := skillAnswer(snap, proj, "mine"); got != engine.ScopeLocal+"/local" {
			t.Errorf("SkillIn(%q, mine) = %s", proj, got)
		}
		if f, ok := snap.SkillFile(proj, "mine", "extra.txt"); !ok || string(f.Content) != "extra" {
			t.Errorf("SkillFile(%q, mine, extra.txt) = %+v, %v", proj, f, ok)
		}
	}
}

func TestSkillsResolveLocalThenProjectThenGlobal(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalSkillFile("a", "SKILL.md"):    skillDoc("a", "global a"),
		globalSkillFile("b", "SKILL.md"):    skillDoc("b", "global b"),
		globalSkillFile("c", "SKILL.md"):    skillDoc("c", "global c"),
		globalSkillFile("c", "only-global"): "y",
		projectSkillFile("b", "SKILL.md"):   skillDoc("b", "project b"),
		projectSkillFile("c", "SKILL.md"):   skillDoc("c", "project c"),
		localSkillFile("c", "SKILL.md"):     skillDoc("c", "local c"),
		localSkillFile("c", "only-local"):   "x",
	})
	want := map[string]map[string]string{
		"":         {"a": "global/global a", "b": "global/global b", "c": "local/local c"},
		govProject: {"a": "global/global a", "b": govProject + "/project b", "c": "local/local c"},
	}
	for proj, names := range want {
		for name, w := range names {
			if got := skillAnswer(snap, proj, name); got != w {
				t.Errorf("SkillIn(%q, %s) = %s, want %s", proj, name, got, w)
			}
		}
		// The winner's bundle is served whole, never merged with a shadowed one.
		s, _ := snap.SkillIn(proj, "c")
		if got := skillPaths(s); !slices.Equal(got, []string{"SKILL.md", "only-local"}) {
			t.Errorf("SkillIn(%q, c) Files = %v, want only the local bundle", proj, got)
		}
		if _, ok := snap.SkillFile(proj, "c", "only-global"); ok {
			t.Errorf("SkillFile(%q, c, only-global) served a shadowed Scope's file", proj)
		}
	}
}

func TestSkillsAreCopies(t *testing.T) {
	snap := load(t, bundle(map[string]string{"a.md": "alpha"}))
	s, _ := snap.SkillIn("", "x")
	if len(s.Files) != 2 {
		t.Fatalf("Files = %v, want SKILL.md and a.md", skillPaths(s))
	}
	s.Description = "changed"
	s.Files[0].Content[0] = 'X'
	s.Files[1].Path = "changed"
	f, _ := snap.SkillFile("", "x", "a.md")
	f.Content[0] = 'Z'
	again, _ := snap.SkillIn("", "x")
	if again.Description != "d" || !bytes.HasPrefix(again.Files[0].Content, []byte("---\n")) || again.Files[1].Path != "a.md" || string(again.Files[1].Content) != "alpha" {
		t.Errorf("the Snapshot changed through a returned Skill: %+v", again)
	}
}

// --- Criterion 1: the name is one flat segment and equals the frontmatter name ---

func TestSkillNameMustEqualTheDirectoryName(t *testing.T) {
	for name, file := range map[string]string{
		"global":  globalSkillFile("deploy", "SKILL.md"),
		"project": projectSkillFile("deploy", "SKILL.md"),
		"local":   localSkillFile("deploy", "SKILL.md"),
	} {
		t.Run(name, func(t *testing.T) {
			loadLocalErr(t, map[string]string{file: skillDoc("something-else", "d")}, filepath.ToSlash(filepath.Dir(file)))
		})
	}
}

func TestSkillNameIsOneFlatSegment(t *testing.T) {
	for name, front := range map[string]string{
		"slash":          "team/deploy",
		"parent":         "../deploy",
		"dot dot":        "..",
		"dot":            ".",
		"absolute":       "/deploy",
		"trailing slash": "deploy/",
		"backslash":      `team\\deploy`,
	} {
		t.Run(name, func(t *testing.T) {
			loadLocalErr(t, map[string]string{
				globalSkillFile("deploy", "SKILL.md"): "---\nname: \"" + front + "\"\ndescription: d\n---\n",
			}, "global/skills/deploy")
		})
	}
	t.Run("nested directory", func(t *testing.T) {
		// skills/team/deploy/SKILL.md is not a skill named team/deploy: the
		// directory skills/team has no SKILL.md of its own.
		loadLocalErr(t, map[string]string{
			globalSkillFile("team", "deploy/SKILL.md"): skillDoc("team/deploy", "d"),
		}, "global/skills/team")
	})
	t.Run("no name", func(t *testing.T) {
		loadLocalErr(t, map[string]string{globalSkillFile("x", "SKILL.md"): "---\ndescription: d\n---\n"}, "global/skills/x")
	})
}

func TestSkillFrontmatterErrorsNameTheSkill(t *testing.T) {
	for name, content := range map[string]string{
		"no frontmatter": "just text\n",
		"unclosed":       "---\nname: x\n",
		"malformed yaml": "---\nname: [unclosed\n---\n",
		"name is a list": "---\nname: [x]\n---\n",
		"empty file":     "",
	} {
		t.Run(name, func(t *testing.T) {
			for _, file := range []string{globalSkillFile("x", "SKILL.md"), projectSkillFile("x", "SKILL.md"), localSkillFile("x", "SKILL.md")} {
				msg := loadLocalErr(t, map[string]string{file: content}, filepath.ToSlash(filepath.Dir(file)))
				if strings.Contains(msg, "just text") {
					t.Errorf("error echoes the content: %q", msg)
				}
			}
		})
	}
}

func TestSkillNamesDifferingOnlyInCaseFailTheLoad(t *testing.T) {
	for name, files := range map[string][2]string{
		"global":  {globalSkillFile("Deploy", "SKILL.md"), globalSkillFile("deploy", "SKILL.md")},
		"project": {projectSkillFile("Deploy", "SKILL.md"), projectSkillFile("deploy", "SKILL.md")},
		"local":   {localSkillFile("Deploy", "SKILL.md"), localSkillFile("deploy", "SKILL.md")},
	} {
		t.Run(name, func(t *testing.T) {
			loadLocalErr(t, map[string]string{
				files[0]: skillDoc("Deploy", "One"),
				files[1]: skillDoc("deploy", "Two"),
			}, "Deploy", "deploy")
		})
	}
}

// The same name in two Scopes is shadowing, not a collision.
func TestSkillSameNameInThreeScopesLoads(t *testing.T) {
	loadLocal(t, map[string]string{
		globalSkillFile("x", "SKILL.md"):  skillDoc("x", "g"),
		projectSkillFile("x", "SKILL.md"): skillDoc("x", "p"),
		localSkillFile("x", "SKILL.md"):   skillDoc("x", "l"),
	})
}

// --- Criterion 4: a missing SKILL.md, an empty skill directory ---

func TestSkillDirectoryWithoutSkillMdFailsTheLoad(t *testing.T) {
	for name, dir := range map[string]string{
		"global":  "global/skills/x",
		"project": "projects/" + govProject + "/skills/x",
		"local":   ".goldie/skills/x",
	} {
		t.Run(name, func(t *testing.T) {
			loadLocalErr(t, map[string]string{dir + "/notes.md": "notes\n"}, dir)
		})
		t.Run(name+" wrong case", func(t *testing.T) {
			loadLocalErr(t, map[string]string{dir + "/skill.md": skillDoc("x", "d")}, dir)
		})
	}
}

func TestEmptySkillDirectoryFailsTheLoad(t *testing.T) {
	root := writeFiles(t, map[string]string{globalFile("a"): article("A")})
	mustMkdir(t, filepath.Join(root, "global/skills/empty"))
	_, err := engine.NewDirSource(root).Load(t.Context())
	if err == nil {
		t.Fatal("an empty skill directory loaded")
	}
	if !strings.Contains(err.Error(), "global/skills/empty") || strings.Contains(err.Error(), root) {
		t.Errorf("error = %q, want it to name global/skills/empty without the host path", err)
	}
}

// A loose file in skills/ is not a skill: there is no directory to hold it.
func TestLooseFileInSkillsDirIsNotASkill(t *testing.T) {
	snap, err := engine.NewDirSource(writeFiles(t, map[string]string{
		"global/skills/README.md":        "# skills\n",
		globalSkillFile("x", "SKILL.md"): skillDoc("x", "d"),
	})).Load(t.Context())
	if err != nil {
		return // refusing it is acceptable
	}
	if _, ok := snap.SkillIn("", "README"); ok {
		t.Error("a loose file in skills/ was served as a skill")
	}
}

// --- Criteria 1 and 4: every file stays inside the skill directory ---

func TestSkillFileRefusesPathsOutsideTheBundle(t *testing.T) {
	snap := load(t, map[string]string{
		globalSkillFile("x", "SKILL.md"):    skillDoc("x", "d"),
		globalSkillFile("x", "sub/ok.md"):   "ok",
		globalSkillFile("y", "SKILL.md"):    skillDoc("y", "secret"),
		globalSkillFile("y", "private.txt"): "secret",
		globalFile("secret"):                article("Secret"),
	})
	for _, p := range []string{
		"..", "../y/SKILL.md", "../y/private.txt", "sub/../../y/private.txt", "sub/..", "../../articles/secret.md",
		"/etc/passwd", "/global/skills/y/private.txt", "//etc/passwd", "/", "",
		"./SKILL.md", "sub/./ok.md", "sub//ok.md", "sub/ok.md/", "sub/", "sub", ".",
		`..\y\private.txt`, `sub\ok.md`, "sub/ok.md\x00", "SKILL.md\x00.md", "%2e%2e/y/private.txt", "..%2fy/private.txt",
		"C:/Windows/win.ini", "\u202e/SKILL.md",
	} {
		if f, ok := snap.SkillFile("", "x", p); ok {
			t.Errorf("SkillFile(x, %q) served %+v", p, f)
		}
	}
	if _, ok := snap.SkillFile("", "x", "sub/ok.md"); !ok {
		t.Error("SkillFile(x, sub/ok.md) found nothing")
	}
}

func TestSkillInRefusesNamesThatAreNotOneSegment(t *testing.T) {
	snap := load(t, map[string]string{
		globalSkillFile("x", "SKILL.md"):   skillDoc("x", "d"),
		globalSkillFile("x", "a/SKILL.md"): "nested",
	})
	for _, n := range []string{"", ".", "..", "x/..", "x/", "/x", "x/a", "../x", "global/skills/x", `x\a`, "x\x00", "../../x"} {
		if s, ok := snap.SkillIn("", n); ok {
			t.Errorf("SkillIn(%q) = %+v", n, s)
		}
		if f, ok := snap.SkillFile("", n, "SKILL.md"); ok {
			t.Errorf("SkillFile(%q, SKILL.md) = %+v", n, f)
		}
	}
	if f, ok := snap.SkillFile("", "x", "a/SKILL.md"); !ok || string(f.Content) != "nested" {
		t.Errorf("a nested SKILL.md is a plain file of the bundle: %+v, %v", f, ok)
	}
}

func outsideSkill(t *testing.T) string {
	t.Helper()
	return writeFiles(t, map[string]string{
		"secret.txt":       "TOP-SECRET",
		"dir/inner.txt":    "TOP-SECRET",
		"skill/SKILL.md":   skillDoc("stolen", "Stolen"),
		"skill/leaked.txt": "TOP-SECRET",
	})
}

// A symlink in a skill is never followed: Load refuses it, or loads the skill
// without it. No byte of what it points at is served, and an error carries no
// host path.
func TestSymlinksInASkillAreNotFollowed(t *testing.T) {
	outside := outsideSkill(t)
	for name, setup := range map[string]func(t *testing.T, root string) error{
		"file to outside the root": func(t *testing.T, root string) error {
			return symlink(t, filepath.Join(outside, "secret.txt"), filepath.Join(root, "global/skills/x/link.txt"))
		},
		"file to a relative outside": func(t *testing.T, root string) error {
			return symlink(t, "../../../../../../../../"+strings.TrimPrefix(filepath.Join(outside, "secret.txt"), "/"), filepath.Join(root, "global/skills/x/link.txt"))
		},
		"file to another skill": func(t *testing.T, root string) error {
			writeTo(t, root, globalSkillFile("y", "SKILL.md"), skillDoc("y", "TOP-SECRET"))
			return symlink(t, "../y/SKILL.md", filepath.Join(root, "global/skills/x/link.txt"))
		},
		"file inside the skill": func(t *testing.T, root string) error {
			writeTo(t, root, globalSkillFile("x", "real.md"), "TOP-SECRET")
			return symlink(t, "real.md", filepath.Join(root, "global/skills/x/link.txt"))
		},
		"directory to outside the root": func(t *testing.T, root string) error {
			return symlink(t, filepath.Join(outside, "dir"), filepath.Join(root, "global/skills/x/linked"))
		},
		"dangling": func(t *testing.T, root string) error {
			return symlink(t, "nowhere", filepath.Join(root, "global/skills/x/link.txt"))
		},
		"SKILL.md itself": func(t *testing.T, root string) error {
			if err := os.Remove(filepath.Join(root, "global/skills/x/SKILL.md")); err != nil {
				t.Fatal(err)
			}
			return symlink(t, filepath.Join(outside, "skill/SKILL.md"), filepath.Join(root, "global/skills/x/SKILL.md"))
		},
		"the skill directory": func(t *testing.T, root string) error {
			if err := os.RemoveAll(filepath.Join(root, "global/skills/x")); err != nil {
				t.Fatal(err)
			}
			return symlink(t, filepath.Join(outside, "skill"), filepath.Join(root, "global/skills/x"))
		},
		"the skills directory": func(t *testing.T, root string) error {
			if err := os.RemoveAll(filepath.Join(root, "global/skills")); err != nil {
				t.Fatal(err)
			}
			return symlink(t, outside, filepath.Join(root, "global/skills"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeFiles(t, bundle(map[string]string{"plain.md": "plain"}))
			if err := setup(t, root); err != nil {
				t.Skip("symlinks unavailable")
			}
			snap, err := engine.NewDirSource(root).Load(t.Context())
			if err != nil {
				if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), outside) {
					t.Errorf("error %q leaks a host path", err)
				}
				return
			}
			for _, n := range []string{"x", "stolen", "skill"} {
				s, ok := snap.SkillIn("", n)
				if !ok {
					continue
				}
				for _, f := range s.Files {
					if bytes.Contains(f.Content, []byte("TOP-SECRET")) || f.Path == "link.txt" || strings.HasPrefix(f.Path, "linked/") {
						t.Errorf("skill %s serves %q through a symlink: %q", n, f.Path, f.Content)
					}
				}
			}
			for _, p := range []string{"link.txt", "linked/inner.txt", "leaked.txt"} {
				if f, ok := snap.SkillFile("", "x", p); ok {
					t.Errorf("SkillFile(x, %s) = %q", p, f.Content)
				}
			}
		})
	}
}

func TestSymlinkInALocalSkillIsNotFollowed(t *testing.T) {
	outside := outsideSkill(t)
	for name, setup := range map[string]func(t *testing.T, repo string) error{
		"file": func(t *testing.T, repo string) error {
			writeTo(t, repo, localSkillFile("mine", "SKILL.md"), skillDoc("mine", "Mine"))
			return symlink(t, filepath.Join(outside, "secret.txt"), filepath.Join(repo, filepath.FromSlash(localSkillFile("mine", "link.txt"))))
		},
		"skill directory": func(t *testing.T, repo string) error {
			return symlink(t, filepath.Join(outside, "skill"), filepath.Join(repo, ".goldie", "skills", "mine"))
		},
		"skills directory": func(t *testing.T, repo string) error {
			return symlink(t, outside, filepath.Join(repo, ".goldie", "skills"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			src, dirs, ok := localFSSource(t, setup)
			if !ok {
				t.Skip("symlinks unavailable")
			}
			snap, err := src.Load(t.Context())
			if err != nil {
				assertNoHostPath(t, err.Error(), dirs)
				if strings.Contains(err.Error(), outside) {
					t.Errorf("error %q leaks the symlink target", err)
				}
				return
			}
			for _, proj := range localViews {
				for _, n := range []string{"mine", "stolen", "skill"} {
					s, ok := snap.SkillIn(proj, n)
					if !ok {
						continue
					}
					for _, f := range s.Files {
						if bytes.Contains(f.Content, []byte("TOP-SECRET")) || f.Path == "link.txt" {
							t.Errorf("SkillIn(%q, %s) serves %q through a symlink", proj, n, f.Path)
						}
					}
				}
			}
		})
	}
}

func TestSymlinkedProjectSkillsDirectoryIsNotFollowed(t *testing.T) {
	outside := outsideSkill(t)
	root := writeFiles(t, map[string]string{globalSkillFile("g", "SKILL.md"): skillDoc("g", "g")})
	mustMkdir(t, filepath.Join(root, "projects/github.com/acme/shop"))
	if err := os.Symlink(outside, filepath.Join(root, "projects/github.com/acme/shop/skills")); err != nil {
		t.Skip("symlinks unavailable")
	}
	snap, err := engine.NewDirSource(root).Load(t.Context())
	if err != nil {
		if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), outside) {
			t.Errorf("error %q leaks a host path", err)
		}
		return
	}
	if s, ok := snap.SkillIn(govProject, "skill"); ok {
		t.Errorf("served %+v through a symlinked skills directory", s)
	}
	if f, ok := snap.SkillFile(govProject, "skill", "leaked.txt"); ok {
		t.Errorf("served %q through a symlinked skills directory", f.Content)
	}
}

func TestSymlinkInAGitSkillIsNotFollowed(t *testing.T) {
	repo := newGitRepo(t)
	repo.commit("main", map[string]string{
		globalSkillFile("x", "SKILL.md"): skillDoc("x", "d"),
		globalSkillFile("x", "link.txt"): "symlink:/etc/passwd",
		globalSkillFile("x", "up.txt"):   "symlink:../../../../../etc/passwd",
		globalSkillFile("y", "SKILL.md"): skillDoc("y", "d"),
		globalSkillFile("y", "other"):    "symlink:../x/SKILL.md",
	})
	snap, err := newGit(t, engine.GitOptions{URL: repo.url}).Load(t.Context())
	if err != nil {
		if strings.Contains(err.Error(), repo.dir) {
			t.Errorf("error %q leaks a host path", err)
		}
		return
	}
	for _, c := range [][2]string{{"x", "link.txt"}, {"x", "up.txt"}, {"y", "other"}} {
		if f, ok := snap.SkillFile("", c[0], c[1]); ok {
			t.Errorf("SkillFile(%s, %s) = %q, want the symlink not served", c[0], c[1], f.Content)
		}
	}
}

// --- Criterion 1: caps on the file count and the total bundle bytes ---

func TestSkillWithinTheCapsLoads(t *testing.T) {
	files := map[string]string{globalSkillFile("x", "SKILL.md"): skillDoc("x", "d")}
	for i := range 50 {
		files[globalSkillFile("x", fmt.Sprintf("refs/f%02d.txt", i))] = strings.Repeat("k", 1024)
	}
	snap := load(t, files)
	if s, _ := snap.SkillIn("", "x"); len(s.Files) != 51 {
		t.Errorf("Files = %d, want 51", len(s.Files))
	}
}

func TestSkillWithTooManyFilesFailsTheLoad(t *testing.T) {
	root := writeFiles(t, bundle(nil))
	dir := filepath.Join(root, "global/skills/x/many")
	mustMkdir(t, dir)
	for i := range 5000 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := engine.NewDirSource(root).Load(t.Context())
	if err == nil {
		s, _ := snap.SkillIn("", "x")
		t.Fatalf("a skill of %d files loaded; the file count is capped", len(s.Files))
	}
	if !strings.Contains(err.Error(), "global/skills/x") || strings.Contains(err.Error(), root) {
		t.Errorf("error = %q, want it to name global/skills/x without the host path", err)
	}
}

func TestSkillWithTooManyBytesFailsTheLoad(t *testing.T) {
	root := writeFiles(t, bundle(nil))
	dir := filepath.Join(root, "global/skills/x/big")
	mustMkdir(t, dir)
	for i := range 80 { // 80 MiB, each file within one article's 1 MiB
		p := filepath.Join(dir, fmt.Sprintf("f%02d.bin", i))
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(p, 1<<20); err != nil {
			t.Skipf("truncate: %v", err)
		}
	}
	snap, err := engine.NewDirSource(root).Load(t.Context())
	if err == nil {
		s, _ := snap.SkillIn("", "x")
		t.Fatalf("a skill of %d files and 80 MiB loaded; the total bundle bytes are capped", len(s.Files))
	}
	if !strings.Contains(err.Error(), "global/skills/x") || strings.Contains(err.Error(), root) {
		t.Errorf("error = %q, want it to name global/skills/x without the host path", err)
	}
}

func TestOneHugeSkillFileFailsTheLoad(t *testing.T) {
	root := writeFiles(t, bundle(nil))
	p := filepath.Join(root, "global/skills/x/huge.bin")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(p, 200<<20); err != nil {
		t.Skipf("truncate: %v", err)
	}
	if snap, err := engine.NewDirSource(root).Load(t.Context()); err == nil {
		s, _ := snap.SkillIn("", "x")
		t.Fatalf("a skill with a 200 MiB file loaded (%d files)", len(s.Files))
	}
}

// --- Criterion 1: two paths differing only in case collide on macOS ---

func TestSkillPathsDifferingOnlyInCaseFailTheLoad(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"files":              {"Notes.md": "a", "notes.md": "b"},
		"files in a subdir":  {"refs/A.txt": "a", "refs/a.txt": "b"},
		"directories":        {"Refs/a.md": "a", "refs/b.md": "b"},
		"nested directories": {"a/Deep/x.md": "a", "a/deep/y.md": "b"},
		"SKILL.md":           {"skill.md": "b"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, dir := range []string{"global/skills/x", "projects/" + govProject + "/skills/x", ".goldie/skills/x"} {
				files := map[string]string{dir + "/SKILL.md": skillDoc("x", "d")}
				for p, c := range extra {
					files[dir+"/"+p] = c
				}
				loadLocalErr(t, files, dir)
			}
		})
	}
	t.Run("the same name in two skills is fine", func(t *testing.T) {
		load(t, map[string]string{
			globalSkillFile("x", "SKILL.md"): skillDoc("x", "d"),
			globalSkillFile("x", "A.md"):     "a",
			globalSkillFile("y", "SKILL.md"): skillDoc("y", "d"),
			globalSkillFile("y", "a.md"):     "b",
		})
	})
}

func TestSkillPathsDifferingInCaseCollideInGit(t *testing.T) {
	repo := newGitRepo(t)
	repo.commit("main", map[string]string{
		globalSkillFile("x", "SKILL.md"): skillDoc("x", "d"),
		globalSkillFile("x", "Notes.md"): "a",
		globalSkillFile("x", "notes.md"): "b",
	})
	if _, err := newGit(t, engine.GitOptions{URL: repo.url}).Load(t.Context()); err == nil {
		t.Fatal("a git skill with paths differing only in case loaded")
	}
}

// --- Criterion 4: binary files ---

func TestSkillServesBinaryFilesByteForByte(t *testing.T) {
	blob := []byte{0x00, 0xff, 0xfe, '\n', '\r', '\n', 0x80, 0x00, '-', '-', '-', 0xc3, 0x28}
	root := writeFiles(t, bundle(nil))
	if err := os.WriteFile(filepath.Join(root, "global/skills/x/tool.bin"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := engine.NewDirSource(root).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f, ok := snap.SkillFile("", "x", "tool.bin")
	if !ok || !bytes.Equal(f.Content, blob) {
		t.Errorf("SkillFile(tool.bin) = %x, %v, want %x", f.Content, ok, blob)
	}
	if s, _ := snap.SkillIn("", "x"); !s.RequiresSync {
		t.Error("a binary file did not set RequiresSync")
	}
}

// SKILL.md and Markdown are served as written, CRLF and raw HTML included.
func TestSkillMarkdownIsServedAsWritten(t *testing.T) {
	doc := "---\r\nname: x\r\ndescription: d\r\n---\r\n\r\nbody\r\n"
	snap := load(t, map[string]string{globalSkillFile("x", "SKILL.md"): doc, globalSkillFile("x", "r.md"): "<script>alert(1)</script>\r\n"})
	if f, _ := snap.SkillFile("", "x", "SKILL.md"); string(f.Content) != doc {
		t.Errorf("SKILL.md = %q", f.Content)
	}
	if f, _ := snap.SkillFile("", "x", "r.md"); string(f.Content) != "<script>alert(1)</script>\r\n" {
		t.Errorf("r.md = %q", f.Content)
	}
}

// --- Criterion 2: the digest ---

func TestSkillDigestFormat(t *testing.T) {
	d := digestOf(t, bundle(map[string]string{"a.md": "a"}))
	if !digestForm.MatchString(d) {
		t.Errorf("Digest = %q, want sha256: and 64 lowercase hex digits", d)
	}
}

func TestSkillDigestIsStable(t *testing.T) {
	files := bundle(map[string]string{"z.md": "z", "a/b.txt": "b", "m.bin": "\x00\x01"})
	first := digestOf(t, files)
	for i := range 5 { // map order and write order vary
		if got := digestOf(t, files); got != first {
			t.Fatalf("load %d: Digest = %s, want %s", i, got, first)
		}
	}
	// One Snapshot answers one digest, whatever the view or case of the name.
	snap := load(t, files)
	a, _ := snap.SkillIn("", "x")
	b, _ := snap.SkillIn(govProject, "X")
	if a.Digest != first || b.Digest != first {
		t.Errorf("Digest by view = %s, %s, want %s", a.Digest, b.Digest, first)
	}
}

// The digest depends on the paths, the bytes and the executable bits, not on
// where the bundle came from.
func TestSkillDigestIsTheSameAcrossSources(t *testing.T) {
	files := bundle(map[string]string{"z.md": "z", "scripts/a.sh": "#!/bin/sh\n", "bin/tool": "\x00\xff", "empty": ""})
	dir := digestOf(t, files, "global/skills/x/scripts/a.sh")

	repo := newGitRepo(t)
	commitWithExec(t, repo, files, "global/skills/x/scripts/a.sh")
	snap, err := newGit(t, engine.GitOptions{URL: repo.url}).Load(t.Context())
	if err != nil {
		t.Fatalf("git Load: %v", err)
	}
	g, ok := snap.SkillIn("", "x")
	if !ok {
		t.Fatal("the git Source served no skill x")
	}
	if g.Digest != dir {
		t.Errorf("git Digest = %s, directory Digest = %s", g.Digest, dir)
	}

	// The local Scope computes it the same way (no executable bits here).
	local := loadLocal(t, map[string]string{
		localSkillFile("x", "SKILL.md"):     skillDoc("x", "d"),
		localSkillFile("x", "z.md"):         "z",
		localSkillFile("x", "scripts/a.sh"): "#!/bin/sh\n",
		localSkillFile("x", "bin/tool"):     "\x00\xff",
		localSkillFile("x", "empty"):        "",
	})
	l, _ := local.SkillIn("", "x")
	if plain := digestOf(t, files); l.Digest != plain {
		t.Errorf("local Digest = %s, directory Digest = %s (same bytes, no exec bits)", l.Digest, plain)
	}
}

func TestSkillDigestChangesWithAnyByteOrPath(t *testing.T) {
	base := bundle(map[string]string{"a.md": "alpha", "dir/b.txt": "beta"})
	changed := map[string]map[string]string{
		"a byte of a file":    bundle(map[string]string{"a.md": "alphb", "dir/b.txt": "beta"}),
		"one more byte":       bundle(map[string]string{"a.md": "alpha ", "dir/b.txt": "beta"}),
		"one byte less":       bundle(map[string]string{"a.md": "alph", "dir/b.txt": "beta"}),
		"a byte of SKILL.md":  {globalSkillFile("x", "SKILL.md"): skillDoc("x", "e"), globalSkillFile("x", "a.md"): "alpha", globalSkillFile("x", "dir/b.txt"): "beta"},
		"a renamed file":      bundle(map[string]string{"c.md": "alpha", "dir/b.txt": "beta"}),
		"a moved file":        bundle(map[string]string{"a.md": "alpha", "other/b.txt": "beta"}),
		"a path's case":       bundle(map[string]string{"a.md": "alpha", "dir/B.txt": "beta"}),
		"an added empty file": bundle(map[string]string{"a.md": "alpha", "dir/b.txt": "beta", "e": ""}),
		"a removed file":      bundle(map[string]string{"a.md": "alpha"}),
		"swapped contents":    bundle(map[string]string{"a.md": "beta", "dir/b.txt": "alpha"}),
	}
	seen := map[string]string{digestOf(t, base): "the base bundle"}
	for name, files := range changed {
		got := digestOf(t, files)
		if prev, dup := seen[got]; dup {
			t.Errorf("%s: Digest equals the one of %s (%s)", name, prev, got)
		}
		seen[got] = name
	}
}

// The digest frames each file as path, length and bytes, so moving a byte from
// one file's path to its content, or between two files, is a different bundle.
func TestSkillDigestFramesPathsAndContents(t *testing.T) {
	cases := map[string]map[string]string{
		"ab=c":      bundle(map[string]string{"ab": "c"}),
		"a=bc":      bundle(map[string]string{"a": "bc"}),
		"a=b, c=":   bundle(map[string]string{"a": "b", "c": ""}),
		"a=, c=b":   bundle(map[string]string{"a": "", "c": "b"}),
		"a=bc, d=":  bundle(map[string]string{"a": "bc", "d": ""}),
		"a=b, cd=":  bundle(map[string]string{"a": "b", "cd": ""}),
		"a=1 NUL c": bundle(map[string]string{"a": "1\x00c"}),
		"a1=c":      bundle(map[string]string{"a1": "c"}),
	}
	seen := map[string]string{}
	for name, files := range cases {
		d := digestOf(t, files)
		if prev, dup := seen[d]; dup {
			t.Errorf("%s and %s have the same Digest", name, prev)
		}
		seen[d] = name
	}
}

func TestSkillDigestChangesWithTheExecutableBit(t *testing.T) {
	files := bundle(map[string]string{"run.sh": "#!/bin/sh\n", "doc.md": "doc"})
	seen := map[string]string{}
	for name, exec := range map[string][]string{
		"plain":  nil,
		"run.sh": {"global/skills/x/run.sh"},
		"doc.md": {"global/skills/x/doc.md"},
		"both":   {"global/skills/x/run.sh", "global/skills/x/doc.md"},
	} {
		d := digestOf(t, files, exec...)
		if prev, dup := seen[d]; dup {
			t.Errorf("%s has the same Digest as %s", name, prev)
		}
		seen[d] = name
	}
}

func TestSkillDigestIsPerSkill(t *testing.T) {
	snap := load(t, map[string]string{
		globalSkillFile("x", "SKILL.md"): skillDoc("x", "d"),
		globalSkillFile("y", "SKILL.md"): skillDoc("y", "d"),
		globalSkillFile("y", "extra"):    "e",
	})
	x, _ := snap.SkillIn("", "x")
	y, _ := snap.SkillIn("", "y")
	if x.Digest == "" || x.Digest == y.Digest {
		t.Errorf("two different skills have the digests %q and %q", x.Digest, y.Digest)
	}
}

// --- Criterion 3: requires_sync and the executable bit ---

func TestRequiresSyncIsFalseForAMarkdownOnlyBundle(t *testing.T) {
	snap := load(t, bundle(map[string]string{"refs/a.md": "a", "b.md": "b", "refs/deep/c.md": "c"}))
	s, _ := snap.SkillIn("", "x")
	if s.RequiresSync {
		t.Error("RequiresSync is true for a bundle of .md files with no executable bit")
	}
	for _, f := range s.Files {
		if f.Executable {
			t.Errorf("%s is Executable", f.Path)
		}
	}
	// SKILL.md alone is the smallest bundle.
	only := load(t, map[string]string{globalSkillFile("x", "SKILL.md"): skillDoc("x", "d")})
	if s, _ := only.SkillIn("", "x"); s.RequiresSync {
		t.Error("RequiresSync is true for a SKILL.md alone")
	}
}

func TestRequiresSyncIsTrueForAnyFileThatIsNotMarkdown(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"a script":            {"run.sh": "#!/bin/sh\n"},
		"a text file":         {"notes.txt": "n"},
		"a json file":         {"schema.json": "{}"},
		"a binary":            {"tool.bin": "\x00\x01"},
		"no extension":        {"Makefile": "all:\n"},
		"nested non-markdown": {"refs/deep/data.yaml": "a: b"},
		"a dotfile":           {".gitignore": "x"},
		"markdown-like name":  {"notes.md.txt": "n"},
		"an empty file":       {"empty": ""},
	} {
		t.Run(name, func(t *testing.T) {
			snap := load(t, bundle(extra))
			if s, _ := snap.SkillIn("", "x"); !s.RequiresSync {
				t.Errorf("RequiresSync is false for a bundle holding %v", skillPaths(s))
			}
		})
	}
}

func TestRequiresSyncIsTrueForAnExecutableFileEvenWhenMarkdown(t *testing.T) {
	root := writeFiles(t, bundle(map[string]string{"doc.md": "doc"}))
	chmodX(t, root, "global/skills/x/doc.md")
	snap, err := engine.NewDirSource(root).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s, _ := snap.SkillIn("", "x")
	if !s.RequiresSync {
		t.Error("RequiresSync is false for an executable .md file")
	}
	for _, f := range s.Files {
		if want := f.Path == "doc.md"; f.Executable != want {
			t.Errorf("%s Executable = %v, want %v", f.Path, f.Executable, want)
		}
	}
	if f, ok := snap.SkillFile("", "x", "doc.md"); !ok || !f.Executable {
		t.Errorf("SkillFile(doc.md) = %+v, %v, want Executable", f, ok)
	}
	// SKILL.md itself can be executable.
	root = writeFiles(t, bundle(nil))
	chmodX(t, root, "global/skills/x/SKILL.md")
	snap, err = engine.NewDirSource(root).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s, _ = snap.SkillIn("", "x")
	if !s.RequiresSync || len(s.Files) != 1 || !s.Files[0].Executable {
		t.Errorf("an executable SKILL.md: RequiresSync = %v, Files = %+v", s.RequiresSync, s.Files)
	}
}

func TestEachFilesExecutableBitIsServed(t *testing.T) {
	root := writeFiles(t, bundle(map[string]string{"a.sh": "a", "b.sh": "b", "sub/c.sh": "c"}))
	chmodX(t, root, "global/skills/x/a.sh", "global/skills/x/sub/c.sh")
	snap, err := engine.NewDirSource(root).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{"a.sh": true, "b.sh": false, "sub/c.sh": true, "SKILL.md": false} {
		f, ok := snap.SkillFile("", "x", p)
		if !ok || f.Executable != want {
			t.Errorf("SkillFile(%s) = %+v, %v, want Executable %v", p, f, ok, want)
		}
	}
	s, _ := snap.SkillIn("", "x")
	got := map[string]bool{}
	for _, f := range s.Files {
		got[f.Path] = f.Executable
	}
	if !got["a.sh"] || got["b.sh"] || !got["sub/c.sh"] || got["SKILL.md"] {
		t.Errorf("Files executable bits = %v", got)
	}
}

func TestExecutableBitComesFromGitFileMode(t *testing.T) {
	repo := newGitRepo(t)
	commitWithExec(t, repo, map[string]string{
		globalSkillFile("x", "SKILL.md"):  skillDoc("x", "d"),
		globalSkillFile("x", "run.sh"):    "#!/bin/sh\n",
		globalSkillFile("x", "plain.txt"): "p",
	}, "global/skills/x/run.sh")
	snap, err := newGit(t, engine.GitOptions{URL: repo.url}).Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for p, want := range map[string]bool{"run.sh": true, "plain.txt": false, "SKILL.md": false} {
		if f, ok := snap.SkillFile("", "x", p); !ok || f.Executable != want {
			t.Errorf("SkillFile(%s) = %+v, %v, want Executable %v", p, f, ok, want)
		}
	}
	if s, _ := snap.SkillIn("", "x"); !s.RequiresSync {
		t.Error("RequiresSync is false for a git bundle with an executable file")
	}
}
