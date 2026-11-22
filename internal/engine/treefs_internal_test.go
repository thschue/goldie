package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

// treeBuilder writes raw git objects, so a test can build trees no well-behaved
// client would produce.
type treeBuilder struct {
	t   *testing.T
	sto *memory.Storage
}

func (b treeBuilder) blob(content string) plumbing.Hash {
	b.t.Helper()
	obj := b.sto.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, _ := obj.Writer()
	_, _ = w.Write([]byte(content))
	_ = w.Close()
	h, err := b.sto.SetEncodedObject(obj)
	if err != nil {
		b.t.Fatal(err)
	}
	return h
}

// tree writes the raw tree format ("<mode> <name>\x00<hash>" per entry), in
// the order given and without the name checks go-git's encoder applies: a
// hostile remote is not bound by them either.
func (b treeBuilder) tree(entries ...object.TreeEntry) plumbing.Hash {
	b.t.Helper()
	obj := b.sto.NewEncodedObject()
	obj.SetType(plumbing.TreeObject)
	w, _ := obj.Writer()
	for _, e := range entries {
		_, _ = fmt.Fprintf(w, "%o %s\x00", uint32(e.Mode), e.Name)
		_, _ = w.Write(e.Hash[:])
	}
	_ = w.Close()
	h, err := b.sto.SetEncodedObject(obj)
	if err != nil {
		b.t.Fatal(err)
	}
	return h
}

func (b treeBuilder) fs(root plumbing.Hash) treeFS {
	b.t.Helper()
	tree, err := object.GetTree(b.sto, root)
	if err != nil {
		b.t.Fatal(err)
	}
	return newTreeFS(tree)
}

// load runs loadTree on root, the way a Source loads a commit.
func (b treeBuilder) load(root plumbing.Hash) (*Snapshot, error) {
	b.t.Helper()
	tree, err := object.GetTree(b.sto, root)
	if err != nil {
		b.t.Fatal(err)
	}
	return loadTree(tree)
}

func TestTreeFSConforms(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	md := b.blob("---\nname: A\n---\nbody")
	inner := b.tree(
		object.TreeEntry{Name: "a.md", Mode: filemode.Regular, Hash: md},
		object.TreeEntry{Name: "run.sh", Mode: filemode.Executable, Hash: md},
	)
	root := b.tree(
		object.TreeEntry{Name: "README", Mode: filemode.Regular, Hash: b.blob("hi")},
		object.TreeEntry{Name: "global", Mode: filemode.Dir, Hash: inner},
	)
	if err := fstest.TestFS(b.fs(root), "global/a.md", "global/run.sh", "README"); err != nil {
		t.Fatal(err)
	}
}

func TestTreeFSNeverFollowsSymlinks(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	dir := b.tree(object.TreeEntry{Name: "a.md", Mode: filemode.Regular, Hash: b.blob("x")})
	root := b.tree(
		object.TreeEntry{Name: "dir", Mode: filemode.Dir, Hash: dir},
		object.TreeEntry{Name: "link", Mode: filemode.Symlink, Hash: b.blob("dir")},
		object.TreeEntry{Name: "sub", Mode: filemode.Submodule, Hash: plumbing.NewHash("0123456789012345678901234567890123456789")},
	)
	fsys := b.fs(root)
	for _, name := range []string{"link", "link/a.md", "sub"} {
		if f, err := fsys.Open(name); err == nil {
			_ = f.Close()
			t.Errorf("Open(%q) succeeded", name)
		}
	}
	if _, err := fs.Stat(fsys, "link"); err == nil {
		t.Error("Stat followed a symlink")
	}
	fi, err := fs.Lstat(fsys, "link")
	if err != nil || fi.Mode().Type() != fs.ModeSymlink {
		t.Fatalf("Lstat = %v, %v", fi, err)
	}
	if target, err := fs.ReadLink(fsys, "link"); err != nil || target != "dir" {
		t.Fatalf("ReadLink = %q, %v", target, err)
	}
	if _, err := fs.Stat(fsys, "link/a.md"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("path through a symlink: err = %v, want ErrInvalid", err)
	}
}

// dirTree builds nested directory trees from one path, ending in entries.
func (b treeBuilder) dirTree(path string, entries ...object.TreeEntry) plumbing.Hash {
	b.t.Helper()
	parts := strings.Split(path, "/")
	h := b.tree(entries...)
	for i := len(parts) - 1; i > 0; i-- {
		h = b.tree(object.TreeEntry{Name: parts[i], Mode: filemode.Dir, Hash: h})
	}
	return b.tree(object.TreeEntry{Name: parts[0], Mode: filemode.Dir, Hash: h})
}

func TestLoadFSReadsATreeWithProjects(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	md := func(name string) object.TreeEntry {
		return object.TreeEntry{Name: name + ".md", Mode: filemode.Regular, Hash: b.blob("---\nname: " + name + "\n---\nbody")}
	}
	globalT, _ := object.GetTree(b.sto, b.dirTree("global/articles", md("a")))
	projT, _ := object.GetTree(b.sto, b.dirTree("projects/github.com/acme/shop/articles", md("b")))
	root := b.tree(globalT.Entries[0], projT.Entries[0])
	snap, err := b.load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Projects(); len(got) != 1 || got[0] != "github.com/acme/shop" {
		t.Fatalf("Projects = %v", got)
	}
	if a, ok := snap.Article("a"); !ok || a.Scope != ScopeGlobal {
		t.Fatalf("global article = %+v, %v", a, ok)
	}
	if a, ok := snap.ArticleIn("github.com/acme/shop", "b"); !ok || a.Scope != "github.com/acme/shop" {
		t.Fatalf("project article = %+v, %v", a, ok)
	}
}

// A symlinked global or global/articles dir must fail the load, not load
// nothing: a path through a symlink reads as fs.ErrInvalid, never ErrNotExist.
func TestLoadFSFailsOnSymlinkedDirs(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	real := b.tree(object.TreeEntry{Name: "a.md", Mode: filemode.Regular, Hash: b.blob("---\nname: A\n---\n")})
	realGlobal := b.tree(object.TreeEntry{Name: "articles", Mode: filemode.Dir, Hash: real})
	linkedArticles := b.tree(object.TreeEntry{Name: "articles", Mode: filemode.Symlink, Hash: b.blob("../real/articles")})
	for name, root := range map[string]plumbing.Hash{
		"global": b.tree(
			object.TreeEntry{Name: "global", Mode: filemode.Symlink, Hash: b.blob("real")},
			object.TreeEntry{Name: "real", Mode: filemode.Dir, Hash: realGlobal},
		),
		"global/articles": b.tree(
			object.TreeEntry{Name: "global", Mode: filemode.Dir, Hash: linkedArticles},
			object.TreeEntry{Name: "real", Mode: filemode.Dir, Hash: realGlobal},
		),
	} {
		if snap, err := b.load(root); err == nil {
			t.Errorf("symlinked %s: loaded %d articles", name, len(snap.Articles()))
		}
	}
}

func TestTreeFSRejectsTraversingNames(t *testing.T) {
	for _, name := range []string{"..", ".", "a/b", "a\x00b", ""} {
		b := treeBuilder{t, memory.NewStorage()}
		evil := b.tree(object.TreeEntry{Name: "x.md", Mode: filemode.Regular, Hash: b.blob("---\nname: X\n---\n")})
		articles := b.tree(object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: evil})
		global := b.tree(object.TreeEntry{Name: "articles", Mode: filemode.Dir, Hash: articles})
		root := b.tree(object.TreeEntry{Name: "global", Mode: filemode.Dir, Hash: global})
		snap, err := b.load(root)
		if err == nil {
			t.Errorf("entry %q: loaded %d articles", name, len(snap.Articles()))
		}
	}
}

// FuzzValidEntryName checks that an accepted tree entry name is always a single,
// non-traversing path element.
func FuzzValidEntryName(f *testing.F) {
	for _, s := range []string{"a.md", "..", ".", "", "a/b", "a\x00", ".git", "...", "a\\b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if !validEntryName(name) {
			return
		}
		if !fs.ValidPath(name) || strings.Contains(name, "/") || name == "." || name == ".." {
			t.Fatalf("accepted %q", name)
		}
	})
}
