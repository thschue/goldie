package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

// bomb builds a tree that references one subtree width times per level: few
// objects, but width^(depth+1) paths.
func (b treeBuilder) bomb(width, depth int, leaf object.TreeEntry) plumbing.Hash {
	b.t.Helper()
	var entries []object.TreeEntry
	for i := range width {
		e := leaf
		e.Name = fmt.Sprintf("%02d%s", i, leaf.Name)
		entries = append(entries, e)
	}
	h := b.tree(entries...)
	for range depth {
		entries = entries[:0]
		for i := range width {
			entries = append(entries, object.TreeEntry{Name: fmt.Sprintf("d%02d", i), Mode: filemode.Dir, Hash: h})
		}
		h = b.tree(entries...)
	}
	return h
}

// underGlobalArticles wraps a tree as global/articles.
func (b treeBuilder) underGlobalArticles(articles plumbing.Hash) plumbing.Hash {
	b.t.Helper()
	global := b.tree(object.TreeEntry{Name: "articles", Mode: filemode.Dir, Hash: articles})
	return b.tree(object.TreeEntry{Name: "global", Mode: filemode.Dir, Hash: global})
}

// loadWithin fails the test if loading root runs longer than d, so a missing
// limit shows up as a failure instead of a hung suite.
func (b treeBuilder) loadWithin(d time.Duration, root plumbing.Hash) error {
	b.t.Helper()
	tree, err := object.GetTree(b.sto, root)
	if err != nil {
		b.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := loadTree(tree); done <- err }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		b.t.Fatalf("load still running after %v", d)
		return nil
	}
}

func TestLoadFSStopsAtTreeStepLimit(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	// 16^7 paths from 7 objects; plain files, so only the walk itself counts.
	articles := b.bomb(16, 6, object.TreeEntry{Name: "f", Mode: filemode.Regular, Hash: b.blob("x")})
	if err := b.loadWithin(time.Minute, b.underGlobalArticles(articles)); !errors.Is(err, errLoadLimit) {
		t.Fatalf("err = %v, want errLoadLimit", err)
	}
}

// Every load gets the whole budget: a tree that needs more than half of it
// loads again and again.
func TestLoadTreeBudgetIsPerLoad(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	// 800^2 plain files: about 640k steps, more than half of maxTreeSteps.
	root := b.underGlobalArticles(b.bomb(800, 1, object.TreeEntry{Name: "f", Mode: filemode.Regular, Hash: b.blob("x")}))
	for i := range 2 {
		if err := b.loadWithin(time.Minute, root); err != nil {
			t.Fatalf("load %d: %v", i+1, err)
		}
	}
}

func TestLoadFSStopsAtTreeByteLimit(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	md := b.blob("---\nname: A\n---\n" + strings.Repeat("x", 100))
	articles := b.bomb(4, 0, object.TreeEntry{Name: ".md", Mode: filemode.Regular, Hash: md})
	if _, err := b.load(b.underGlobalArticles(articles)); err != nil {
		t.Fatalf("within the limit: %v", err)
	}
	fsys := b.fs(b.underGlobalArticles(articles))
	fsys.b.bytes = 300 // four articles of 116 bytes each
	if _, err := loadFS(fsys); !errors.Is(err, errLoadLimit) {
		t.Fatalf("err = %v, want errLoadLimit", err)
	}
}

func TestTreeFSRejectsOverlongPaths(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	long := strings.Repeat("d", 200)
	h := b.tree(object.TreeEntry{Name: "a.md", Mode: filemode.Regular, Hash: b.blob("---\nname: A\n---\n")})
	path := "a.md"
	for range 25 {
		h = b.tree(object.TreeEntry{Name: long, Mode: filemode.Dir, Hash: h})
		path = long + "/" + path
	}
	if _, err := fs.Stat(b.fs(h), path); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Stat of a %d-byte path: err = %v, want ErrInvalid", len(path), err)
	}
}

// ReadLink reads a blob too, so it spends the same byte budget as Open.
func TestTreeFSReadLinkSpendsTheByteBudget(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	fsys := b.fs(b.tree(object.TreeEntry{Name: "link", Mode: filemode.Symlink, Hash: b.blob("a/long/target")}))
	fsys.b.bytes = 4
	if _, err := fs.ReadLink(fsys, "link"); !errors.Is(err, errLoadLimit) {
		t.Fatalf("err = %v, want errLoadLimit", err)
	}
}
