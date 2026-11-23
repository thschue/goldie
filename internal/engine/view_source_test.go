package engine_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #391: NewViewSource(fetch) is the Source kind behind an upstream. It
// is handed a View, the winning Items of one Project's view as an upstream
// sent them (engine.View: Items []Article with ID, Scope, Name, Keywords,
// Content, Status, Enforced and ReplacedBy set, and Suppressions []string),
// and a revision. It validates the View and builds a Snapshot, and does no
// network I/O: fetch is its only input.

const viewProject = "github.com/acme/shop"

func viewSourceItem(id, scope, name string) engine.Article {
	return engine.Article{ID: id, Scope: scope, Name: name, Keywords: []string{id}, Content: "Body of " + name + ".\n", Status: engine.StatusPublished}
}

func viewSource(v engine.View, rev string, err error) engine.Source {
	return engine.NewViewSource(func(context.Context) (engine.View, string, error) { return v, rev, err })
}

func goodView() engine.View {
	rules := viewSourceItem("rules", engine.ScopeGlobal, "Rules")
	rules.Enforced = true
	deploy := viewSourceItem("deploy", viewProject, "Shop deploy")
	return engine.View{Items: []engine.Article{
		viewSourceItem("deploy", engine.ScopeGlobal, "Global deploy"), deploy, rules,
		viewSourceItem("runbook", viewProject, "Runbook"),
	}}
}

// A valid View loads into a Snapshot where the Project's Items shadow the
// global ones, and the revision is the one fetch returned.
func TestViewSourceBuildsASnapshotFromAView(t *testing.T) {
	src := viewSource(goodView(), "rev-1", nil)
	snap, err := src.Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if a, ok := snap.ArticleIn(viewProject, "deploy"); !ok || a.Scope != viewProject || a.Name != "Shop deploy" {
		t.Errorf("ArticleIn(shop, deploy) = %+v, %v; want the Project copy", a, ok)
	}
	if a, ok := snap.ArticleIn("", "deploy"); !ok || a.Scope != engine.ScopeGlobal {
		t.Errorf("ArticleIn(none, deploy) = %+v, %v; want the global copy", a, ok)
	}
	if a, ok := snap.ArticleIn(viewProject, "rules"); !ok || !a.Enforced {
		t.Errorf("ArticleIn(shop, rules) = %+v, %v; want the enforced Item", a, ok)
	}
	if r, ok := src.(engine.Revisioner); !ok || r.Revision() != "rev-1" {
		t.Errorf("Revision = %v, want rev-1 from fetch", src)
	}
}

// An upstream layered under .goldie/: a local Item that shadows an enforced
// Item of the View fails the load and names the Item.
func TestViewSourceUnderALocalScopeRefusesShadowingAnEnforcedItem(t *testing.T) {
	root := writeFiles(t, map[string]string{".goldie/articles/rules.md": article("Mine", "rules")})
	_, err := engine.NewLocalSource(viewSource(goodView(), "r", nil), filepath.Join(root, ".goldie")).Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), "article:rules") || !strings.Contains(err.Error(), ".goldie/articles/rules.md") {
		t.Fatalf("Load error = %v, want it to name article:rules and the local file", err)
	}

	// A local Item that shadows an Item that is not enforced is fine.
	root = writeFiles(t, map[string]string{".goldie/articles/runbook.md": article("Mine", "runbook")})
	snap, err := engine.NewLocalSource(viewSource(goodView(), "r", nil), filepath.Join(root, ".goldie")).Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if a, ok := snap.ArticleIn(viewProject, "runbook"); !ok || a.Scope != engine.ScopeLocal {
		t.Errorf("ArticleIn(shop, runbook) = %+v, %v; want the local copy", a, ok)
	}
}

// A fetch error fails the Load, as it is.
func TestViewSourceReturnsTheFetchError(t *testing.T) {
	want := errors.New("upstream down")
	if _, err := viewSource(engine.View{}, "", want).Load(t.Context()); !errors.Is(err, want) {
		t.Errorf("Load error = %v, want the fetch error", err)
	}
}

// A Load whose context has ended loads nothing and does not call fetch.
func TestViewSourceWithAnEndedContextDoesNotFetch(t *testing.T) {
	called := false
	src := engine.NewViewSource(func(context.Context) (engine.View, string, error) {
		called = true
		return goodView(), "r", nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if snap, err := src.Load(ctx); err == nil || snap != nil {
		t.Errorf("Load = %v, %v; want an error and no Snapshot", snap, err)
	}
	if called {
		t.Error("fetch was called with an ended context")
	}
}

// The View comes from the network, so it is validated like a file: a Scope
// that is not global or a Project id, a reference that does not resolve and a
// lookup that does not resolve each fail the Load.
func TestViewSourceRefusesAnInvalidView(t *testing.T) {
	deprecated := viewSourceItem("old", engine.ScopeGlobal, "Old")
	deprecated.Status, deprecated.ReplacedBy = engine.StatusDeprecated, "article:missing"
	lookup := viewSourceItem("guide", engine.ScopeGlobal, "Guide")
	lookup.Content = "Run `goldie lookup nothere` first.\n"
	for name, item := range map[string]engine.Article{
		"scope local (an upstream cannot make local Items)": viewSourceItem("x", engine.ScopeLocal, "X"),
		"scope that is a path":                              viewSourceItem("x", "../../etc/passwd", "X"),
		"replaced_by that does not resolve":                 deprecated,
		"lookup that does not resolve":                      lookup,
	} {
		t.Run(name, func(t *testing.T) {
			v := goodView()
			v.Items = append(v.Items, item)
			if snap, err := viewSource(v, "r", nil).Load(t.Context()); err == nil || snap != nil {
				t.Errorf("Load = %v, %v; want an error and no Snapshot", snap, err)
			}
		})
	}
}
