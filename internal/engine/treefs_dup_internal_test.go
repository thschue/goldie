package engine

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

// Two entries with one name make a tree read two ways: a path lookup and a
// directory listing could pick different ones. git fsck rejects such trees,
// and so does the load.
func TestLoadFSRejectsDuplicateEntryNames(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	article := func(name string) object.TreeEntry {
		return object.TreeEntry{Name: "a.md", Mode: filemode.Regular, Hash: b.blob("---\nname: " + name + "\n---\n")}
	}
	one := b.tree(object.TreeEntry{Name: "articles", Mode: filemode.Dir, Hash: b.tree(article("one"))})
	two := b.tree(object.TreeEntry{Name: "articles", Mode: filemode.Dir, Hash: b.tree(article("two"))})
	for name, root := range map[string]plumbing.Hash{
		"directory on the path": b.tree(
			object.TreeEntry{Name: "global", Mode: filemode.Dir, Hash: one},
			object.TreeEntry{Name: "global", Mode: filemode.Dir, Hash: two},
		),
		"listed file": b.underGlobalArticles(b.tree(article("one"), article("two"))),
	} {
		if snap, err := b.load(root); err == nil {
			a, _ := snap.Article("a")
			t.Errorf("%s: loaded %q", name, a.Name)
		}
	}
}
