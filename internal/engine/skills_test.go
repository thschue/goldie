package engine_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #406 (slice 3 of #15): a skill bundle is the directory skills/<name>/
// with a SKILL.md and its supporting files, in the three Scopes:
// global/skills/, projects/<host>/<org>/<repo>/skills/ and .goldie/skills/.
// The nearest Scope wins with its whole bundle (local, then Project, then
// global). Files are read through contentFS and the treefs.go budget, so no new
// caps, name rules or file-type checks are tested here beyond what agents and
// articles already hold. The digest, the executable bit and requires_sync are
// out of scope (#16).
//
// The API the tests assume, following agents.go:
//
//	type Skill struct {
//		Name  string   // the directory name
//		Scope string   // ScopeGlobal, ScopeLocal or the Project
//		Files []string // the bundle's files, slash paths relative to the
//		                // bundle, sorted, SKILL.md included
//	}
//	func (s *Snapshot) SkillIn(proj, name string) (Skill, bool)
//	func (s *Snapshot) SkillFile(proj, name, path string) ([]byte, error)
//
// SkillFile returns a copy of the file's bytes as written, and an error for a
// path that is not a file of the bundle that answers: a missing one, one that
// is absolute or leaves the bundle with "..", or a symlink.

func globalSkillFile(name, file string) string { return "global/skills/" + name + "/" + file }

func projectSkillFile(name, file string) string {
	return "projects/" + govProject + "/skills/" + name + "/" + file
}

func localSkillFile(name, file string) string { return ".goldie/skills/" + name + "/" + file }

// skillAnswer says which bundle answers for name in proj, by Scope and
// SKILL.md content, or "none".
func skillAnswer(t *testing.T, snap *engine.Snapshot, proj, name string) string {
	t.Helper()
	s, ok := snap.SkillIn(proj, name)
	if !ok {
		return "none"
	}
	b, err := snap.SkillFile(proj, name, "SKILL.md")
	if err != nil {
		t.Fatalf("SkillFile(%q, %q, SKILL.md): %v", proj, name, err)
	}
	return s.Scope + "/" + string(b)
}

// --- Criterion 1: bundles in three Scopes, nearest Scope wins whole ---

func TestSkillBundleLoadsFromTheGlobalScope(t *testing.T) {
	snap := load(t, map[string]string{
		globalSkillFile("deploy", "SKILL.md"):           "# Deploy\r\n",
		globalSkillFile("deploy", "checklist.txt"):      "one\ntwo\n",
		globalSkillFile("deploy", "scripts/run.sh"):     "#!/bin/sh\necho hi\n",
		globalSkillFile("deploy", "refs/deep/data.bin"): "\x00\x01\xff",
		globalSkillFile("other", "SKILL.md"):            "# Other\n",
		"global/articles/a.md":                          article("A"),
	})
	s, ok := snap.SkillIn("", "deploy")
	if !ok {
		t.Fatal("SkillIn(deploy) found nothing")
	}
	if s.Name != "deploy" || s.Scope != engine.ScopeGlobal {
		t.Errorf("skill = %+v", s)
	}
	want := []string{"SKILL.md", "checklist.txt", "refs/deep/data.bin", "scripts/run.sh"}
	if !slices.Equal(s.Files, want) {
		t.Errorf("Files = %v, want %v", s.Files, want)
	}
	for file, content := range map[string]string{
		"SKILL.md":           "# Deploy\r\n",
		"checklist.txt":      "one\ntwo\n",
		"scripts/run.sh":     "#!/bin/sh\necho hi\n",
		"refs/deep/data.bin": "\x00\x01\xff",
	} {
		got, err := snap.SkillFile("", "deploy", file)
		if err != nil || string(got) != content {
			t.Errorf("SkillFile(deploy, %q) = %q, %v; want %q as written", file, got, err, content)
		}
	}
	if _, ok := snap.SkillIn("", "missing"); ok {
		t.Error("SkillIn(missing) found a skill")
	}
	if _, err := snap.SkillFile("", "missing", "SKILL.md"); err == nil {
		t.Error("SkillFile of a skill that does not exist succeeded")
	}
	if _, err := snap.SkillFile("", "deploy", "nope.txt"); err == nil {
		t.Error("SkillFile of a file that does not exist succeeded")
	}
}

func TestSkillFileReturnsACopy(t *testing.T) {
	snap := load(t, map[string]string{globalSkillFile("deploy", "SKILL.md"): "# Deploy\n"})
	b, err := snap.SkillFile("", "deploy", "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	b[0] = 'X'
	if again, _ := snap.SkillFile("", "deploy", "SKILL.md"); string(again) != "# Deploy\n" {
		t.Errorf("a caller changed the served bytes: %q", again)
	}
}

func TestProjectSkillBundleAnswersOnlyInThatView(t *testing.T) {
	snap := load(t, map[string]string{
		globalSkillFile("deploy", "SKILL.md"):        "global",
		projectSkillFile("shop-only", "SKILL.md"):    "shop",
		projectSkillFile("shop-only", "notes/a.txt"): "a",
	})
	for _, proj := range []string{govProject, strings.ToUpper(govProject)} {
		if got := skillAnswer(t, snap, proj, "shop-only"); got != govProject+"/shop" {
			t.Errorf("SkillIn(%q, shop-only) = %s", proj, got)
		}
		if b, err := snap.SkillFile(proj, "shop-only", "notes/a.txt"); err != nil || string(b) != "a" {
			t.Errorf("SkillFile(%q, shop-only, notes/a.txt) = %q, %v", proj, b, err)
		}
	}
	for _, proj := range []string{"", "github.com/acme/other", "github.com/acme/unregistered"} {
		if got := skillAnswer(t, snap, proj, "shop-only"); got != "none" {
			t.Errorf("SkillIn(%q, shop-only) = %s; a Project skill is not in another view", proj, got)
		}
		if _, err := snap.SkillFile(proj, "shop-only", "SKILL.md"); err == nil {
			t.Errorf("SkillFile(%q, shop-only) served a Project skill outside its view", proj)
		}
		if got := skillAnswer(t, snap, proj, "deploy"); got != engine.ScopeGlobal+"/global" {
			t.Errorf("SkillIn(%q, deploy) = %s, want the global bundle", proj, got)
		}
	}
}

func TestLocalSkillBundleAnswersInEveryView(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalSkillFile("deploy", "SKILL.md"): "global",
		localSkillFile("mine", "SKILL.md"):    "local",
		localSkillFile("mine", "tools/x.txt"): "x",
	})
	for _, proj := range localViews {
		if got := skillAnswer(t, snap, proj, "mine"); got != engine.ScopeLocal+"/local" {
			t.Errorf("SkillIn(%q, mine) = %s", proj, got)
		}
		if b, err := snap.SkillFile(proj, "mine", "tools/x.txt"); err != nil || string(b) != "x" {
			t.Errorf("SkillFile(%q, mine, tools/x.txt) = %q, %v", proj, b, err)
		}
		if got := skillAnswer(t, snap, proj, "deploy"); got != engine.ScopeGlobal+"/global" {
			t.Errorf("SkillIn(%q, deploy) = %s", proj, got)
		}
	}
}

// The nearest Scope wins with its whole bundle: a file only the wider bundle
// has is not served from the winner, and the winner's files are not mixed with
// the wider bundle's.
func TestNearestScopeSkillWinsWithItsWholeBundle(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalSkillFile("deploy", "SKILL.md"):          "global",
		globalSkillFile("deploy", "global-only.txt"):   "g",
		globalSkillFile("deploy", "shared.txt"):        "global shared",
		projectSkillFile("deploy", "SKILL.md"):         "project",
		projectSkillFile("deploy", "project-only.txt"): "p",
		projectSkillFile("deploy", "shared.txt"):       "project shared",
		globalSkillFile("tidy", "SKILL.md"):            "global",
		globalSkillFile("tidy", "global-only.txt"):     "g",
		projectSkillFile("tidy", "SKILL.md"):           "project",
		projectSkillFile("tidy", "project-only.txt"):   "p",
		localSkillFile("tidy", "SKILL.md"):             "local",
		localSkillFile("tidy", "local-only.txt"):       "l",
	})

	// Project over global, in the Project's view only.
	if got := skillAnswer(t, snap, govProject, "deploy"); got != govProject+"/project" {
		t.Errorf("SkillIn(project, deploy) = %s, want the Project bundle", got)
	}
	s, _ := snap.SkillIn(govProject, "deploy")
	if want := []string{"SKILL.md", "project-only.txt", "shared.txt"}; !slices.Equal(s.Files, want) {
		t.Errorf("project deploy Files = %v, want %v", s.Files, want)
	}
	if b, err := snap.SkillFile(govProject, "deploy", "shared.txt"); err != nil || string(b) != "project shared" {
		t.Errorf("shared.txt = %q, %v, want the Project's", b, err)
	}
	if b, err := snap.SkillFile(govProject, "deploy", "global-only.txt"); err == nil {
		t.Errorf("a file only the global bundle has was served from the Project bundle: %q", b)
	}
	if got := skillAnswer(t, snap, "", "deploy"); got != engine.ScopeGlobal+"/global" {
		t.Errorf("SkillIn(\"\", deploy) = %s, want the global bundle", got)
	}
	if b, err := snap.SkillFile("", "deploy", "shared.txt"); err != nil || string(b) != "global shared" {
		t.Errorf("global shared.txt = %q, %v", b, err)
	}

	// Local over Project over global, in every view.
	for _, proj := range localViews {
		if got := skillAnswer(t, snap, proj, "tidy"); got != engine.ScopeLocal+"/local" {
			t.Errorf("SkillIn(%q, tidy) = %s, want the local bundle", proj, got)
		}
		for _, other := range []string{"global-only.txt", "project-only.txt"} {
			if b, err := snap.SkillFile(proj, "tidy", other); err == nil {
				t.Errorf("SkillFile(%q, tidy, %s) = %q; a wider bundle's file leaked into the local one", proj, other, b)
			}
		}
		if b, err := snap.SkillFile(proj, "tidy", "local-only.txt"); err != nil || string(b) != "l" {
			t.Errorf("SkillFile(%q, tidy, local-only.txt) = %q, %v", proj, b, err)
		}
	}
}

// Reads go through the Source's own filesystem, so a bundle in a git Source is
// served too, and its symlinks are not followed.
func TestSkillBundleLoadsFromAGitSourceAndDoesNotFollowItsSymlinks(t *testing.T) {
	repo := newGitRepo(t)
	repo.commit("main", map[string]string{
		globalSkillFile("deploy", "SKILL.md"):   "# Deploy\n",
		globalSkillFile("deploy", "scripts/a"):  "a",
		globalSkillFile("deploy", "link.txt"):   "symlink:SKILL.md",
		globalSkillFile("deploy", "escape.txt"): "symlink:../../../../etc/passwd",
	})
	snap, err := newGit(t, engine.GitOptions{URL: repo.url}).Load(t.Context())
	if err != nil {
		return // refusing the symlinks at load is as safe as skipping them
	}
	if b, err := snap.SkillFile("", "deploy", "scripts/a"); err != nil || string(b) != "a" {
		t.Errorf("SkillFile(scripts/a) = %q, %v", b, err)
	}
	for _, link := range []string{"link.txt", "escape.txt"} {
		if b, err := snap.SkillFile("", "deploy", link); err == nil {
			t.Errorf("SkillFile(%s) served a symlink: %q", link, b)
		}
	}
}

// --- Criterion 2: through contentFS and the budget, as for agents ---

// The 1 MiB file cap of articles and agents holds for a bundle's files, SKILL.md
// and supporting alike. No other cap is added: a file of exactly the cap loads.
func TestSkillFileOfExactlyTheCapLoadsAndOneByteMoreFails(t *testing.T) {
	const capBytes = 1 << 20
	for name, file := range map[string]func(string, string) string{
		"global":  globalSkillFile,
		"project": projectSkillFile,
		"local":   localSkillFile,
	} {
		for _, which := range []string{"SKILL.md", "data/blob.bin"} {
			t.Run(name+"/"+which, func(t *testing.T) {
				files := func(size int) map[string]string {
					m := map[string]string{file("big", "SKILL.md"): "# Big\n"}
					m[file("big", which)] = strings.Repeat("x", size)
					return m
				}
				msg := loadLocalErr(t, files(capBytes+1), file("big", which), "larger than 1048576 bytes")
				if len(msg) > 1<<10 {
					t.Errorf("error is %d bytes; it must not carry the content", len(msg))
				}
				snap := loadLocal(t, files(capBytes))
				b, err := snap.SkillFile(govProject, "big", which)
				if err != nil || len(b) != capBytes {
					t.Errorf("a file of exactly the cap: %d bytes, %v; want it served whole", len(b), err)
				}
			})
		}
	}
}

// --- Criterion 3: refused paths and symlinks, and a bundle without SKILL.md ---

func TestSkillFileRefusesPathsThatLeaveTheBundle(t *testing.T) {
	snap := load(t, map[string]string{
		globalSkillFile("deploy", "SKILL.md"): "# Deploy\n",
		globalSkillFile("deploy", "ok.txt"):   "ok",
		globalSkillFile("other", "SKILL.md"):  "# Other, not yours\n",
		globalFile("a"):                       article("A"),
		"global/skills/secret.txt":            "SECRET",
	})
	for _, p := range []string{
		"..",
		"../other/SKILL.md",
		"../../articles/a.md",
		"../../../global/articles/a.md",
		"../../skills/secret.txt",
		"../../../../../../../../etc/passwd",
		"./../other/SKILL.md",
		"scripts/../../other/SKILL.md",
		"ok.txt/../../other/SKILL.md",
		"/etc/passwd",
		"/global/skills/other/SKILL.md",
		"",
		".",
		"ok.txt/",
		"ok.txt\x00",
	} {
		b, err := snap.SkillFile("", "deploy", p)
		if err == nil {
			t.Errorf("SkillFile(%q) = %q, want a refusal", p, b)
			continue
		}
		if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "not yours") {
			t.Errorf("SkillFile(%q) leaked content with its error: %q", p, b)
		}
	}
	// The same holds for a skill name that tries to leave the skills directory.
	for _, name := range []string{"..", "../articles", "deploy/..", "deploy/../other", "/deploy", ""} {
		if b, err := snap.SkillFile("", name, "SKILL.md"); err == nil {
			t.Errorf("SkillFile(name %q, SKILL.md) = %q, want a refusal", name, b)
		}
		if s, ok := snap.SkillIn("", name); ok {
			t.Errorf("SkillIn(%q) = %+v, want nothing", name, s)
		}
	}
}

// A symlink in a bundle, to a file outside the Source or to another file in it,
// is never followed: Load refuses it or loads without it, and SkillFile refuses
// it. Nothing it points at is served.
func TestSymlinkInASkillBundleIsNotFollowed(t *testing.T) {
	outside := writeFiles(t, map[string]string{"secret.txt": "SECRET", "dir/inner.txt": "SECRET"})
	for name, setup := range map[string]func(t *testing.T, root string) error{
		"file outside the root": func(t *testing.T, root string) error {
			return symlink(t, filepath.Join(outside, "secret.txt"), filepath.Join(root, "global/skills/deploy/link.txt"))
		},
		"file inside the root": func(t *testing.T, root string) error {
			writeTo(t, root, "global/articles/a.md", article("A"))
			return symlink(t, "../../articles/a.md", filepath.Join(root, "global/skills/deploy/link.txt"))
		},
		"sibling file in the bundle": func(t *testing.T, root string) error {
			return symlink(t, "SKILL.md", filepath.Join(root, "global/skills/deploy/link.txt"))
		},
		"directory outside the root": func(t *testing.T, root string) error {
			return symlink(t, filepath.Join(outside, "dir"), filepath.Join(root, "global/skills/deploy/link.txt"))
		},
		"directory used as a path prefix": func(t *testing.T, root string) error {
			return symlink(t, filepath.Join(outside, "dir"), filepath.Join(root, "global/skills/deploy/sub"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeFiles(t, map[string]string{globalSkillFile("deploy", "SKILL.md"): "# Deploy\n"})
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
			for _, p := range []string{"link.txt", "sub/inner.txt", "sub"} {
				if b, err := snap.SkillFile("", "deploy", p); err == nil {
					t.Errorf("SkillFile(%q) = %q; a symlink was followed", p, b)
				}
			}
			if s, ok := snap.SkillIn("", "deploy"); ok {
				for _, f := range s.Files {
					if f == "link.txt" || strings.HasPrefix(f, "sub/") {
						t.Errorf("Files lists %q, reached through a symlink", f)
					}
				}
			}
		})
	}
}

// A symlinked skill directory, or skills directory, is not followed, at any Scope.
func TestSymlinkedSkillDirectoryIsNotFollowed(t *testing.T) {
	outside := writeFiles(t, map[string]string{"deploy/SKILL.md": "SECRET", "SKILL.md": "SECRET"})
	for name, setup := range map[string]func(t *testing.T, root string) error{
		"global skill directory": func(t *testing.T, root string) error {
			return symlink(t, filepath.Join(outside, "deploy"), filepath.Join(root, "global/skills/deploy"))
		},
		"global skills directory": func(t *testing.T, root string) error {
			return symlink(t, outside, filepath.Join(root, "global/skills"))
		},
		"Project skill directory": func(t *testing.T, root string) error {
			return symlink(t, outside, filepath.Join(root, "projects", filepath.FromSlash(govProject), "skills/deploy"))
		},
		"Project skills directory": func(t *testing.T, root string) error {
			return symlink(t, outside, filepath.Join(root, "projects", filepath.FromSlash(govProject), "skills"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeFiles(t, map[string]string{globalFile("a"): article("A")})
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
			for _, proj := range []string{"", govProject} {
				if b, err := snap.SkillFile(proj, "deploy", "SKILL.md"); err == nil {
					t.Errorf("SkillFile(%q, deploy, SKILL.md) = %q; a symlinked directory was followed", proj, b)
				}
			}
		})
	}
}

func TestSymlinkedLocalSkillsAreNotFollowed(t *testing.T) {
	outside := writeFiles(t, map[string]string{"deploy/SKILL.md": "SECRET", "secret.txt": "SECRET"})
	for name, setup := range map[string]func(t *testing.T, repo string) error{
		"skills directory": func(t *testing.T, repo string) error {
			return symlink(t, outside, filepath.Join(repo, ".goldie", "skills"))
		},
		"skill directory": func(t *testing.T, repo string) error {
			return symlink(t, filepath.Join(outside, "deploy"), filepath.Join(repo, ".goldie", "skills", "deploy"))
		},
		"file in a bundle": func(t *testing.T, repo string) error {
			writeTo(t, repo, localSkillFile("deploy", "SKILL.md"), "# Deploy\n")
			return symlink(t, filepath.Join(outside, "secret.txt"), filepath.Join(repo, filepath.FromSlash(localSkillFile("deploy", "link.txt"))))
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
				for _, p := range []string{"SKILL.md", "link.txt"} {
					if b, err := snap.SkillFile(proj, "deploy", p); err == nil && strings.Contains(string(b), "SECRET") {
						t.Errorf("SkillFile(%q, deploy, %s) served what a symlink points at", proj, p)
					}
				}
			}
		})
	}
}

// A skill directory without a SKILL.md fails the load, in every Scope, naming
// the directory and never a host path.
func TestSkillDirectoryWithoutSkillMDFailsTheLoad(t *testing.T) {
	for name, file := range map[string]func(string, string) string{
		"global":  globalSkillFile,
		"project": projectSkillFile,
		"local":   localSkillFile,
	} {
		t.Run(name, func(t *testing.T) {
			dir := strings.TrimSuffix(file("broken", "x"), "/x")
			for what, files := range map[string]map[string]string{
				"only supporting files": {file("broken", "notes.txt"): "n"},
				"nested SKILL.md only":  {file("broken", "sub/SKILL.md"): "# nested\n"},
			} {
				files[file("fine", "SKILL.md")] = "# Fine\n"
				msg := loadLocalErr(t, files, dir)
				if !strings.Contains(msg, "SKILL.md") {
					t.Errorf("%s: error %q does not say what is missing", what, msg)
				}
			}
		})
	}
}

func TestEmptySkillDirectoryFailsTheLoad(t *testing.T) {
	root := writeFiles(t, map[string]string{globalSkillFile("fine", "SKILL.md"): "# Fine\n"})
	if err := os.MkdirAll(filepath.Join(root, "global/skills/broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	snap, err := engine.NewDirSource(root).Load(t.Context())
	if err == nil {
		_, ok := snap.SkillIn("", "broken")
		t.Fatalf("Load succeeded (empty skill served: %v), want an error", ok)
	}
	if !strings.Contains(err.Error(), "global/skills/broken") || strings.Contains(err.Error(), root) {
		t.Errorf("error = %q, want it to name global/skills/broken and no host path", err)
	}
}
