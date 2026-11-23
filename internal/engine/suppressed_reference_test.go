package engine_test

import (
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #367: a lookup line (`goldie lookup <id>`, #358) and a replaced_by
// (ADR 0021) resolve in their article's own view, and for a Project article
// that view is the one the Project's readers get: suppressions included
// (#360). A reference from a Project article to a global Item that Project
// suppresses fails the load, naming the referring article and the
// project.yaml. Suppression is per Project: another Project's article, and a
// global article, still see the Item.

// suppressedRefFiles is a global Item the shop Project suppresses, the
// suppressing project.yaml, and the referring file at name.
func suppressedRefFiles(suppress, name, content string) map[string]string {
	return map[string]string{
		globalFile("platform/deploy"): article("Deploy"),
		globalFile("kept"):            article("Kept"),
		supFile:                       suppressYAML(suppress),
		name:                          content,
	}
}

// --- A reference to a suppressed Item fails that Project's load ---

func TestLookupOfAnItemTheProjectSuppressesFailsTheLoad(t *testing.T) {
	for name, tc := range map[string]struct{ suppress, body string }{
		"fenced":            {"article:platform/deploy", fence("goldie lookup platform/deploy")},
		"tilde fence":       {"article:platform/deploy", "~~~\ngoldie lookup platform/deploy\n~~~\n"},
		"inline":            {"article:platform/deploy", "Read `goldie lookup platform/deploy` first."},
		"in a list item":    {"article:platform/deploy", "- step\n\n  ```\n  goldie lookup platform/deploy\n  ```\n"},
		"lookup case":       {"article:platform/deploy", fence("goldie lookup PLATFORM/Deploy")},
		"suppress case":     {"article:Platform/DEPLOY", fence("goldie lookup platform/deploy")},
		"both cases differ": {"article:PLATFORM/deploy", "Read `goldie lookup platform/DEPLOY` first."},
	} {
		t.Run(name, func(t *testing.T) {
			loadErr(t, suppressedRefFiles(tc.suppress, projectFile("guide"), docWith("Guide", tc.body)),
				projectFile("guide"), supFile)
		})
	}
}

func TestReplacedByAnItemTheProjectSuppressesFailsTheLoad(t *testing.T) {
	for name, tc := range map[string]struct{ suppress, ref string }{
		"same case":     {"article:platform/deploy", "article:platform/deploy"},
		"ref case":      {"article:platform/deploy", "article:Platform/Deploy"},
		"suppress case": {"article:PLATFORM/DEPLOY", "article:platform/deploy"},
	} {
		t.Run(name, func(t *testing.T) {
			files := suppressedRefFiles(tc.suppress, projectFile("old"),
				govArticle("Old", "status: deprecated", "replaced_by: "+tc.ref))
			loadErr(t, files, projectFile("old"), supFile)
		})
	}
}

// A nested id is matched as a whole, and its case is ignored per segment.
func TestReferenceToASuppressedNestedItemFailsTheLoad(t *testing.T) {
	base := map[string]string{
		globalFile("team/platform/deploy/v2"): article("Deploy v2"),
		globalFile("team/platform/deploy"):    article("Deploy"),
		supFile:                               suppressYAML("article:Team/Platform/Deploy/V2"),
	}
	with := func(name, content string) map[string]string {
		files := map[string]string{name: content}
		for k, v := range base {
			files[k] = v
		}
		return files
	}
	loadErr(t, with(projectFile("a/b/guide"), docWith("Guide", fence("goldie lookup team/platform/deploy/v2"))),
		projectFile("a/b/guide"), supFile)
	loadErr(t, with(projectFile("a/b/old"), govArticle("Old", "replaced_by: article:team/PLATFORM/deploy/v2")),
		projectFile("a/b/old"), supFile)
	// The parent id is a different Item, and it is not suppressed.
	mustLoad(t, with(projectFile("a/b/guide"), docWith("Guide", fence("goldie lookup team/platform/deploy"))))
	mustLoad(t, with(projectFile("a/b/old"), govArticle("Old", "replaced_by: article:team/platform/deploy")))
}

// A draft Project copy is left out of Resolution (#353), so it neither shadows
// the global Item nor stops its suppression (#360): the reference resolves to
// nothing the Project can see, and the suppression is why.
func TestReferenceToASuppressedItemWithADraftProjectCopyFailsTheLoad(t *testing.T) {
	files := suppressedRefFiles("article:platform/deploy", projectFile("guide"),
		docWith("Guide", fence("goldie lookup platform/deploy")))
	files[projectFile("platform/deploy")] = govArticle("Draft deploy", "status: draft")
	loadErr(t, files, projectFile("guide"), supFile)

	files = suppressedRefFiles("article:platform/deploy", projectFile("old"),
		govArticle("Old", "replaced_by: article:platform/deploy"))
	files[projectFile("platform/deploy")] = govArticle("Draft deploy", "status: draft")
	loadErr(t, files, projectFile("old"), supFile)
}

// The error names the files by their paths relative to the content root, and
// does not echo the article's content.
func TestSuppressedReferenceErrorDoesNotEchoContent(t *testing.T) {
	msg := loadErr(t, suppressedRefFiles("article:platform/deploy", projectFile("guide"),
		docWith("Secret guide title", fence("goldie lookup platform/deploy"), "keywords: [hush]")),
		projectFile("guide"), supFile)
	for _, s := range []string{"Secret guide title", "hush", "Body of Deploy", "Intro."} {
		if strings.Contains(msg, s) {
			t.Errorf("error %q echoes %q from the content", msg, s)
		}
	}
	assertNoFormatRunes(t, msg)
}

// --- Suppression is per Project; everything else still loads ---

func TestAnotherProjectsReferenceToTheSuppressedItemLoads(t *testing.T) {
	files := suppressedRefFiles("article:platform/deploy", otherProjectFile("guide"),
		docWith("Guide", fence("goldie lookup platform/deploy")+"\nAlso `goldie lookup Platform/Deploy`."))
	files[otherProjectFile("old")] = govArticle("Old", "replaced_by: article:PLATFORM/deploy")
	files[projectFile("own")] = article("Own")
	snap := load(t, files)
	if _, ok := snap.ArticleIn(otherGovProject, "platform/deploy"); !ok {
		t.Error("the other Project lost an Item only the shop Project suppresses")
	}
	if _, ok := snap.ArticleIn(govProject, "platform/deploy"); ok {
		t.Error("the shop Project still sees the Item it suppresses")
	}
}

// A global article sees the global view, which no Project's suppression
// changes.
func TestGlobalReferenceToASuppressedItemLoads(t *testing.T) {
	files := suppressedRefFiles("article:platform/deploy", globalFile("guide"),
		docWith("Guide", fence("goldie lookup platform/deploy")+"\nAlso `goldie lookup PLATFORM/deploy`."))
	files[globalFile("old")] = govArticle("Old", "status: deprecated", "replaced_by: article:Platform/Deploy")
	files[otherSupFile] = suppressYAML("article:platform/deploy")
	snap := load(t, files)
	if _, ok := snap.Article("platform/deploy"); !ok {
		t.Error("the global view lost a suppressed Item")
	}
}

func TestProjectReferenceToAnItemItDoesNotSuppressLoads(t *testing.T) {
	files := suppressedRefFiles("article:platform/deploy", projectFile("guide"),
		docWith("Guide", fence("goldie lookup kept")+"\nAlso `goldie lookup KEPT`."))
	files[projectFile("old")] = govArticle("Old", "replaced_by: article:kept")
	// A Project article that shares an id with the suppressed Item's prefix,
	// and the Project's own Item, both resolve.
	files[projectFile("platform/own")] = article("Own")
	files[projectFile("next")] = docWith("Next", fence("goldie lookup platform/own"))
	mustLoad(t, files)
}

// A dangling suppression is a warning (#360); it must not turn an existing,
// unsuppressed reference into a failure.
func TestDanglingSuppressionDoesNotFailAnExistingReference(t *testing.T) {
	root := writeFiles(t, map[string]string{
		globalFile("kept"):  article("Kept"),
		globalFile("style"): article("Style"),
		supFile:             suppressYAML("article:gone", "article:Style"),
		projectFile("guide"): docWith("Guide",
			fence("goldie lookup kept", "goldie lookup KEPT")+"\nAlso `goldie lookup kept`."),
		projectFile("old"): govArticle("Old", "replaced_by: article:kept"),
	})
	s := engine.NewSyncer(engine.NewDirSource(root))
	if err := s.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v; a dangling suppression must not fail an existing reference", err)
	}
	assertOneWarning(t, syncWarnings(t, s.Status()), root, supFile, "article:gone")
}

// A load that fails on a suppressed reference keeps the last good Snapshot,
// like any other failed load.
func TestSuppressingAReferencedItemKeepsTheLastGoodSnapshot(t *testing.T) {
	root := writeFiles(t, map[string]string{
		globalFile("platform/deploy"): article("Deploy"),
		projectFile("guide"):          docWith("Guide", fence("goldie lookup platform/deploy")),
	})
	s := engine.NewSyncer(engine.NewDirSource(root))
	if err := s.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := s.Current()
	writeTo(t, root, supFile, suppressYAML("article:platform/deploy"))
	err := s.Poll(t.Context())
	if err == nil || s.Current() != first {
		t.Fatalf("suppressing a referenced Item: err %v, Snapshot kept %v", err, s.Current() == first)
	}
	for _, f := range []string{projectFile("guide"), supFile} {
		if !strings.Contains(s.Status().LastError, f) {
			t.Errorf("LastError %q does not name %s", s.Status().LastError, f)
		}
	}
	if strings.Contains(s.Status().LastError, root) {
		t.Errorf("LastError %q leaks the root path", s.Status().LastError)
	}
}

// --- The git Source shares the loader ---

func TestGitSourceFailsOnAReferenceToASuppressedItem(t *testing.T) {
	for name, tc := range map[string]struct{ file, content string }{
		"lookup":      {projectFile("guide"), docWith("Guide", fence("goldie lookup Platform/Deploy"))},
		"replaced_by": {projectFile("old"), govArticle("Old", "replaced_by: article:platform/DEPLOY")},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newGitRepo(t)
			repo.commit("main", suppressedRefFiles("article:platform/deploy", tc.file, tc.content))
			_, err := newGit(t, engine.GitOptions{URL: repo.url}).Load(t.Context())
			if err == nil {
				t.Fatal("Load succeeded; want a reference to a suppressed Item to fail it")
			}
			for _, f := range []string{tc.file, supFile} {
				if !strings.Contains(err.Error(), f) {
					t.Errorf("error %q does not name %s", err, f)
				}
			}
			if strings.Contains(err.Error(), repo.dir) {
				t.Errorf("error %q leaks the repository path", err)
			}
		})
	}
}
