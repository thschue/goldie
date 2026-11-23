package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #272: projects/<host>/<org>/<repo>/project.yaml may carry
// suppress: [<kind>:<name>], which hides those non-enforced global Items from
// that Project's search and lookup (CONTEXT: Suppression). A reference is
// parsed as in ADR 0021 and compared ignoring case (ADR 0015). Suppressing an
// enforced Item, the Project's own Item, or writing a malformed reference fails
// the load; a reference that names nothing is a warning in sync status. The
// file is untrusted: it is decoded strictly, read with a size cap, never
// followed through a symlink, and its list is capped.

// supFile is the shop Project's project.yaml; otherSupFile the other Project's.
const (
	supFile      = "projects/" + govProject + "/project.yaml"
	otherSupFile = "projects/" + otherGovProject + "/project.yaml"
)

// maxSuppressionsInTest is the cap on suppress entries per project.yaml that
// the implementation names maxSuppressions.
const maxSuppressionsInTest = 1000

// suppressYAML is a project.yaml in the flow form the issue writes.
func suppressYAML(refs ...string) string {
	return "suppress: [" + strings.Join(refs, ", ") + "]\n"
}

// suppressionsOf calls Snapshot.Suppressions(project) []string through an
// interface, so the file compiles before the method exists and each test fails
// on its absence instead.
func suppressionsOf(t *testing.T, snap *engine.Snapshot, proj string) []string {
	t.Helper()
	s, ok := any(snap).(interface{ Suppressions(project string) []string })
	if !ok {
		t.Fatal("engine.Snapshot has no method Suppressions(project string) []string")
	}
	return s.Suppressions(proj)
}

// syncWarnings reads SyncStatus.Warnings ([]string) by reflection, for the same
// reason.
func syncWarnings(t *testing.T, st engine.SyncStatus) []string {
	t.Helper()
	f := reflect.ValueOf(st).FieldByName("Warnings")
	if !f.IsValid() || f.Type() != reflect.TypeFor[[]string]() {
		t.Fatal("engine.SyncStatus has no field Warnings []string")
	}
	return f.Interface().([]string)
}

func suppressionFiles() map[string]string {
	return map[string]string{
		globalFile("platform/deploy"): article("Deploy", "rollout"),
		globalFile("style"):           article("Style guide", "rollout"),
		globalFile("kept"):            article("Kept", "rollout"),
		supFile:                       suppressYAML("article:platform/deploy", "article:style"),
		otherProjectFile("x"):         article("X"),
	}
}

func hitIDList(hits []engine.Hit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.ID)
	}
	return out
}

// --- Criterion 1: suppressed Items are hidden from that Project only ---

func TestSuppressedGlobalItemsAreHiddenFromTheProjectsSearchAndLookup(t *testing.T) {
	snap := load(t, suppressionFiles())
	for _, proj := range []string{govProject, strings.ToUpper(govProject)} {
		for _, opts := range [][]engine.ReadOptions{nil, {withDrafts}} {
			if got := hitIDList(snap.SearchIn(proj, "rollout", opts...)); !slices.Equal(got, []string{"kept"}) {
				t.Errorf("SearchIn(%q, %v) = %v; want only kept", proj, opts, got)
			}
			for _, id := range []string{"platform/deploy", "Platform/Deploy", "style", "STYLE"} {
				if a, ok := snap.ArticleIn(proj, id, opts...); ok {
					t.Errorf("ArticleIn(%q, %q, %v) = %+v; want the suppressed Item hidden", proj, id, opts, a)
				}
			}
			if _, ok := snap.ArticleIn(proj, "kept", opts...); !ok {
				t.Errorf("ArticleIn(%q, kept, %v) not found; only suppressed Items are hidden", proj, opts)
			}
		}
	}
}

func TestSuppressionLeavesOtherProjectsAndTheGlobalViewAlone(t *testing.T) {
	snap := load(t, suppressionFiles())
	want := []string{"kept", "platform/deploy", "style"}
	for _, proj := range []string{"", otherGovProject, "github.com/acme/unregistered"} {
		got := hitIDList(snap.SearchIn(proj, "rollout"))
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("SearchIn(%q) = %v; want %v", proj, got, want)
		}
		for _, id := range want {
			if _, ok := snap.ArticleIn(proj, id); !ok {
				t.Errorf("ArticleIn(%q, %q) not found; suppression applies to its own Project only", proj, id)
			}
		}
	}
	if got := hitIDList(snap.Search("rollout")); len(got) != 3 {
		t.Errorf("Search = %v; want all three global Items", got)
	}
	if _, ok := snap.Article("style"); !ok {
		t.Error("Article(style) not found in the global view")
	}
}

func TestSuppressionsListsTheProjectsSuppressedItems(t *testing.T) {
	snap := load(t, suppressionFiles())
	want := []string{"article:platform/deploy", "article:style"}
	for _, proj := range []string{govProject, strings.ToUpper(govProject)} {
		if got := suppressionsOf(t, snap, proj); !slices.Equal(got, want) {
			t.Errorf("Suppressions(%q) = %q; want %q", proj, got, want)
		}
	}
	for _, proj := range []string{otherGovProject, "", "github.com/acme/unregistered"} {
		if got := suppressionsOf(t, snap, proj); len(got) != 0 {
			t.Errorf("Suppressions(%q) = %q; want none", proj, got)
		}
	}
	// The Snapshot is immutable: changing the returned slice changes nothing.
	got := suppressionsOf(t, snap, govProject)
	if len(got) == 0 {
		t.Fatal("Suppressions is empty")
	}
	got[0] = "article:kept"
	if again := suppressionsOf(t, snap, govProject); !slices.Equal(again, want) {
		t.Errorf("Suppressions after changing a returned slice = %q; want %q", again, want)
	}
}

// A reference compares ignoring case (ADR 0015); Suppressions names each Item
// by its identity as loaded, the way Projects names a Project by its
// registered spelling.
func TestSuppressionMatchesTheIdentityIgnoringCase(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("platform/deploy"): article("Deploy", "rollout"),
		globalFile("Team/Policy"):     article("Policy", "rollout"),
		supFile:                       suppressYAML("article:Platform/DEPLOY", "article:team/policy"),
	})
	if got := hitIDList(snap.SearchIn(govProject, "rollout")); len(got) != 0 {
		t.Errorf("SearchIn = %v; want both Items suppressed", got)
	}
	if _, ok := snap.ArticleIn(govProject, "team/policy"); ok {
		t.Error("ArticleIn(team/policy) found; want it suppressed")
	}
	want := []string{"article:Team/Policy", "article:platform/deploy"}
	if got := suppressionsOf(t, snap, govProject); !slices.Equal(got, want) {
		t.Errorf("Suppressions = %q; want %q", got, want)
	}
}

func TestSuppressionHidesADeprecatedGlobalItem(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("old"): govArticle("Old", "status: deprecated"),
		supFile:           suppressYAML("article:old"),
	})
	if _, ok := snap.ArticleIn(govProject, "old"); ok {
		t.Error("the suppressed deprecated Item is served to the Project")
	}
	if got := suppressionsOf(t, snap, govProject); !slices.Equal(got, []string{"article:old"}) {
		t.Errorf("Suppressions = %q; want [article:old]", got)
	}
}

// A Project without a project.yaml, or with an empty list, suppresses nothing.
func TestProjectWithoutSuppressionsSeesEveryGlobalItem(t *testing.T) {
	for name, content := range map[string]string{
		"empty list":       "suppress: []\n",
		"leading marker":   "---\nsuppress: []\n",
		"comment only":     "# nothing suppressed\nsuppress: []\n",
		"no project.yaml":  "",
		"block style list": "suppress:\n  - article:other\n",
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{globalFile("style"): article("Style"), globalFile("other"): article("Other"), projectFile("own"): article("Own")}
			if content != "" {
				files[supFile] = content
			}
			snap := load(t, files)
			if _, ok := snap.ArticleIn(govProject, "style"); !ok {
				t.Error("ArticleIn(style) not found")
			}
		})
	}
}

// --- Criterion 2: enforced, own and malformed references fail the load ---

func TestSuppressingAnEnforcedItemFailsTheLoad(t *testing.T) {
	for name, tt := range map[string]struct {
		id, ref string
		front   []string
	}{
		"published by default": {"rules", "article:rules", []string{"enforced: true"}},
		"explicit published":   {"rules", "article:rules", []string{"enforced: true", "status: published"}},
		"deprecated":           {"rules", "article:rules", []string{"enforced: true", "status: deprecated"}},
		"other case":           {"rules", "article:RULES", []string{"enforced: true"}},
		"nested":               {"team/policy", "article:team/policy", []string{"enforced: true"}},
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{
				globalFile(tt.id): govArticle("Rules", tt.front...),
				globalFile("ok"):  article("Ok"),
				supFile:           suppressYAML("article:ok", tt.ref),
			}, supFile)
			if !strings.Contains(strings.ToLower(msg), tt.id) {
				t.Errorf("error %q does not name the enforced Item %s", msg, tt.id)
			}
		})
	}
}

// A draft does not enforce (checkEnforced, #353), so suppressing an enforced
// draft does not fail the load. Whether it is listed is not pinned here.
func TestSuppressingAnEnforcedDraftDoesNotFailTheLoad(t *testing.T) {
	mustLoad(t, map[string]string{
		globalFile("rules"): govArticle("Rules", "enforced: true", "status: draft"),
		supFile:             suppressYAML("article:rules"),
	})
}

func TestSuppressingTheProjectsOwnItemFailsTheLoad(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"project only": {projectFile("own"): article("Own"), supFile: suppressYAML("article:own")},
		"other case":   {projectFile("own"): article("Own"), supFile: suppressYAML("article:OWN")},
		// The reference resolves in the referrer's view, Project first (ADR
		// 0021), so it names the Project's own copy.
		"shadowing a global": {projectFile("style"): article("Own style"), globalFile("style"): article("Style"), supFile: suppressYAML("article:style")},
		"nested":             {projectFile("team/own"): article("Own"), supFile: suppressYAML("article:team/own")},
	} {
		t.Run(name, func(t *testing.T) { loadErr(t, files, supFile) })
	}
}

func TestSuppressionWithBadSyntaxFailsTheLoad(t *testing.T) {
	for name, content := range map[string]string{
		"no kind":             "suppress: [style]\n",
		"bare name with kind": "suppress: [\"article:\"]\n",
		"empty kind":          "suppress: [\":style\"]\n",
		"upper-case kind":     "suppress: [ARTICLE:style]\n",
		"unknown kind":        "suppress: [skill:style]\n",
		"dot-dot segment":     "suppress: [article:../style]\n",
		"dot segment":         "suppress: [article:./style]\n",
		"leading slash":       "suppress: [article:/style]\n",
		"trailing slash":      "suppress: [\"article:style/\"]\n",
		"empty segment":       "suppress: [\"article:team//style\"]\n",
		"backslash":           "suppress: [\"article:team\\\\style\"]\n",
		"second colon":        "suppress: [\"article:style:x\"]\n",
		"empty string":        "suppress: [\"\"]\n",
		"null entry":          "suppress: [~]\n",
		"number entry":        "suppress: [1]\n",
		"nested list":         "suppress: [[article:style]]\n",
		"mapping entry":       "suppress: [{article: style}]\n",
		"scalar not a list":   "suppress: article:style\n",
		"mapping not a list":  "suppress: {article: style}\n",
		"null list":           "suppress:\n",
	} {
		t.Run(name, func(t *testing.T) {
			loadErr(t, map[string]string{globalFile("style"): article("Style"), supFile: content}, supFile)
		})
	}
}

// A reference holds no Unicode space and no invisible character (ADR 0021): a
// name that reads like another must not hide a different Item. The target
// exists under the exact spelling, so only the reference rule can fail.
func TestSuppressionRejectsSpacesAndInvisibleCharacters(t *testing.T) {
	for name, tt := range map[string]struct{ yamlEsc, raw string }{
		"plain space":            {" ", " "},
		"non-breaking space":     {`\u00a0`, "\u00a0"},
		"zero-width space":       {`\u200b`, "\u200b"},
		"zero-width joiner":      {`\u200d`, "\u200d"},
		"byte order mark":        {`\ufeff`, "\ufeff"},
		"right-to-left override": {`\u202e`, "\u202e"},
		"bell":                   {`\a`, "\a"},
		"escape sequence":        {`\e[31m`, "\x1b[31m"},
		"newline":                {`\n`, "\n"},
		"NUL":                    {`\0`, "\x00"},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{supFile: `suppress: ["article:a` + tt.yamlEsc + `b"]` + "\n"}
			if !strings.ContainsAny(tt.raw, "\x00\n") {
				files[globalFile("a"+tt.raw+"b")] = article("Target")
			}
			msg := loadErr(t, files, supFile)
			if tt.raw != " " && strings.Contains(msg, tt.raw) {
				t.Errorf("error %q carries %+q unescaped", msg, tt.raw)
			}
			assertNoFormatRunes(t, msg)
		})
	}
}

// A reference longer than 256 bytes (ADR 0021) is malformed, and the error
// stays short and still names the file.
func TestSuppressionRejectsOversizedReferences(t *testing.T) {
	const maxErr = 1 << 10
	for name, ref := range map[string]string{
		"just over the cap":  "article:" + strings.Repeat("a", 256-len("article:")+1),
		"most of a megabyte": "article:" + strings.Repeat("ab-", (1<<20-200)/3),
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{supFile: suppressYAML(ref)}, supFile)
			if len(msg) > maxErr {
				t.Fatalf("error is %d bytes, want at most %d: %.200q...", len(msg), maxErr, msg)
			}
		})
	}
	// The negative: a reference of exactly 256 bytes is well formed. It names
	// nothing, so it is a warning, not a load failure.
	mustLoad(t, map[string]string{supFile: suppressYAML("article:" + strings.Repeat("a", 256-len("article:")))})
}

// A rule broken in project.yaml keeps the last good Snapshot serving.
func TestBrokenSuppressionKeepsTheLastGoodSnapshot(t *testing.T) {
	for name, content := range map[string]string{
		"enforced target": suppressYAML("article:rules"),
		"own Item":        suppressYAML("article:own"),
		"bad syntax":      suppressYAML("article:../rules"),
		"unknown field":   "supress: [article:style]\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := writeFiles(t, map[string]string{
				globalFile("rules"): govArticle("Rules", "enforced: true"),
				globalFile("style"): article("Style"),
				projectFile("own"):  article("Own"),
			})
			s := engine.NewSyncer(engine.NewDirSource(root))
			if err := s.Poll(t.Context()); err != nil {
				t.Fatalf("first load: %v", err)
			}
			first := s.Current()
			writeTo(t, root, supFile, content)
			if err := s.Poll(t.Context()); err == nil {
				t.Fatal("Poll succeeded with a broken project.yaml")
			}
			if s.Current() != first {
				t.Fatal("a failing load must keep the last good Snapshot")
			}
			if st := s.Status(); st.LastError == "" || strings.Contains(st.LastError, root) {
				t.Errorf("LastError = %q; want the failure recorded without the root path", st.LastError)
			}
		})
	}
}

// --- Criterion 3: a reference that names nothing is a warning ---

func TestSuppressingAMissingItemIsAWarningNotALoadFailure(t *testing.T) {
	root := writeFiles(t, map[string]string{
		globalFile("style"): article("Style", "rollout"),
		globalFile("kept"):  article("Kept", "rollout"),
		supFile:             suppressYAML("article:gone", "article:style"),
	})
	s := engine.NewSyncer(engine.NewDirSource(root))
	if err := s.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v; a dangling suppression must not fail the load", err)
	}
	snap := s.Current()
	if snap == nil {
		t.Fatal("no Snapshot served")
	}
	if got := hitIDList(snap.SearchIn(govProject, "rollout")); !slices.Equal(got, []string{"kept"}) {
		t.Errorf("SearchIn = %v; the other suppression still applies", got)
	}
	if got := suppressionsOf(t, snap, govProject); !slices.Equal(got, []string{"article:style"}) {
		t.Errorf("Suppressions = %q; want only the Item that exists", got)
	}
	st := s.Status()
	if st.LastError != "" {
		t.Errorf("LastError = %q; a warning is not an error", st.LastError)
	}
	assertOneWarning(t, syncWarnings(t, st), root, supFile, "article:gone")
}

// Deleting one global article must not break every Project that suppressed it.
func TestDeletingASuppressedGlobalItemKeepsTheProjectServing(t *testing.T) {
	root := writeFiles(t, map[string]string{
		globalFile("style"): article("Style"),
		globalFile("kept"):  article("Kept"),
		supFile:             suppressYAML("article:style"),
		otherSupFile:        suppressYAML("article:style"),
	})
	s := engine.NewSyncer(engine.NewDirSource(root))
	if err := s.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := s.Current()
	if w := syncWarnings(t, s.Status()); len(w) != 0 {
		t.Fatalf("Warnings = %q before anything dangles", w)
	}

	if err := os.Remove(filepath.Join(root, filepath.FromSlash(globalFile("style")))); err != nil {
		t.Fatal(err)
	}
	if err := s.Poll(t.Context()); err != nil {
		t.Fatalf("Poll after the deletion: %v", err)
	}
	if s.Current() == first {
		t.Fatal("the good load must swap the Snapshot")
	}
	if _, ok := s.Current().ArticleIn(govProject, "kept"); !ok {
		t.Error("the Project lost an Item it does not suppress")
	}
	w := syncWarnings(t, s.Status())
	if len(w) != 2 {
		t.Fatalf("Warnings = %q; want one per project.yaml", w)
	}
	for _, f := range []string{supFile, otherSupFile} {
		if !slices.ContainsFunc(w, func(s string) bool { return strings.Contains(s, f) && strings.Contains(s, "article:style") }) {
			t.Errorf("Warnings = %q; none names %s and article:style", w, f)
		}
	}

	writeTo(t, root, globalFile("style"), article("Style"))
	if err := s.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w := syncWarnings(t, s.Status()); len(w) != 0 {
		t.Errorf("Warnings = %q once the Item is back; want none", w)
	}
}

// Another Project's Item is not in this Project's view (ADR 0021), so a
// reference to it names nothing here.
func TestSuppressingAnotherProjectsItemIsAWarning(t *testing.T) {
	root := writeFiles(t, map[string]string{
		otherProjectFile("x"): article("X"),
		supFile:               suppressYAML("article:x"),
	})
	s := engine.NewSyncer(engine.NewDirSource(root))
	if err := s.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	assertOneWarning(t, syncWarnings(t, s.Status()), root, supFile, "article:x")
	if _, ok := s.Current().ArticleIn(otherGovProject, "x"); !ok {
		t.Error("the other Project lost its own Item")
	}
}

// A dangling reference has passed the strict <kind>:<name> parse, so the
// warning shows it, as #358 shows a lookup id (quoteID): ASCII-escaped and cut,
// never redacted into nothing, so a nested id stays readable. Non-ASCII never
// appears raw. A reference that fails the parse is a load error and is never
// echoed (TestMalformedSuppressionIsNotEchoed).
func TestSuppressionWarningDoesNotEchoHostileText(t *testing.T) {
	for name, tt := range map[string]struct{ ref, raw, want string }{
		"nested name":     {"article:team/gone", "", "article:team/gone"},
		"token-like name": {"article:ghp_abcdefghij0123456789", "", "article:ghp_abcdefghij0123456789"},
		"homoglyph":       {`"article:p\u0430yments"`, "\u0430", `article:p\u0430yments`},
		"long non-ASCII":  {`"article:` + strings.Repeat(`\u00e9`, 120) + `"`, "\u00e9", `article:\u00e9`},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeFiles(t, map[string]string{supFile: suppressYAML(tt.ref)})
			s := engine.NewSyncer(engine.NewDirSource(root))
			if err := s.Poll(t.Context()); err != nil {
				t.Fatalf("Poll: %v", err)
			}
			w := syncWarnings(t, s.Status())
			if len(w) != 1 {
				t.Fatalf("Warnings = %q; want one", w)
			}
			if !strings.Contains(w[0], supFile) {
				t.Errorf("warning %q does not name %s", w[0], supFile)
			}
			if !strings.Contains(w[0], tt.want) {
				t.Errorf("warning %q does not show the reference as %s", w[0], tt.want)
			}
			if (tt.raw != "" && strings.Contains(w[0], tt.raw)) || strings.Contains(w[0], root) {
				t.Errorf("warning %q echoes %+q or the root path", w[0], tt.raw)
			}
			if len(w[0]) > 1<<10 {
				t.Errorf("warning is %d bytes; want at most 1024", len(w[0]))
			}
			for _, r := range w[0] {
				if r < 0x20 || r > 0x7e {
					t.Errorf("warning %q carries %U raw; non-ASCII must be escaped", w[0], r)
					break
				}
			}
		})
	}
}

// A reference that fails the parse can carry anything, so neither the load
// error nor sync status echoes a secret from it.
func TestMalformedSuppressionIsNotEchoed(t *testing.T) {
	for name, tt := range map[string]struct{ ref, secret string }{
		"credential":       {`"article:token=sk-live-0123456789abcdef x"`, "sk-live-0123456789abcdef"},
		"url":              {`"https://user:hunter2@example.test/x"`, "hunter2"},
		"control sequence": {`"article:a\e[31mb"`, "\x1b"},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{supFile: suppressYAML(tt.ref)}
			if msg := loadErr(t, files, supFile); strings.Contains(msg, tt.secret) {
				t.Errorf("error %q echoes %q", msg, tt.secret)
			}
			s := engine.NewSyncer(engine.NewDirSource(writeFiles(t, files)))
			if err := s.Poll(t.Context()); err == nil {
				t.Fatal("Poll succeeded")
			}
			if last := s.Status().LastError; last == "" || strings.Contains(last, tt.secret) {
				t.Errorf("LastError = %q; want the failure recorded without %q", last, tt.secret)
			}
		})
	}
}

// --- Criterion 4: strict decoding, no symlinks or directories, caps ---

func TestProjectYAMLWithAnUnknownFieldFailsTheLoad(t *testing.T) {
	for name, content := range map[string]string{
		"misspelt suppress":   "supress: [article:style]\n",
		"extra field":         "suppress: [article:style]\nowner: me\n",
		"escape sequence key": "\"\\e[31mkey\\e[0m\": 1\n",
		"bidi key":            "\"\\u202ekey\": 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{globalFile("style"): article("Style"), supFile: content}, supFile)
			assertNoFormatRunes(t, msg)
		})
	}
}

func TestHostileProjectYAMLDocumentsFailTheLoad(t *testing.T) {
	bomb := "suppress:\n- &a [x, x, x, x, x, x, x, x, x]\n"
	for i, prev := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		next := string(rune('b' + i))
		bomb += "- &" + next + " [" + strings.Repeat("*"+prev+", ", 8) + "*" + prev + "]\n"
	}
	for name, content := range map[string]string{
		"bad YAML":        "suppress: [article:style\n",
		"tab indent":      "suppress:\n\t- article:style\n",
		"duplicate key":   "suppress: [article:style]\nsuppress: []\n",
		"alias bomb":      bomb,
		"multi-document":  "suppress: [article:style]\n---\nsuppress: []\n",
		"not a mapping":   "- article:style\n",
		"bare scalar":     "article:style\n",
		"undefined alias": "suppress: *nope\n",
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			loadErr(t, map[string]string{globalFile("style"): article("Style"), supFile: content}, supFile)
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("Load took %v; hostile YAML must fail fast", d)
			}
		})
	}
}

func TestProjectYAMLThatIsASymlinkOrDirectoryIsRejected(t *testing.T) {
	outside := writeFiles(t, map[string]string{"project.yaml": suppressYAML("article:style")})
	for name, setup := range map[string]func(t *testing.T, root string) error{
		"symlink inside the root": func(_ *testing.T, root string) error {
			return os.Symlink("../../../../global/shared.yaml", filepath.Join(root, filepath.FromSlash(supFile)))
		},
		"symlink outside the root": func(_ *testing.T, root string) error {
			return os.Symlink(filepath.Join(outside, "project.yaml"), filepath.Join(root, filepath.FromSlash(supFile)))
		},
		"dangling symlink": func(_ *testing.T, root string) error {
			return os.Symlink("missing.yaml", filepath.Join(root, filepath.FromSlash(supFile)))
		},
		"directory": func(t *testing.T, root string) error {
			writeTo(t, root, supFile+"/suppress.yaml", suppressYAML("article:style"))
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeFiles(t, map[string]string{
				globalFile("style"):  article("Style"),
				"global/shared.yaml": suppressYAML("article:style"),
				projectFile("own"):   article("Own"),
			})
			if err := setup(t, root); err != nil {
				t.Skip("symlinks unavailable")
			}
			snap, err := engine.NewDirSource(root).Load(t.Context())
			if err == nil {
				_, visible := snap.ArticleIn(govProject, "style")
				t.Fatalf("a project.yaml that is not a regular file was accepted (style visible: %v)", visible)
			}
			if !strings.Contains(err.Error(), supFile) || strings.Contains(err.Error(), root) || strings.Contains(err.Error(), outside) {
				t.Fatalf("error = %q; want it to name %s and no host path", err, supFile)
			}
		})
	}
}

// project.yaml is read through readLimited, capped like an article.
func TestProjectYAMLLargerThanTheCapFailsTheLoad(t *testing.T) {
	const capBytes = 1 << 20
	pad := func(total int) string {
		head := "suppress: [article:style]\n#"
		return head + strings.Repeat("x", total-len(head)-1) + "\n"
	}
	loadErr(t, map[string]string{globalFile("style"): article("Style"), supFile: pad(capBytes + 1)}, supFile)
	snap := load(t, map[string]string{globalFile("style"): article("Style"), supFile: pad(capBytes)})
	if _, ok := snap.ArticleIn(govProject, "style"); ok {
		t.Error("a project.yaml of exactly the cap was not applied")
	}
}

func TestSuppressListLongerThanTheCapFailsTheLoad(t *testing.T) {
	refs := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("article:gone-%d", i)
		}
		return out
	}
	mustLoad(t, map[string]string{supFile: suppressYAML(refs(maxSuppressionsInTest)...)})
	msg := loadErr(t, map[string]string{supFile: suppressYAML(refs(maxSuppressionsInTest + 1)...)}, supFile)
	if len(msg) > 1<<10 {
		t.Errorf("error is %d bytes; it must not list the entries", len(msg))
	}
}

// Only projects/<host>/<org>/<repo>/project.yaml is a Project's project.yaml.
// One anywhere else in projects/ is a layout error, as any file there is; one
// inside a Project directory but deeper suppresses nothing.
func TestProjectYAMLOutsideAProjectDirectory(t *testing.T) {
	for name, place := range map[string]string{
		"under projects": "projects/project.yaml",
		"at host depth":  "projects/github.com/project.yaml",
		"at org depth":   "projects/github.com/acme/project.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			loadErr(t, map[string]string{globalFile("style"): article("Style"), place: suppressYAML("article:style")}, place)
		})
	}
	for name, place := range map[string]string{
		"in articles": "projects/" + govProject + "/articles/project.yaml",
		"in a subdir": "projects/" + govProject + "/sub/project.yaml",
		"in .goldie":  "projects/" + govProject + "/.goldie/project.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			snap := load(t, map[string]string{globalFile("style"): article("Style"), projectFile("own"): article("Own"), place: suppressYAML("article:style")})
			if _, ok := snap.ArticleIn(govProject, "style"); !ok {
				t.Errorf("%s suppressed an Item; only the Project's own project.yaml may", place)
			}
			if got := suppressionsOf(t, snap, govProject); len(got) != 0 {
				t.Errorf("Suppressions = %q; want none", got)
			}
		})
	}
}

// --- The git Source shares the loader ---

func TestGitSourceAppliesSuppressions(t *testing.T) {
	repo := newGitRepo(t)
	repo.commit("main", suppressionFiles())
	snap, err := newGit(t, engine.GitOptions{URL: repo.url}).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.ArticleIn(govProject, "style"); ok {
		t.Error("the suppressed Item is served to the Project")
	}
	if got := suppressionsOf(t, snap, govProject); !slices.Equal(got, []string{"article:platform/deploy", "article:style"}) {
		t.Errorf("Suppressions = %q", got)
	}
}

func TestGitSourceRejectsASymlinkedProjectYAML(t *testing.T) {
	repo := newGitRepo(t)
	files := suppressionFiles()
	files["global/shared.yaml"] = suppressYAML("article:style")
	files[supFile] = "symlink:../../../../global/shared.yaml"
	repo.commit("main", files)
	if _, err := newGit(t, engine.GitOptions{URL: repo.url}).Load(t.Context()); err == nil || !strings.Contains(err.Error(), supFile) {
		t.Fatalf("err = %v; want a rejection naming %s", err, supFile)
	}
}

// FuzzProjectYAML checks that no project.yaml panics the loader, that every
// failure is safe to show, and that an accepted file hides only existing,
// non-enforced global Items.
func FuzzProjectYAML(f *testing.F) {
	for _, s := range []string{
		suppressYAML("article:style"),
		suppressYAML("article:rules"),
		suppressYAML("article:own"),
		suppressYAML("article:gone"),
		"supress: []\n",
		"suppress: &a [*a]\n",
		"suppress: [\"article:a\\u202eb\"]\n",
		"suppress: []\n---\nsuppress: []\n",
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, content string) {
		root := writeFiles(t, map[string]string{
			globalFile("style"): article("Style"),
			globalFile("rules"): govArticle("Rules", "enforced: true"),
			projectFile("own"):  article("Own"),
			supFile:             content,
		})
		snap, err := engine.NewDirSource(root).Load(t.Context())
		if err != nil {
			if strings.Contains(err.Error(), root) {
				t.Fatalf("error leaks the root: %q", err)
			}
			for _, r := range err.Error() {
				if unicode.IsControl(r) {
					t.Fatalf("error carries %U: %q", r, err)
				}
			}
			return
		}
		if _, ok := snap.ArticleIn(govProject, "rules"); !ok {
			t.Fatalf("an enforced Item was suppressed by %q", content)
		}
		if _, ok := snap.ArticleIn(govProject, "own"); !ok {
			t.Fatalf("the Project's own Item was suppressed by %q", content)
		}
		for _, ref := range suppressionsOf(t, snap, govProject) {
			id, ok := strings.CutPrefix(ref, "article:")
			if !ok {
				t.Fatalf("Suppressions lists %q, not an article reference", ref)
			}
			if _, ok := snap.Article(id); !ok {
				t.Fatalf("Suppressions lists %q, which names no global Item", ref)
			}
			if _, ok := snap.ArticleIn(govProject, id); ok {
				t.Fatalf("Suppressions lists %q, but the Project still sees it", ref)
			}
		}
	})
}

func assertOneWarning(t *testing.T, w []string, root string, want ...string) {
	t.Helper()
	if len(w) != 1 {
		t.Fatalf("Warnings = %q; want exactly one", w)
	}
	for _, s := range want {
		if !strings.Contains(w[0], s) {
			t.Errorf("warning %q does not name %s", w[0], s)
		}
	}
	if strings.Contains(w[0], root) {
		t.Errorf("warning %q leaks the root path", w[0])
	}
}

// assertNoFormatRunes fails when msg carries a format (Cf) rune raw, such as a
// bidi override or zero-width space; loadErr already rejects control runes.
func assertNoFormatRunes(t *testing.T, msg string) {
	t.Helper()
	for _, r := range msg {
		if unicode.Is(unicode.Cf, r) {
			t.Errorf("error %q carries the format character %U raw", msg, r)
			return
		}
	}
}
