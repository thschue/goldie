package engine

import (
	"io/fs"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

// A tree whose entries name objects the store never received (a corrupt or
// shallow tree) fails the load instead of loading an empty article.
func TestLoadFSFailsOnMissingObjects(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	missing := plumbing.NewHash("0123456789012345678901234567890123456789")
	fsys := b.underGlobalArticles(b.tree(
		object.TreeEntry{Name: "x.md", Mode: filemode.Regular, Hash: missing},
		object.TreeEntry{Name: "link", Mode: filemode.Symlink, Hash: missing},
	))
	if snap, err := loadFS(fsys); err == nil {
		t.Fatalf("loaded %d articles", len(snap.Articles()))
	}
	if _, err := fs.ReadLink(fsys, "global/articles/link"); err == nil {
		t.Error("ReadLink of a missing blob succeeded")
	}
	if fi, err := fs.Lstat(fsys, "global/articles/x.md"); err != nil || fi.Size() != 0 {
		t.Errorf("Lstat = %v, %v; want the entry with size 0", fi, err)
	}
	if _, err := fs.Stat(b.fs(b.tree(object.TreeEntry{Name: "d", Mode: filemode.Dir, Hash: missing})), "d/x"); err == nil {
		t.Error("Stat through a missing subtree succeeded")
	}
}
