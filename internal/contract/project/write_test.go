package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// Acceptance tests for #282: the package that reads .goldie/config.yaml also
// writes it, and exposes the repository root the file belongs at.
//
//	func RepositoryRoot(dir string) (root string, ok bool)
//	func WriteConfig(root, id string) error
//
// RepositoryRoot is the nearest directory at or above dir that holds a .git
// entry (a directory, or the file of a worktree or submodule), as an absolute
// path; ok is false outside a repository. WriteConfig writes
// root/.goldie/config.yaml holding `project: <id>`, with the id normalised as
// a typed Project is, and refuses with these errors (callers word them):
//
//	ErrNotRepository     root holds no .git entry
//	ErrSourceRepository  root holds global/articles or projects/: a Source
//	ErrConfigExists      .goldie/config.yaml exists, as anything, even a symlink
//	ErrSymlink           .goldie is a symlink
//
// An id that does not normalise is an error that never echoes it. Every
// refusal writes nothing.

// absDir is filepath.Abs, failing the test on error.
func absDir(t *testing.T, dir string) string {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// assertNoGoldieDir fails when dir/.goldie exists in any form.
func assertNoGoldieDir(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, ".goldie")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s/.goldie exists (err = %v), want nothing written", dir, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRepositoryRoot(t *testing.T) {
	plain := repo(t, forkConfig)
	deep := mkdir(t, filepath.Join(plain, "a", "b"))

	main := repo(t, forkConfig)
	wtGit := filepath.Join(main, ".git", "worktrees", "feature")
	write(t, filepath.Join(wtGit, "commondir"), "../..\n")
	wt := t.TempDir()
	write(t, filepath.Join(wt, ".git"), "gitdir: "+wtGit+"\n")
	wtDeep := mkdir(t, filepath.Join(wt, "pkg"))

	super := t.TempDir()
	write(t, filepath.Join(super, ".git", "modules", "sub", "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/sub.git\n")
	write(t, filepath.Join(super, "sub", ".git"), "gitdir: ../.git/modules/sub\n")
	subDeep := mkdir(t, filepath.Join(super, "sub", "x"))

	outer := repo(t, forkConfig)
	inner := filepath.Join(outer, "vendor", "inner")
	write(t, filepath.Join(inner, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/inner.git\n")
	innerDeep := mkdir(t, filepath.Join(inner, "pkg", "deep"))

	// A .git file that is not a gitdir pointer still marks the boundary: the
	// root is never looked for above it.
	garbage := t.TempDir()
	write(t, filepath.Join(garbage, ".git"), "garbage")

	cases := []struct {
		name, dir, want string
	}{
		{"at the root", plain, plain},
		{"from a subdirectory", deep, plain},
		{"worktree .git file", wtDeep, wt},
		{"submodule .git file", subDeep, filepath.Join(super, "sub")},
		{"nested repository is its own root", innerDeep, inner},
		{"garbage .git file", garbage, garbage},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := RepositoryRoot(c.dir)
			if !ok || got != absDir(t, c.want) {
				t.Errorf("RepositoryRoot(%s) = %q, %v; want %q, true", c.dir, got, ok, absDir(t, c.want))
			}
		})
	}

	if got, ok := RepositoryRoot(t.TempDir()); ok || got != "" {
		t.Errorf("outside a repository: RepositoryRoot = %q, %v; want \"\", false", got, ok)
	}
}

func TestWriteConfigWritesProjectAtRoot(t *testing.T) {
	root := repo(t, forkConfig)
	sub := mkdir(t, filepath.Join(root, "pkg", "deep"))
	if err := WriteConfig(root, "github.com/acme/widgets"); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	path := filepath.Join(root, ".goldie", "config.yaml")
	if got := readFile(t, path); got != "project: github.com/acme/widgets\n" {
		t.Errorf("config.yaml = %q, want %q", got, "project: github.com/acme/widgets\n")
	}
	assertNoGoldieDir(t, sub)

	// Mode: a regular file, readable and writable by the owner, writable by no
	// one else and executable by no one; the directory likewise.
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Mode().IsRegular() {
		t.Errorf("config.yaml mode = %v, want a regular file", fi.Mode())
	}
	if perm := fi.Mode().Perm(); perm&^0o644 != 0 || perm&0o600 != 0o600 {
		t.Errorf("config.yaml perm = %o, want owner rw, at most 0644", perm)
	}
	di, err := os.Lstat(filepath.Join(root, ".goldie"))
	if err != nil {
		t.Fatal(err)
	}
	if !di.IsDir() {
		t.Errorf(".goldie mode = %v, want a directory", di.Mode())
	}
	if perm := di.Mode().Perm(); perm&^0o755 != 0 || perm&0o700 != 0o700 {
		t.Errorf(".goldie perm = %o, want owner rwx, at most 0755", perm)
	}

	// The reader reads back exactly what the writer wrote, from the root and
	// from a subdirectory.
	for _, dir := range []string{root, sub} {
		got, err := Detect(dir, "", "")
		if err != nil || got.ID != "github.com/acme/widgets" || got.Source != SourceConfig {
			t.Errorf("Detect(%s) after WriteConfig = %+v, %v; want github.com/acme/widgets from config", dir, got, err)
		}
	}
}

// TestWriteConfigNormalisesTheID checks that the id is normalised as a typed
// Project is before it is written: lower case, and a remote URL becomes
// host/org/repo without its userinfo.
func TestWriteConfigNormalisesTheID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Ex.com/Org_1/Re-Po.v2", "ex.com/org_1/re-po.v2"},
		{"git@GitHub.com:Acme/Widgets.git", "github.com/acme/widgets"},
		{"https://user:s3cret-token@GitHub.com/Acme/Widgets.git", "github.com/acme/widgets"},
	}
	for _, c := range cases {
		root := repo(t, forkConfig)
		if err := WriteConfig(root, c.in); err != nil {
			t.Errorf("WriteConfig(%q): %v", c.in, err)
			continue
		}
		got := readFile(t, filepath.Join(root, ".goldie", "config.yaml"))
		if got != "project: "+c.want+"\n" {
			t.Errorf("WriteConfig(%q) wrote %q, want %q", c.in, got, "project: "+c.want+"\n")
		}
		if strings.Contains(got, "s3cret") {
			t.Errorf("WriteConfig(%q) wrote the credential: %q", c.in, got)
		}
		if d, err := Detect(root, "", ""); err != nil || d.ID != c.want {
			t.Errorf("round trip of %q: %+v, %v", c.in, d, err)
		}
	}
}

// hostileIDs are ids that must never reach the file: path syntax, YAML that
// tries to add a key, hidden and control runes, credential-bearing values that
// cannot be read as a remote, and the wrong shape.
//
//nolint:staticcheck // ST1018: the hostile ids hold format characters on purpose.
var hostileIDs = []string{
	"",
	"   ",
	"../../etc/passwd",
	"/etc/passwd",
	"github.com/acme/widgets\nproject: evil.example/x/y",
	"github.com/acme/widgets\r\nother: evil",
	"github.com/acme/widgets # evil",
	"github.com/acme/wid​gets",
	"github.com/acme/widgets‮",
	"github.com/acme/⁦widgets",
	"github.com/acme/widgets\x00",
	"github.com/acme/wid\x1b[31mgets",
	"user:s3cret-token@host",
	"https://user:s3cret-token@",
	"https://user:s3cret-token@github.com/acme/../../x",
	"gitlab.com/group/sub/app",
	"github.com/acme",
	"%2e%2e/x/y",
	"{project: evil.example/x/y}",
}

func TestWriteConfigRefusesHostileID(t *testing.T) {
	for _, id := range hostileIDs {
		root := repo(t, forkConfig)
		err := WriteConfig(root, id)
		if err == nil {
			t.Errorf("WriteConfig(%q) = nil, want an error", id)
		}
		if err != nil {
			msg := err.Error()
			if (strings.TrimSpace(id) != "" && strings.Contains(msg, id)) || strings.Contains(msg, "s3cret") || strings.Contains(msg, "evil") || strings.ContainsFunc(msg, contract.UnsafeRune) {
				t.Errorf("WriteConfig(%q) error %q echoes the id", id, msg)
			}
		}
		assertNoGoldieDir(t, root)
	}
}

func TestWriteConfigNeverOverwrites(t *testing.T) {
	root := repo(t, forkConfig)
	path := filepath.Join(root, ".goldie", "config.yaml")
	const old = "# mine\nproject: github.com/me/mine\n"
	write(t, path, old)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	err = WriteConfig(root, "github.com/acme/widgets")
	if !errors.Is(err, ErrConfigExists) {
		t.Fatalf("WriteConfig over an existing config: err = %v, want ErrConfigExists", err)
	}
	if got := readFile(t, path); got != old {
		t.Errorf("config.yaml = %q, want it untouched: %q", got, old)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("config.yaml mode or mtime changed: %v %v -> %v %v", before.Mode(), before.ModTime(), after.Mode(), after.ModTime())
	}

	// An empty config is still a config.
	empty := repo(t, forkConfig)
	write(t, filepath.Join(empty, ".goldie", "config.yaml"), "")
	if err := WriteConfig(empty, "github.com/acme/widgets"); !errors.Is(err, ErrConfigExists) {
		t.Errorf("WriteConfig over an empty config: err = %v, want ErrConfigExists", err)
	}
	if got := readFile(t, filepath.Join(empty, ".goldie", "config.yaml")); got != "" {
		t.Errorf("empty config.yaml = %q, want it untouched", got)
	}

	// A directory where the file would go is refused and left alone.
	dirAsFile := repo(t, forkConfig)
	write(t, filepath.Join(dirAsFile, ".goldie", "config.yaml", "keep"), "keep\n")
	if err := WriteConfig(dirAsFile, "github.com/acme/widgets"); err == nil {
		t.Error("WriteConfig with a directory at config.yaml: want an error")
	}
	if got := readFile(t, filepath.Join(dirAsFile, ".goldie", "config.yaml", "keep")); got != "keep\n" {
		t.Errorf("file inside the config.yaml directory = %q, want it untouched", got)
	}

	// A regular file where .goldie/ would go is refused and left alone.
	fileAsDir := repo(t, forkConfig)
	write(t, filepath.Join(fileAsDir, ".goldie"), "not a dir\n")
	if err := WriteConfig(fileAsDir, "github.com/acme/widgets"); err == nil {
		t.Error("WriteConfig with a file at .goldie: want an error")
	}
	if got := readFile(t, filepath.Join(fileAsDir, ".goldie")); got != "not a dir\n" {
		t.Errorf(".goldie file = %q, want it untouched", got)
	}
}

// TestWriteConfigKeepsOtherGoldieFiles checks that an existing .goldie/
// directory is used as it is, and its other files stay byte-identical.
func TestWriteConfigKeepsOtherGoldieFiles(t *testing.T) {
	root := repo(t, forkConfig)
	other := filepath.Join(root, ".goldie", "notes.md")
	write(t, other, "keep me\n")
	if err := WriteConfig(root, "github.com/acme/widgets"); err != nil {
		t.Fatalf("WriteConfig into an existing .goldie/: %v", err)
	}
	if got := readFile(t, other); got != "keep me\n" {
		t.Errorf("notes.md = %q, want it untouched", got)
	}
	if got := readFile(t, filepath.Join(root, ".goldie", "config.yaml")); got != "project: github.com/acme/widgets\n" {
		t.Errorf("config.yaml = %q", got)
	}
}

// TestWriteConfigOnlyAtRepositoryRoot checks that WriteConfig writes only into
// a directory that is a repository root, whether its .git is a directory or a
// worktree's or submodule's file.
func TestWriteConfigOnlyAtRepositoryRoot(t *testing.T) {
	root := repo(t, forkConfig)
	sub := mkdir(t, filepath.Join(root, "pkg"))
	if err := WriteConfig(sub, "github.com/acme/widgets"); !errors.Is(err, ErrNotRepository) {
		t.Errorf("WriteConfig(subdirectory): err = %v, want ErrNotRepository", err)
	}
	assertNoGoldieDir(t, sub)
	assertNoGoldieDir(t, root)

	plain := t.TempDir()
	if err := WriteConfig(plain, "github.com/acme/widgets"); !errors.Is(err, ErrNotRepository) {
		t.Errorf("WriteConfig(not a repository): err = %v, want ErrNotRepository", err)
	}
	assertNoGoldieDir(t, plain)

	main := repo(t, forkConfig)
	wtGit := filepath.Join(main, ".git", "worktrees", "feature")
	write(t, filepath.Join(wtGit, "commondir"), "../..\n")
	wt := t.TempDir()
	write(t, filepath.Join(wt, ".git"), "gitdir: "+wtGit+"\n")
	if err := WriteConfig(wt, "github.com/acme/widgets"); err != nil {
		t.Fatalf("WriteConfig(worktree): %v", err)
	}
	if got := readFile(t, filepath.Join(wt, ".goldie", "config.yaml")); got != "project: github.com/acme/widgets\n" {
		t.Errorf("worktree config.yaml = %q", got)
	}
	assertNoGoldieDir(t, main)
	if _, err := os.Lstat(filepath.Join(wtGit, ".goldie")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("wrote into the worktree's gitdir: %v", err)
	}

	super := t.TempDir()
	write(t, filepath.Join(super, ".git", "modules", "sub", "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/sub.git\n")
	subm := filepath.Join(super, "sub")
	write(t, filepath.Join(subm, ".git"), "gitdir: ../.git/modules/sub\n")
	if err := WriteConfig(subm, "github.com/acme/sub"); err != nil {
		t.Fatalf("WriteConfig(submodule): %v", err)
	}
	if got := readFile(t, filepath.Join(subm, ".goldie", "config.yaml")); got != "project: github.com/acme/sub\n" {
		t.Errorf("submodule config.yaml = %q", got)
	}
	assertNoGoldieDir(t, super)
}

func TestWriteConfigRefusesSourceRepository(t *testing.T) {
	for name, marker := range map[string]string{
		"global/articles": filepath.Join("global", "articles", "deploy.md"),
		"projects/":       filepath.Join("projects", "github.com", "acme", "shop", "articles", "deploy.md"),
	} {
		t.Run(name, func(t *testing.T) {
			root := repo(t, forkConfig)
			write(t, filepath.Join(root, marker), "---\nname: x\n---\n")
			if err := WriteConfig(root, "github.com/acme/widgets"); !errors.Is(err, ErrSourceRepository) {
				t.Errorf("err = %v, want ErrSourceRepository", err)
			}
			assertNoGoldieDir(t, root)
		})
	}
	// Empty marker directories count too.
	for _, d := range []string{filepath.Join("global", "articles"), "projects"} {
		root := repo(t, forkConfig)
		mkdir(t, filepath.Join(root, d))
		if err := WriteConfig(root, "github.com/acme/widgets"); !errors.Is(err, ErrSourceRepository) {
			t.Errorf("empty %s: err = %v, want ErrSourceRepository", d, err)
		}
		assertNoGoldieDir(t, root)
	}
	// A global/ without articles, or a projects/ below the root, is not a Source.
	for _, d := range []string{"global", filepath.Join("docs", "projects"), filepath.Join("pkg", "global", "articles")} {
		root := repo(t, forkConfig)
		mkdir(t, filepath.Join(root, d))
		if err := WriteConfig(root, "github.com/acme/widgets"); err != nil {
			t.Errorf("with only %s: WriteConfig = %v, want it written", d, err)
		}
	}
}

// FuzzWriteConfig checks that any id either writes nothing and returns an
// error, or writes a file the reader reads back as a valid Project. That the
// error never echoes the id is TestWriteConfigRefusesHostileID's job.
func FuzzWriteConfig(f *testing.F) {
	for _, s := range append([]string{"github.com/acme/widgets", "git@h.example:a/b.git", "https://u:p@h.example/a/b"}, hostileIDs...) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, id string) {
		root := repo(t, forkConfig)
		if err := WriteConfig(root, id); err != nil {
			assertNoGoldieDir(t, root)
			return
		}
		got := readFile(t, filepath.Join(root, ".goldie", "config.yaml"))
		d, err := Detect(root, "", "")
		if err != nil || d.Source != SourceConfig || !contract.ValidProjectID(d.ID) || got != "project: "+d.ID+"\n" {
			t.Errorf("WriteConfig(%q) wrote %q, read back %+v, %v", id, got, d, err)
		}
	})
}
