package engine

import (
	"os"
	"path/filepath"
	"strings"
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

// TestLoadRejectsSymlinkedArticlesDir proves a symlinked global or
// global/articles directory cannot pull files from outside the root.
func TestLoadRejectsSymlinkedArticlesDir(t *testing.T) {
	for _, link := range []string{"global", "global/articles"} {
		t.Run(link, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			if err := os.MkdirAll(filepath.Join(outside, "articles"), 0o755); err != nil {
				t.Fatal(err)
			}
			md := []byte("---\nname: Outside\n---\n")
			for _, p := range []string{"o.md", filepath.Join("articles", "o.md")} {
				if err := os.WriteFile(filepath.Join(outside, p), md, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, link)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, link)); err != nil {
				t.Skip("symlinks unavailable")
			}
			snap, err := NewDirSource(root).Load()
			if err == nil {
				t.Fatalf("loaded %d articles through a symlink out of the root", len(snap.Articles()))
			}
			if strings.Contains(err.Error(), outside) || strings.Contains(err.Error(), root) {
				t.Fatalf("error leaks a host path: %v", err)
			}
		})
	}
}
