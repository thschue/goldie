package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #269: Items carry lifecycle frontmatter (status, enforced, replaced_by)
// and the loader validates it. A reference is <kind>:<name>, resolved in the
// referrer's view: a project Item sees its Project, then global; a global Item
// sees global only. A load that breaks a rule fails, and the Syncer keeps the
// last good Snapshot.

const govProject = "github.com/acme/shop"

// govArticle is an article with the given extra frontmatter lines.
func govArticle(name string, front ...string) string {
	return "---\nname: " + name + "\n" + strings.Join(front, "\n") + "\n---\nBody of " + name + ".\n"
}

func globalFile(id string) string { return "global/articles/" + id + ".md" }

func projectFile(id string) string { return "projects/" + govProject + "/articles/" + id + ".md" }

// loadErr loads files and returns the error, failing the test when Load
// succeeds. The error must name each of wantFiles and must not carry the root
// path or a control character.
func loadErr(t *testing.T, files map[string]string, wantFiles ...string) string {
	t.Helper()
	root := writeFiles(t, files)
	snap, err := engine.NewDirSource(root).Load(t.Context())
	if err == nil {
		t.Fatalf("Load succeeded, want a validation error: %+v", snap.Articles())
	}
	msg := err.Error()
	if strings.Contains(msg, root) {
		t.Errorf("error leaks the root path: %q", msg)
	}
	for _, r := range msg {
		if unicode.IsControl(r) {
			t.Errorf("error carries the control character %U: %q", r, msg)
			break
		}
	}
	for _, f := range wantFiles {
		if !strings.Contains(msg, f) {
			t.Errorf("error %q does not name %s", msg, f)
		}
	}
	return msg
}

// lifecycle reads the exported fields Status, Enforced and ReplacedBy of a, by
// reflection, so the tests compile before the fields exist and fail on their
// absence instead.
func lifecycle(t *testing.T, a engine.Article) (status string, enforced bool, replacedBy string) {
	t.Helper()
	v := reflect.ValueOf(a)
	for _, name := range []string{"Status", "Enforced", "ReplacedBy"} {
		if !v.FieldByName(name).IsValid() {
			t.Fatalf("engine.Article has no field %s", name)
		}
	}
	return fmt.Sprint(v.FieldByName("Status").Interface()), v.FieldByName("Enforced").Bool(), fmt.Sprint(v.FieldByName("ReplacedBy").Interface())
}

func TestLifecycleDefaultsToPublishedNotEnforcedWithoutReplacement(t *testing.T) {
	snap := load(t, map[string]string{globalFile("plain"): article("Plain")})
	a, ok := snap.Article("plain")
	if !ok {
		t.Fatal("article not found")
	}
	status, enforced, replacedBy := lifecycle(t, a)
	if status != "published" || enforced || replacedBy != "" {
		t.Fatalf("Status %q, Enforced %v, ReplacedBy %q; want published, false, empty", status, enforced, replacedBy)
	}
}

func TestLifecycleFrontmatterIsParsed(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("draft"):      govArticle("Draft", "status: draft"),
		globalFile("published"):  govArticle("Published", "status: published", "enforced: true"),
		globalFile("not-forced"): govArticle("Not forced", "enforced: false"),
		globalFile("old"):        govArticle("Old", "status: deprecated", "enforced: true", "replaced_by: article:published"),
	})
	for id, want := range map[string]struct {
		status   string
		enforced bool
		replaced string
	}{
		"draft":      {"draft", false, ""},
		"published":  {"published", true, ""},
		"not-forced": {"published", false, ""},
		"old":        {"deprecated", true, "article:published"},
	} {
		a, ok := snap.Article(id, engine.ReadOptions{Drafts: true})
		if !ok {
			t.Fatalf("%s: not found", id)
		}
		status, enforced, replacedBy := lifecycle(t, a)
		if status != want.status || enforced != want.enforced || !strings.EqualFold(replacedBy, want.replaced) {
			t.Errorf("%s: Status %q, Enforced %v, ReplacedBy %q; want %q, %v, %q", id, status, enforced, replacedBy, want.status, want.enforced, want.replaced)
		}
	}
}

func TestLifecycleReachesTheProjectView(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("target"):  article("Target"),
		projectFile("shop-x"): govArticle("Shop X", "status: deprecated", "enforced: true", "replaced_by: article:target"),
	})
	a, ok := snap.ArticleIn(govProject, "shop-x")
	if !ok {
		t.Fatal("ArticleIn: not found")
	}
	status, enforced, replacedBy := lifecycle(t, a)
	if status != "deprecated" || !enforced || !strings.EqualFold(replacedBy, "article:target") {
		t.Fatalf("Status %q, Enforced %v, ReplacedBy %q", status, enforced, replacedBy)
	}
}

func TestLifecycleRejectsValuesOutsideTheVocabulary(t *testing.T) {
	tests := []struct{ name, line, field string }{
		{"status capitalised", "status: Draft", "status"},
		{"status upper case", "status: PUBLISHED", "status"},
		{"status unknown word", "status: archived", "status"},
		{"status empty string", `status: ""`, "status"},
		{"status a list", "status: [draft]", "status"},
		{"status a number", "status: 1", "status"},
		{"status with a control character", `status: "dra\nft"`, "status"},
		{"enforced yes", "enforced: yes", "enforced"},
		{"enforced on", "enforced: on", "enforced"},
		{"enforced quoted true", `enforced: "true"`, "enforced"},
		{"enforced a number", "enforced: 1", "enforced"},
		{"enforced a word", "enforced: maybe", "enforced"},
		{"enforced a list", "enforced: [true]", "enforced"},
		{"replaced_by a number", "replaced_by: 5", "replaced_by"},
		{"replaced_by a list", "replaced_by: [article:other]", "replaced_by"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{
				globalFile("a"):     govArticle("A", tt.line),
				globalFile("other"): article("Other"),
			}, globalFile("a"))
			if !strings.Contains(msg, tt.field) {
				t.Errorf("error %q does not name the field %s", msg, tt.field)
			}
		})
	}
}

func TestLifecycleValuesAreValidatedInProjectArticlesToo(t *testing.T) {
	loadErr(t, map[string]string{projectFile("a"): govArticle("A", "status: Draft")}, projectFile("a"))
}

func TestReplacedByRejectsMalformedReferences(t *testing.T) {
	refs := map[string]string{
		"no kind":                  "other",
		"empty name":               `"article:"`,
		"empty kind":               ":other",
		"extra colon":              "article:a:b",
		"dot dot":                  "article:../secret",
		"dot dot inside":           "article:a/../other",
		"trailing dot dot":         "article:other/..",
		"leading slash":            "article:/other",
		"absolute path":            "article:/etc/passwd",
		"control character":        `"article:oth\u0001er"`,
		"newline":                  `"article:other\nstatus: published"`,
		"tab":                      `"article:oth\ter"`,
		"delete character":         `"article:oth\u007fer"`,
		"line separator":           `"article:oth er"`,
		"kind with a control char": `"art\u0001icle:other"`,
	}
	for name, ref := range refs {
		t.Run(name, func(t *testing.T) {
			loadErr(t, map[string]string{
				globalFile("a"):     govArticle("A", "replaced_by: "+ref),
				globalFile("other"): article("Other"),
				globalFile("etc"):   article("Etc"),
			}, globalFile("a"))
		})
	}
}

func TestReplacedByThatDoesNotResolveFailsTheLoad(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		file  string
	}{
		{"dangling", map[string]string{globalFile("a"): govArticle("A", "replaced_by: article:missing")}, globalFile("a")},
		{"other Kind that does not exist", map[string]string{globalFile("a"): govArticle("A", "replaced_by: skill:other"), globalFile("other"): article("O")}, globalFile("a")},
		{"nested name that does not exist", map[string]string{globalFile("a"): govArticle("A", "replaced_by: article:team/missing")}, globalFile("a")},
		{"points at itself", map[string]string{globalFile("a"): govArticle("A", "replaced_by: article:a")}, globalFile("a")},
		{"points at itself ignoring case", map[string]string{globalFile("a"): govArticle("A", "replaced_by: article:A")}, globalFile("a")},
		{"points at itself in a nested path", map[string]string{globalFile("team/a"): govArticle("A", "replaced_by: article:team/a")}, globalFile("team/a")},
		{"two-step cycle", map[string]string{
			globalFile("a"): govArticle("A", "replaced_by: article:b"),
			globalFile("b"): govArticle("B", "replaced_by: article:a"),
		}, "global/articles/"},
		{"three-step cycle", map[string]string{
			globalFile("a"): govArticle("A", "replaced_by: article:b"),
			globalFile("b"): govArticle("B", "replaced_by: article:c"),
			globalFile("c"): govArticle("C", "replaced_by: article:a"),
		}, "global/articles/"},
		{"cycle behind a valid entry point", map[string]string{
			globalFile("entry"): govArticle("Entry", "replaced_by: article:b"),
			globalFile("b"):     govArticle("B", "replaced_by: article:c"),
			globalFile("c"):     govArticle("C", "replaced_by: article:b"),
		}, "global/articles/"},
		{"points at a draft", map[string]string{
			globalFile("a"): govArticle("A", "status: deprecated", "replaced_by: article:d"),
			globalFile("d"): govArticle("D", "status: draft"),
		}, globalFile("a")},
		{"project item points at a project draft", map[string]string{
			projectFile("a"): govArticle("A", "replaced_by: article:d"),
			projectFile("d"): govArticle("D", "status: draft"),
		}, projectFile("a")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { loadErr(t, tt.files, tt.file) })
	}
}

func TestReplacedByThatResolvesLoads(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"published target", map[string]string{
			globalFile("a"): govArticle("A", "status: deprecated", "replaced_by: article:b"),
			globalFile("b"): article("B"),
		}},
		{"target ignoring case", map[string]string{
			globalFile("a"):         govArticle("A", "status: deprecated", "replaced_by: article:TEAM/Next"),
			globalFile("team/next"): article("Next"),
		}},
		{"a chain without a cycle", map[string]string{
			globalFile("a"): govArticle("A", "status: deprecated", "replaced_by: article:b"),
			globalFile("b"): govArticle("B", "status: deprecated", "replaced_by: article:c"),
			globalFile("c"): article("C"),
		}},
		{"two Items replaced by one is not a cycle", map[string]string{
			globalFile("a"): govArticle("A", "status: deprecated", "replaced_by: article:c"),
			globalFile("b"): govArticle("B", "status: deprecated", "replaced_by: article:c"),
			globalFile("c"): article("C"),
		}},
		{"project item sees global", map[string]string{
			globalFile("g"):  article("G"),
			projectFile("p"): govArticle("P", "status: deprecated", "replaced_by: article:g"),
		}},
		{"project item sees its own Project", map[string]string{
			projectFile("p"): govArticle("P", "status: deprecated", "replaced_by: article:q"),
			projectFile("q"): article("Q"),
		}},
		{"project item sees the Project's published Item over a global draft", map[string]string{
			globalFile("shared"):  govArticle("Shared", "status: draft"),
			projectFile("shared"): article("Shared"),
			projectFile("p"):      govArticle("P", "status: deprecated", "replaced_by: article:shared"),
		}},
		{"global item sees the global Item while a Project has a draft of the same name", map[string]string{
			globalFile("shared"):  article("Shared"),
			projectFile("shared"): govArticle("Shared", "status: draft"),
			globalFile("g"):       govArticle("G", "status: deprecated", "replaced_by: article:shared"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { load(t, tt.files) })
	}
}

func TestReplacedByIsResolvedInTheReferrersView(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		file  string
	}{
		{"global item cannot see a project-only Item", map[string]string{
			globalFile("g"):             govArticle("G", "status: deprecated", "replaced_by: article:only-in-shop"),
			projectFile("only-in-shop"): article("Only"),
		}, globalFile("g")},
		{"project item cannot see another Project", map[string]string{
			projectFile("p"): govArticle("P", "status: deprecated", "replaced_by: article:theirs"),
			"projects/github.com/acme/other/articles/theirs.md": article("Theirs"),
		}, projectFile("p")},
		{"project item sees the Project's draft before a global published Item", map[string]string{
			globalFile("shared"):  article("Shared"),
			projectFile("shared"): govArticle("Shared", "status: draft"),
			projectFile("p"):      govArticle("P", "status: deprecated", "replaced_by: article:shared"),
		}, projectFile("p")},
		{"global item sees the global draft even if a Project has a published Item", map[string]string{
			globalFile("shared"):  govArticle("Shared", "status: draft"),
			projectFile("shared"): article("Shared"),
			globalFile("g"):       govArticle("G", "status: deprecated", "replaced_by: article:shared"),
		}, globalFile("g")},
		{"project item that shadows a global one and names itself", map[string]string{
			globalFile("dup"):  article("Dup"),
			projectFile("dup"): govArticle("Dup", "status: deprecated", "replaced_by: article:dup"),
		}, projectFile("dup")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { loadErr(t, tt.files, tt.file) })
	}
}

// The error quotes the reference, and reaches sync status and logs, so it must
// not carry a credential-looking value or a control character.
func TestReplacedByErrorDoesNotEchoSecretsOrControlCharacters(t *testing.T) {
	for name, tt := range map[string]struct{ ref, secret string }{
		"dangling token-like name": {"article:ghp_abcdefghij0123456789", "ghp_abcdefghij0123456789"},
		"dangling long run":        {"article:" + strings.Repeat("Ab3", 20), strings.Repeat("Ab3", 20)},
		"malformed with secret":    {`"article:token=sk-live-0123456789abcdef"`, "sk-live-0123456789abcdef"},
		"malformed with url":       {`"https://user:hunter2@example.test/x"`, "hunter2"},
		"control characters":       {`"article:a\u0007b\u001b[31m"`, "\u001b"},
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{globalFile("a"): govArticle("A", "replaced_by: "+tt.ref)}, globalFile("a"))
			if strings.Contains(msg, tt.secret) {
				t.Errorf("error %q echoes %q", msg, tt.secret)
			}
		})
	}
}

func TestSyncStatusDoesNotEchoTheReference(t *testing.T) {
	root := writeFiles(t, map[string]string{
		globalFile("a"): govArticle("A", "replaced_by: article:ghp_abcdefghij0123456789"),
	})
	s := engine.NewSyncer(engine.NewDirSource(root))
	if err := s.Poll(t.Context()); err == nil {
		t.Fatal("Poll succeeded")
	}
	last := s.Status().LastError
	if last == "" || strings.Contains(last, "ghp_abcdefghij0123456789") || strings.Contains(last, root) {
		t.Fatalf("LastError = %q", last)
	}
}

// Resolution is unchanged by the item key: IDs that differ only in case still
// shadow across Scopes and resolve whatever the spelling of the lookup. This
// passes today and pins the no-behaviour-change criterion.
func TestItemKeyComparesIgnoringCaseAcrossScopes(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("platform/deploy"):  article("Global deploy", "rollout"),
		projectFile("Platform/Deploy"): article("Shop deploy", "rollout"),
	})
	for _, id := range []string{"platform/deploy", "PLATFORM/DEPLOY", "Platform/Deploy"} {
		a, ok := snap.ArticleIn(strings.ToUpper(govProject), id)
		if !ok || a.Name != "Shop deploy" || a.Scope != govProject {
			t.Errorf("ArticleIn(%q) = %+v, %v; want the Project's article", id, a, ok)
		}
		if g, ok := snap.Article(id); !ok || g.Name != "Global deploy" {
			t.Errorf("Article(%q) = %+v, %v; want the global article", id, g, ok)
		}
	}
	hits := snap.SearchIn(govProject, "rollout")
	if len(hits) != 1 || hits[0].Scope != govProject {
		t.Fatalf("SearchIn = %+v; want only the Project's article, which shadows the global one", hits)
	}
}

// A failing load keeps the last good Snapshot. The Syncer polls a directory
// that changes between polls, so every rule is exercised through the real
// loader.
func TestFailingLoadKeepsTheLastGoodSnapshot(t *testing.T) {
	remove := func(id string) func(*testing.T, string) {
		return func(t *testing.T, root string) {
			t.Helper()
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(globalFile(id)))); err != nil {
				t.Fatal(err)
			}
		}
	}
	add := func(files map[string]string) func(*testing.T, string) {
		return func(t *testing.T, root string) {
			t.Helper()
			for id, content := range files {
				writeTo(t, root, globalFile(id), content)
			}
		}
	}
	breaks := map[string]func(*testing.T, string){
		"invalid status":       add(map[string]string{"bad": govArticle("Bad", "status: Draft")}),
		"invalid enforced":     add(map[string]string{"bad": govArticle("Bad", "enforced: yes")}),
		"malformed reference":  add(map[string]string{"bad": govArticle("Bad", "replaced_by: article:../x")}),
		"dangling reference":   add(map[string]string{"bad": govArticle("Bad", "replaced_by: article:missing")}),
		"self reference":       add(map[string]string{"bad": govArticle("Bad", "replaced_by: article:bad")}),
		"reference to a draft": add(map[string]string{"bad": govArticle("Bad", "replaced_by: article:wip"), "wip": govArticle("Wip", "status: draft")}),
		"reference cycle":      add(map[string]string{"bad": govArticle("Bad", "replaced_by: article:bad2"), "bad2": govArticle("Bad2", "replaced_by: article:bad")}),
		"target deleted":       remove("target"),
		"target made a draft":  add(map[string]string{"target": govArticle("Target", "status: draft")}),
	}
	for name, mutate := range breaks {
		t.Run(name, func(t *testing.T) {
			root := writeFiles(t, map[string]string{
				globalFile("good"):   article("Good", "kept"),
				globalFile("target"): article("Target"),
				globalFile("old"):    govArticle("Old", "status: deprecated", "replaced_by: article:target"),
			})
			s := engine.NewSyncer(engine.NewDirSource(root))
			if err := s.Poll(t.Context()); err != nil {
				t.Fatalf("first load: %v", err)
			}
			first := s.Current()
			if first == nil {
				t.Fatal("no Snapshot after a good load")
			}

			mutate(t, root)

			if err := s.Poll(t.Context()); err == nil {
				t.Fatal("Poll succeeded with a rule broken")
			}
			if s.Current() != first {
				t.Fatal("a failing load must keep the last good Snapshot")
			}
			if _, ok := s.Current().Article("good"); !ok {
				t.Error("the last good Snapshot lost its article")
			}
			if _, ok := s.Current().Article("bad"); ok {
				t.Error("the broken article reached the served Snapshot")
			}
			st := s.Status()
			if st.LastError == "" {
				t.Errorf("status = %+v; want the failure recorded", st)
			}
			if strings.Contains(st.LastError, root) {
				t.Errorf("LastError leaks the root path: %q", st.LastError)
			}
		})
	}
}

func TestSnapshotRecoversOnceTheContentIsFixed(t *testing.T) {
	root := writeFiles(t, map[string]string{globalFile("good"): article("Good")})
	s := engine.NewSyncer(engine.NewDirSource(root))
	if err := s.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := s.Current()

	writeTo(t, root, globalFile("bad"), govArticle("Bad", "status: Draft"))
	if err := s.Poll(t.Context()); err == nil || s.Current() != first {
		t.Fatalf("broken content: err %v, Snapshot kept %v", err, s.Current() == first)
	}

	writeTo(t, root, globalFile("bad"), govArticle("Bad", "status: draft"))
	if err := s.Poll(t.Context()); err != nil {
		t.Fatalf("fixed content: %v", err)
	}
	if s.Current() == first {
		t.Fatal("a good load must swap the Snapshot")
	}
	a, ok := s.Current().Article("bad", engine.ReadOptions{Drafts: true})
	if !ok {
		t.Fatal("the fixed article is not served")
	}
	if status, _, _ := lifecycle(t, a); status != "draft" {
		t.Fatalf("Status = %q, want draft", status)
	}
	if s.Status().LastError != "" {
		t.Fatalf("LastError = %q after a good load", s.Status().LastError)
	}
}

func writeTo(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
