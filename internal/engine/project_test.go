package engine_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

const (
	projA = "github.com/acme/shop"
	projB = "github.com/acme/other"
)

func projectContent() map[string]string {
	return map[string]string{
		"global/articles/db.md":                                   article("Database", "database", "sql"),
		"global/articles/deploy.md":                               article("Deploy", "deploy"),
		"projects/github.com/acme/shop/articles/db.md":            article("Shop database", "database"),
		"projects/github.com/acme/shop/articles/sub/payments.md":  article("Payments", "payments", "database"),
		"projects/github.com/acme/other/README.md":                "no articles, still registered",
		"projects/github.com/acme/shop/notes/ignored.md":          "not an article",
		"projects/github.com/acme/shop/articles/not-markdown.txt": "ignored",
	}
}

type resolved struct {
	id, scope string
}

func ids(hits []engine.Hit) []resolved {
	var out []resolved
	for _, h := range hits {
		out = append(out, resolved{h.ID, h.Scope})
	}
	return out
}

func TestProjectResolution(t *testing.T) {
	snap := load(t, projectContent())
	// project-over-global: db exists in both; the Project's wins and the global
	// one is dropped. Project hits sort before global ones on equal score.
	got := snap.SearchIn(projA, "database")
	want := []resolved{
		{"db", projA},           // name "Shop database" +2, keyword +4 = 6
		{"sub/payments", projA}, // keyword +4
	}
	if !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("SearchIn(project) = %+v, want %+v", ids(got), want)
	}
	for _, h := range got {
		if h.Kind != "article" {
			t.Errorf("kind = %q", h.Kind)
		}
	}

	// A Project's Items come before global ones; global still found.
	got = snap.SearchIn(projA, "deploy")
	if want := []resolved{{"deploy", "global"}}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("project without own match falls back to global: %+v", ids(got))
	}

	// Equal score: project first.
	snap2 := load(t, map[string]string{
		"global/articles/a.md":                        article("Same", "k"),
		"projects/github.com/acme/shop/articles/b.md": article("Same", "k"),
	})
	if want := []resolved{{"b", projA}, {"a", "global"}}; !reflect.DeepEqual(ids(snap2.SearchIn(projA, "k")), want) {
		t.Fatalf("tie order = %+v", ids(snap2.SearchIn(projA, "k")))
	}
}

func TestProjectLookup(t *testing.T) {
	snap := load(t, projectContent())
	tests := []struct {
		name      string
		project   string
		id        string
		wantName  string
		wantScope string
		found     bool
	}{
		{"project wins", projA, "db", "Shop database", projA, true},
		{"id and project are case-insensitive", "GitHub.com/ACME/Shop", "DB", "Shop database", projA, true},
		{"nested project id", projA, "sub/payments", "Payments", projA, true},
		{"falls back to global", projA, "deploy", "Deploy", "global", true},
		{"project with no own items", projB, "db", "Database", "global", true},
		{"unregistered project is global only", "github.com/acme/unknown", "db", "Database", "global", true},
		{"unregistered project cannot see project items", "github.com/acme/unknown", "sub/payments", "", "", false},
		{"no project is global only", "", "db", "Database", "global", true},
		{"other project's item is invisible", projB, "sub/payments", "", "", false},
		{"missing", projA, "missing", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, ok := snap.ArticleIn(tt.project, tt.id)
			if ok != tt.found || a.Name != tt.wantName || a.Scope != tt.wantScope {
				t.Fatalf("ArticleIn(%q, %q) = %+v, %v", tt.project, tt.id, a, ok)
			}
		})
	}
}

func TestProjectSearchCases(t *testing.T) {
	snap := load(t, projectContent())
	tests := []struct {
		name    string
		project string
		want    []resolved
	}{
		{"project with no own items sees global only", projB, []resolved{{"db", "global"}}},
		{"unregistered project is global only", "github.com/acme/unknown", []resolved{{"db", "global"}}},
		{"no project is global only", "", []resolved{{"db", "global"}}},
		// The shop's db does not match "sql" but still hides the global db
		// that does: a Project article shadows by ID, not by score.
		{"project item shadows a matching global one", projA, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ids(snap.SearchIn(tt.project, "sql")); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// The existing global-only methods ignore Projects entirely.
func TestGlobalAccessorsIgnoreProjects(t *testing.T) {
	snap := load(t, projectContent())
	if got := ids(snap.Search("database")); !reflect.DeepEqual(got, []resolved{{"db", "global"}}) {
		t.Fatalf("Search = %+v", got)
	}
	if a, ok := snap.Article("db"); !ok || a.Scope != "global" {
		t.Fatalf("Article = %+v, %v", a, ok)
	}
	if _, ok := snap.Article("sub/payments"); ok {
		t.Fatal("global lookup found a project article")
	}
	if n := len(snap.Articles()); n != 2 {
		t.Fatalf("Articles = %d, want the 2 global ones", n)
	}
}

func TestProjects(t *testing.T) {
	snap := load(t, projectContent())
	if got, want := snap.Projects(), []string{projB, projA}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Projects = %v, want %v", got, want)
	}
	if got := load(t, map[string]string{"global/articles/a.md": article("A")}).Projects(); len(got) != 0 {
		t.Fatalf("Projects = %v, want none", got)
	}
}

func TestProjectIsRegisteredIgnoringCase(t *testing.T) {
	snap := load(t, projectContent())
	for id, want := range map[string]string{projA: projA, "GITHUB.COM/ACME/SHOP": projA, "github.com/Acme/Other": projB} {
		if scope, ok := snap.Project(id); !ok || scope != want {
			t.Errorf("Project(%q) = %q, %v, want %q", id, scope, ok, want)
		}
	}
	for _, id := range []string{"", "github.com/acme/none", "github.com/acme"} {
		if scope, ok := snap.Project(id); ok || scope != "" {
			t.Errorf("Project(%q) = %q, %v, want not registered", id, scope, ok)
		}
	}
}

func TestProjectValidationErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"uppercase host", map[string]string{"projects/GitHub.com/acme/shop/articles/a.md": article("A")},
			"projects/GitHub.com: invalid Project host"},
		{"file where a host belongs", map[string]string{"projects/readme.md": "x"},
			"projects/readme.md: not a directory"},
		{"file where an org belongs", map[string]string{"projects/github.com/readme.md": "x"},
			"projects/github.com/readme.md: not a directory"},
		{"file where a repo belongs", map[string]string{"projects/github.com/acme/readme.md": "x"},
			"projects/github.com/acme/readme.md: not a directory"},
		{"bad org characters", map[string]string{"projects/github.com/ac me/shop/articles/a.md": article("A")},
			"projects/github.com/ac me: invalid Project org"},
		{"hidden repo", map[string]string{"projects/github.com/acme/.git/articles/a.md": article("A")},
			"projects/github.com/acme/.git: invalid Project repo"},
		{"projects differ only in case", map[string]string{
			"projects/github.com/Acme/shop/articles/a.md": article("A"),
			"projects/github.com/acme/shop/articles/b.md": article("B"),
		}, "projects/github.com/Acme/shop and projects/github.com/acme/shop: Projects differ only in case"},
		{"invalid article names its path", map[string]string{"projects/github.com/acme/shop/articles/sub/a.md": "no frontmatter"},
			"projects/github.com/acme/shop/articles/sub/a.md: missing frontmatter"},
		{"ids differing in case within a project", map[string]string{
			"projects/github.com/acme/shop/articles/A.md": article("A"),
			"projects/github.com/acme/shop/articles/a.md": article("B"),
		}, "projects/github.com/acme/shop/articles/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeFiles(t, tt.files)
			_, err := engine.NewDirSource(root).Load()
			if err == nil {
				t.Fatal("Load succeeded")
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

func TestSameIDInDifferentScopesIsNotADuplicate(t *testing.T) {
	load(t, map[string]string{
		"global/articles/a.md":                        article("A"),
		"projects/github.com/acme/shop/articles/A.md": article("A"),
	})
}

// A symlink inside projects/ is never followed: not as a host, org or repo
// directory, and not as an articles directory. Load rejects it.
func TestProjectSymlinksAreNotFollowed(t *testing.T) {
	outside := writeFiles(t, map[string]string{"stolen.md": article("Stolen", "secret")})
	for name, link := range map[string]string{
		"host":     "projects/github.com",
		"repo":     "projects/github.com/acme/shop",
		"articles": "projects/github.com/acme/shop/articles",
	} {
		t.Run(name, func(t *testing.T) {
			root := writeFiles(t, map[string]string{"projects/github.com/acme/other/README.md": "x"})
			if name == "host" {
				_ = os.RemoveAll(filepath.Join(root, "projects"))
				mustMkdir(t, filepath.Join(root, "projects"))
			}
			if name == "repo" {
				mustMkdir(t, filepath.Join(root, "projects/github.com/acme"))
			}
			if name == "articles" {
				mustMkdir(t, filepath.Join(root, "projects/github.com/acme/shop"))
			}
			if err := os.Symlink(outside, filepath.Join(root, link)); err != nil {
				t.Skip("symlinks unavailable")
			}
			snap, err := engine.NewDirSource(root).Load()
			if err == nil {
				t.Fatalf("a symlinked Project path was accepted: %v", snap.Projects())
			}
		})
	}
}

// A Project articles directory that is a symlink is rejected even when it
// points inside the root, so one directory cannot pose as another scope.
func TestProjectArticlesSymlinkInsideRootIsRejected(t *testing.T) {
	root := writeFiles(t, map[string]string{"global/articles/g.md": article("G", "g")})
	mustMkdir(t, filepath.Join(root, "projects/github.com/acme/shop"))
	if err := os.Symlink("../../../../global/articles", filepath.Join(root, "projects/github.com/acme/shop/articles")); err != nil {
		t.Skip("symlinks unavailable")
	}
	_, err := engine.NewDirSource(root).Load()
	if err == nil {
		t.Fatal("a symlinked Project articles directory was accepted")
	}
	if want := "projects/github.com/acme/shop/articles: "; !strings.HasPrefix(err.Error(), want) || strings.Contains(err.Error(), root) {
		t.Fatalf("error = %q, want prefix %q and no host path", err, want)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
