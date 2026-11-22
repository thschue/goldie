package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzParseArticle checks that hostile frontmatter never panics and that an
// accepted article always has a name.
func FuzzParseArticle(f *testing.F) {
	f.Add([]byte("---\nname: A\nkeywords: [a, b]\n---\nbody"))
	f.Add([]byte("---\r\nname: A\r\n---\r\n"))
	f.Add([]byte("---\nname: &a [*a]\n---\n"))
	f.Add([]byte("---\n---\n"))
	f.Add([]byte("---\nname: \x00\n---"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		a, err := parseArticle("x", raw)
		if err == nil && a.Name == "" {
			t.Fatalf("accepted article without a name: %q", raw)
		}
	})
}

// TestLoadIgnoresSymlinks proves a Source cannot pull files from outside its root.
func TestLoadIgnoresSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	dir := filepath.Join(root, "global", "articles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ok := "---\nname: Ay\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte(ok), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("no frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(dir, "link.md")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	snap, err := NewDirSource(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(snap.Articles()); n != 1 {
		t.Fatalf("articles = %d, want 1", n)
	}
}
