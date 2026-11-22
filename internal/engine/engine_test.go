package engine_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func article(name string, keywords ...string) string {
	return "---\nname: " + name + "\nkeywords: [" + strings.Join(keywords, ", ") + "]\n---\nBody of " + name + ".\n"
}

func load(t *testing.T, files map[string]string) *engine.Snapshot {
	t.Helper()
	snap, err := engine.NewDirSource(writeFiles(t, files)).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return snap
}

func TestLoadExampleKnowledge(t *testing.T) {
	snap, err := engine.NewDirSource("../../examples/knowledge").Load()
	if err != nil {
		t.Fatal(err)
	}
	a, ok := snap.Article("agent-loops")
	if !ok || a.Name != "Autonomous agent loops" || !strings.HasPrefix(a.Content, "Three cooperating loops") {
		t.Fatalf("agent-loops = %+v, %v", a, ok)
	}
	hits := snap.Search("labels")
	if len(hits) != 1 || hits[0].ID != "agent-loops" || hits[0].Score != 4 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestLoad(t *testing.T) {
	snap := load(t, map[string]string{
		"global/articles/b.md":          "---\r\nname: \"Bee\"\r\nkeywords: [ Honey , '' , WAX ]\r\n---\r\n\r\ntext\r\n",
		"global/articles/sub/a.md":      article("Ay", "x"),
		"global/articles/notes.txt":     "ignored",
		"global/skills/s/SKILL.md":      "not an article",
		"project/articles/ignored.md":   "no frontmatter",
		"global/articles/empty-body.md": "---\nname: Empty\n---\n",
	})
	var ids []string
	for _, a := range snap.Articles() {
		ids = append(ids, a.ID)
	}
	if want := []string{"b", "empty-body", "sub/a"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	b, _ := snap.Article("B")
	if b.Name != "Bee" || b.Content != "text\n" || !reflect.DeepEqual(b.Keywords, []string{"honey", "wax"}) {
		t.Fatalf("b = %+v", b)
	}
	if _, ok := snap.Article("missing"); ok {
		t.Fatal("found a missing article")
	}
}

func TestLoadMissingArticlesDirIsEmpty(t *testing.T) {
	snap := load(t, map[string]string{"README.md": "x"})
	if n := len(snap.Articles()); n != 0 {
		t.Fatalf("articles = %d", n)
	}
}

func TestSnapshotIsImmutable(t *testing.T) {
	snap := load(t, map[string]string{"global/articles/a.md": article("Ay", "k")})
	snap.Articles()[0].Keywords[0] = "changed"
	got, _ := snap.Article("a")
	got.Keywords[0] = "changed"
	got.Name = "changed"
	again, _ := snap.Article("a")
	if again.Name != "Ay" || again.Keywords[0] != "k" {
		t.Fatalf("snapshot mutated: %+v", again)
	}
	hits := snap.Search("k")
	hits[0].Keywords[0] = "changed"
	if snap.Search("k")[0].Keywords[0] != "k" {
		t.Fatal("snapshot mutated through a hit")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no frontmatter", map[string]string{"global/articles/a.md": "just text\n"}, "global/articles/a.md: missing frontmatter"},
		{"unclosed", map[string]string{"global/articles/a.md": "---\nname: A\n"}, "global/articles/a.md: frontmatter not closed"},
		{"missing name", map[string]string{"global/articles/sub/a.md": "---\nkeywords: [x]\n---\n"}, "global/articles/sub/a.md: frontmatter has no name"},
		{"blank name", map[string]string{"global/articles/a.md": "---\nname: \"  \"\n---\n"}, "global/articles/a.md: frontmatter has no name"},
		{"empty frontmatter", map[string]string{"global/articles/a.md": "---\n---\nbody"}, "global/articles/a.md: frontmatter has no name"},
		{"too large", map[string]string{"global/articles/a.md": "---\nname: A\n---\n" + strings.Repeat("x", 1<<20)}, "global/articles/a.md: article is larger than 1048576 bytes"},
		{"bad yaml", map[string]string{"global/articles/a.md": "---\nname: [unclosed\n---\n"}, "global/articles/a.md: malformed frontmatter"},
		{"keywords not a list", map[string]string{"global/articles/a.md": "---\nname: A\nkeywords:\n  a: b\n---\n"}, "global/articles/a.md: malformed frontmatter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeFiles(t, tt.files)
			snap, err := engine.NewDirSource(root).Load()
			if err == nil {
				t.Fatalf("Load succeeded: %+v", snap)
			}
			if !strings.HasPrefix(err.Error(), tt.want) {
				t.Fatalf("error = %q, want prefix %q", err, tt.want)
			}
			if strings.Contains(err.Error(), root) {
				t.Fatalf("error leaks the root path: %q", err)
			}
		})
	}
}

func TestLoadBadRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nope")
	_, err := engine.NewDirSource(root).Load()
	if err == nil || strings.Contains(err.Error(), root) {
		t.Fatalf("error = %v", err)
	}
}

func TestSearch(t *testing.T) {
	snap := load(t, map[string]string{
		"global/articles/storage.md":  article("Object storage", "bucket", "files"),
		"global/articles/buckets.md":  article("Buckets", "buckets", "bucket", "s3"),
		"global/articles/postgres.md": article("Postgres", "database", "sql"),
		"global/articles/a-db.md":     article("Database basics", "database"),
	})
	type hit struct {
		id      string
		score   int
		matched []string
	}
	tests := []struct {
		name  string
		query string
		want  []hit
	}{
		{"exact keyword +4", "sql", []hit{{"postgres", 4, []string{"sql"}}}},
		{"substring of keyword +1", "buck", []hit{
			{"buckets", 4, []string{"buckets", "bucket", "name"}},
			{"storage", 1, []string{"bucket"}},
		}},
		{"name word +2", "postgres", []hit{{"postgres", 2, []string{"name"}}}},
		{"exact beats name", "object", []hit{{"storage", 2, []string{"name"}}}},
		{"exact and name add up", "database", []hit{
			{"a-db", 6, []string{"database", "name"}},
			{"postgres", 4, []string{"database"}},
		}},
		{"words add up", "bucket files", []hit{
			{"storage", 8, []string{"bucket", "files"}},
			{"buckets", 7, []string{"buckets", "bucket", "name"}},
		}},
		{"ties sort by id", "data", []hit{
			{"a-db", 3, []string{"database", "name"}},
			{"postgres", 1, []string{"database"}},
		}},
		{"case insensitive", "  POSTGRES ", []hit{{"postgres", 2, []string{"name"}}}},
		{"no match", "kubernetes", nil},
		{"empty query", "   ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []hit
			for _, h := range snap.Search(tt.query) {
				got = append(got, hit{h.ID, h.Score, h.Matched})
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Search(%q) = %+v, want %+v", tt.query, got, tt.want)
			}
		})
	}
}
