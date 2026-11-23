package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Acceptance tests for #424: RootSource reads through an *os.Root the caller
// opened, so replacing the path with a symlink afterwards cannot redirect it.

func articleIDs(snap *engine.Snapshot) []string {
	var ids []string
	for _, a := range snap.Articles() {
		ids = append(ids, a.ID)
	}
	return ids
}

func openTestRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func TestRootSourceReadsTheOpenedDirectoryAfterAPathSwap(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	for dir, name := range map[string]string{a: "from-a", b: "from-b"} {
		if err := os.MkdirAll(filepath.Join(dir, "global", "articles"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "global", "articles", name+".md"), []byte(article(name)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := openTestRoot(t, a)

	if err := os.Rename(a, a+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}

	src := engine.NewRootSource(root)
	for _, attempt := range []string{"first", "second"} {
		snap, err := src.Load(context.Background())
		if err != nil {
			t.Fatalf("%s Load: %v", attempt, err)
		}
		if got, want := articleIDs(snap), []string{"from-a"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s Load articles = %v, want %v (never the outside directory's)", attempt, got, want)
		}
	}
}

// Load never closes the root: the caller owns it.
func TestRootSourceLoadLeavesTheRootOpen(t *testing.T) {
	root := openTestRoot(t, writeFiles(t, map[string]string{"global/articles/x.md": article("X")}))
	if _, err := engine.NewRootSource(root).Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Stat("."); err != nil {
		t.Errorf("the root is closed after Load: %v", err)
	}
}

// A Load whose context has ended loads nothing, like every Source.
func TestRootSourceLoadWithEndedContextFails(t *testing.T) {
	root := openTestRoot(t, writeFiles(t, map[string]string{"global/articles/x.md": article("X")}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.NewRootSource(root).Load(ctx); err == nil {
		t.Error("Load with a cancelled context succeeded")
	}
}
