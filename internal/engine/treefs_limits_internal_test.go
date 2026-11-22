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
func (b treeBuilder) underGlobalArticles(articles plumbing.Hash) treeFS {
	b.t.Helper()
	global := b.tree(object.TreeEntry{Name: "articles", Mode: filemode.Dir, Hash: articles})
	return b.fs(b.tree(object.TreeEntry{Name: "global", Mode: filemode.Dir, Hash: global}))
}

// loadWithin fails the test if loadFS runs longer than d, so a missing limit
// shows up as a failure instead of a hung suite.
func loadWithin(t *testing.T, d time.Duration, fsys fs.FS) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { _, err := loadFS(fsys); done <- err }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("loadFS still running after %v", d)
		return nil
	}
}

func TestLoadFSStopsAtTreeStepLimit(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	// 16^7 paths from 7 objects; plain files, so only the walk itself counts.
	articles := b.bomb(16, 6, object.TreeEntry{Name: "f", Mode: filemode.Regular, Hash: b.blob("x")})
	if err := loadWithin(t, time.Minute, b.underGlobalArticles(articles)); !errors.Is(err, errTreeTooLarge) {
		t.Fatalf("err = %v, want errTreeTooLarge", err)
	}
}

func TestLoadFSStopsAtTreeByteLimit(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	md := b.blob("---\nname: A\n---\n" + strings.Repeat("x", 100))
	articles := b.bomb(4, 0, object.TreeEntry{Name: ".md", Mode: filemode.Regular, Hash: md})
	if _, err := loadFS(b.underGlobalArticles(articles)); err != nil {
		t.Fatalf("within the limit: %v", err)
	}
	fsys := b.underGlobalArticles(articles)
	fsys.b.bytes = 300 // four articles of 116 bytes each
	if _, err := loadFS(fsys); !errors.Is(err, errTreeTooLarge) {
		t.Fatalf("err = %v, want errTreeTooLarge", err)
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
