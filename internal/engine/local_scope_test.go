package engine_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #389: a repository's own .goldie/ is the local Scope (CONTEXT: Scope).
// NewLocalSource(base Source, goldieDir string) decorates a Source: it loads
// the base, then .goldie/articles/** as Scope "local" (engine.ScopeLocal), and
// takes suppress from .goldie/config.yaml, which may also hold project, addr
// and dir (read by the client, ignored here) and nothing else. Resolution is
// local > project > global (CONTEXT: Resolution). The engine is not told which
// Project the checkout is (config.yaml's project is ignored), so a local Item
// answers in every view: no Project, a registered Project, an unregistered one
// (#10, the embedded server serves one checkout).
//
// A published local Item that shadows an Item enforced at any wider Scope
// fails the load, naming both as article:<name> (CONTEXT: Enforced Item). A
// local draft never shadows in the default view (#353). References from local
// Items resolve in the local view, local then global (ADR 0021); a global or
// Project Item's references never see a local Item. Errors name files relative
// to the repository root (.goldie/...) and never a host path.
//
// The fixtures keep the base content and the repository in two directories, so
// an error that leaks either is caught.

// localConfigFile is the local Scope's settings file, relative to the
// repository root.
const localConfigFile = ".goldie/config.yaml"

// localFile is the path of a local article, relative to the repository root.
func localFile(id string) string { return ".goldie/articles/" + id + ".md" }

// localDirs is where a fixture wrote the base content and the repository.
type localDirs struct{ content, repo string }

// newLocalSource writes the files whose path starts with .goldie/ into a
// repository directory and the rest into a content directory, and returns the
// local Source over a DirSource of the content.
func newLocalSource(t *testing.T, files map[string]string) (engine.Source, localDirs) {
	t.Helper()
	base, local := map[string]string{}, map[string]string{}
	for name, content := range files {
		if strings.HasPrefix(name, ".goldie/") {
			local[name] = content
		} else {
			base[name] = content
		}
	}
	dirs := localDirs{content: writeFiles(t, base), repo: writeFiles(t, local)}
	return engine.NewLocalSource(engine.NewDirSource(dirs.content), filepath.Join(dirs.repo, ".goldie")), dirs
}

func loadLocal(t *testing.T, files map[string]string) *engine.Snapshot {
	t.Helper()
	src, _ := newLocalSource(t, files)
	snap, err := src.Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap == nil {
		t.Fatal("Load returned no Snapshot and no error")
	}
	return snap
}

// loadLocalErr loads files through the local Source and returns the error,
// failing the test when Load succeeds. The error must contain each of want,
// and must not carry a host path or a control character.
func loadLocalErr(t *testing.T, files map[string]string, want ...string) string {
	t.Helper()
	src, dirs := newLocalSource(t, files)
	snap, err := src.Load(t.Context())
	if err == nil {
		t.Fatalf("Load succeeded, want an error naming %q: %+v", want, snap.Articles())
	}
	msg := err.Error()
	assertNoHostPath(t, msg, dirs)
	for _, r := range msg {
		if unicode.IsControl(r) {
			t.Errorf("error carries the control character %U: %q", r, msg)
			break
		}
	}
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("error %q does not name %s", msg, w)
		}
	}
	return msg
}

func assertNoHostPath(t *testing.T, msg string, dirs localDirs) {
	t.Helper()
	for _, p := range []string{dirs.content, dirs.repo, filepath.Dir(dirs.repo)} {
		if strings.Contains(msg, p) {
			t.Errorf("%q leaks the host path %s", msg, p)
		}
	}
}

// answer resolves id in proj's view and says which copy answered, by its
// Scope and Name, or "none".
func answer(snap *engine.Snapshot, proj, id string, opts ...engine.ReadOptions) string {
	a, ok := snap.ArticleIn(proj, id, opts...)
	if !ok {
		return "none"
	}
	return a.Scope + "/" + a.Name
}

// localViews are the views a local Item answers in: no Project, the
// registered Project in two spellings, and an unregistered Project.
var localViews = []string{"", govProject, strings.ToUpper(govProject), "github.com/acme/unregistered"}

// --- Criterion 1: .goldie/articles is the local Scope ---

func TestScopeLocalIsLocal(t *testing.T) {
	if engine.ScopeLocal != "local" {
		t.Fatalf("ScopeLocal = %q; CONTEXT names the Scope local", engine.ScopeLocal)
	}
}

func TestLocalArticleIsServedInEveryView(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalFile("kept"):       article("Kept"),
		projectFile("own"):       article("Own"),
		localFile("team/guide"):  article("Guide", "rollout"),
		localFile("team/second"): article("Second"),
	})
	for _, proj := range localViews {
		for _, opts := range [][]engine.ReadOptions{nil, {withDrafts}} {
			for _, id := range []string{"team/guide", "Team/GUIDE"} {
				a, ok := snap.ArticleIn(proj, id, opts...)
				if !ok {
					t.Fatalf("ArticleIn(%q, %q, %v) not found; a local Item answers in every view", proj, id, opts)
				}
				if a.ID != "team/guide" || a.Scope != engine.ScopeLocal || a.Name != "Guide" || !strings.Contains(a.Content, "Body of Guide.") {
					t.Errorf("ArticleIn(%q, %q, %v) = ID %q, Scope %q, Name %q; want team/guide, local, Guide", proj, id, opts, a.ID, a.Scope, a.Name)
				}
			}
			hits := snap.SearchIn(proj, "rollout", opts...)
			if len(hits) != 1 || hits[0].ID != "team/guide" || hits[0].Scope != engine.ScopeLocal || hits[0].Kind != engine.KindArticle {
				t.Errorf("SearchIn(%q, rollout, %v) = %+v; want the one local article, Scope local, Kind article", proj, opts, hits)
			}
		}
	}
	if a, ok := snap.Article("team/second"); !ok || a.Scope != engine.ScopeLocal {
		t.Errorf("Article(team/second) = %+v, %v; want the local Item", a, ok)
	}
	if got := snap.Projects(); !slices.Equal(got, []string{govProject}) {
		t.Errorf("Projects() = %q; the base's Projects stay registered", got)
	}
}

// The resolution fixture: style exists at every Scope, the rest at one or two.
func resolutionFiles() map[string]string {
	return map[string]string{
		globalFile("style"):           article("Global style", "rollout"),
		projectFile("style"):          article("Project style", "rollout"),
		localFile("style"):            article("Local style", "rollout"),
		globalFile("only-global"):     article("Global only", "rollout"),
		projectFile("only-project"):   article("Project only", "rollout"),
		localFile("only-local"):       article("Local only", "rollout"),
		globalFile("global-project"):  article("Global gp", "rollout"),
		projectFile("global-project"): article("Project gp", "rollout"),
		globalFile("global-local"):    article("Global gl", "rollout"),
		localFile("Global-Local"):     article("Local gl", "rollout"),
	}
}

func TestLocalResolvesLocalThenProjectThenGlobal(t *testing.T) {
	snap := loadLocal(t, resolutionFiles())
	local, global := engine.ScopeLocal+"/", engine.ScopeGlobal+"/"
	project := govProject + "/"
	for _, proj := range []string{govProject, strings.ToUpper(govProject)} {
		for id, want := range map[string]string{
			"style":          local + "Local style",
			"STYLE":          local + "Local style",
			"only-global":    global + "Global only",
			"only-project":   project + "Project only",
			"only-local":     local + "Local only",
			"global-project": project + "Project gp",
			"global-local":   local + "Local gl",
		} {
			if got := answer(snap, proj, id); got != want {
				t.Errorf("ArticleIn(%q, %q) answered %s; want %s", proj, id, got, want)
			}
		}
	}
	for _, proj := range []string{"", otherGovProject, "github.com/acme/unregistered"} {
		for id, want := range map[string]string{
			"style":          local + "Local style",
			"only-global":    global + "Global only",
			"only-project":   "none",
			"only-local":     local + "Local only",
			"global-project": global + "Global gp",
			"global-local":   local + "Local gl",
		} {
			if got := answer(snap, proj, id); got != want {
				t.Errorf("ArticleIn(%q, %q) answered %s; want %s", proj, id, got, want)
			}
		}
	}
}

func TestLocalSearchReturnsOneHitPerItemFromTheWinningScope(t *testing.T) {
	snap := loadLocal(t, resolutionFiles())
	scopes := func(hits []engine.Hit) map[string]string {
		out := map[string]string{}
		for _, h := range hits {
			if _, dup := out[strings.ToLower(h.ID)]; dup {
				t.Errorf("%s is listed twice: %+v", h.ID, hits)
			}
			out[strings.ToLower(h.ID)] = h.Scope + "/" + h.Name
		}
		return out
	}
	local, global, project := engine.ScopeLocal+"/", engine.ScopeGlobal+"/", govProject+"/"
	inProject := map[string]string{
		"style":          local + "Local style",
		"only-global":    global + "Global only",
		"only-project":   project + "Project only",
		"only-local":     local + "Local only",
		"global-project": project + "Project gp",
		"global-local":   local + "Local gl",
	}
	if got := scopes(snap.SearchIn(govProject, "rollout")); !mapsEqual(got, inProject) {
		t.Errorf("SearchIn(%q) = %v; want %v", govProject, got, inProject)
	}
	outside := map[string]string{
		"style":          local + "Local style",
		"only-global":    global + "Global only",
		"only-local":     local + "Local only",
		"global-project": global + "Global gp",
		"global-local":   local + "Local gl",
	}
	for _, proj := range []string{"", "github.com/acme/unregistered"} {
		if got := scopes(snap.SearchIn(proj, "rollout")); !mapsEqual(got, outside) {
			t.Errorf("SearchIn(%q) = %v; want %v", proj, got, outside)
		}
	}
	if got := scopes(snap.Search("rollout")); !mapsEqual(got, outside) {
		t.Errorf("Search = %v; want %v", got, outside)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// A local draft is left out of the default view (#353), so it shadows nothing
// there. In the drafts view a draft shadows like any other Item, within
// winner's rule (ReadOptions.Drafts), as a Project draft does.
func TestLocalDraftNeverShadowsInTheDefaultView(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalFile("style"):  article("Global style", "rollout"),
		projectFile("style"): article("Project style", "rollout"),
		localFile("style"):   govArticle("Local style", "status: draft", "keywords: [rollout]"),
		localFile("wip"):     govArticle("Local wip", "status: draft", "keywords: [rollout]"),
	})
	local := engine.ScopeLocal + "/"
	for proj, want := range map[string]string{
		govProject:                     govProject + "/Project style",
		"":                             engine.ScopeGlobal + "/Global style",
		"github.com/acme/unregistered": engine.ScopeGlobal + "/Global style",
	} {
		if got := answer(snap, proj, "style"); got != want {
			t.Errorf("ArticleIn(%q, style) answered %s; a local draft must not shadow %s", proj, got, want)
		}
		if got := answer(snap, proj, "wip"); got != "none" {
			t.Errorf("ArticleIn(%q, wip) answered %s; a draft is left out of the default view", proj, got)
		}
		for _, h := range snap.SearchIn(proj, "rollout") {
			if h.Scope == engine.ScopeLocal {
				t.Errorf("SearchIn(%q) returned the local draft %s", proj, h.ID)
			}
		}
		if got := answer(snap, proj, "style", withDrafts); got != local+"Local style" {
			t.Errorf("ArticleIn(%q, style, Drafts) answered %s; in the drafts view the local draft answers", proj, got)
		}
		if got := answer(snap, proj, "wip", withDrafts); got != local+"Local wip" {
			t.Errorf("ArticleIn(%q, wip, Drafts) answered %s; want the local draft", proj, got)
		}
	}
}

// A deprecated local Item is served and shadows, as a deprecated Project copy
// does.
func TestDeprecatedLocalItemStillShadows(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalFile("old"): article("Global old"),
		globalFile("new"): article("New"),
		localFile("old"):  govArticle("Local old", "status: deprecated", "replaced_by: article:new"),
	})
	for _, proj := range localViews {
		a, ok := snap.ArticleIn(proj, "old")
		if !ok || a.Scope != engine.ScopeLocal || a.Status != engine.StatusDeprecated || a.ReplacedBy != "article:new" {
			t.Errorf("ArticleIn(%q, old) = %+v, %v; want the deprecated local copy with its replaced_by", proj, a, ok)
		}
	}
}

// --- The decorator: the base is loaded first, and left alone ---

// fixedSource returns the same Snapshot, or error, on every Load, as a Source
// that caches its last good load would.
type fixedSource struct {
	snap *engine.Snapshot
	err  error
}

func (f fixedSource) Load(context.Context) (*engine.Snapshot, error) { return f.snap, f.err }

func TestLocalSourceDoesNotChangeTheBaseSnapshot(t *testing.T) {
	base := load(t, map[string]string{
		globalFile("style"): article("Global style"),
		globalFile("kept"):  article("Kept"),
		projectFile("own"):  article("Own"),
	})
	repo := writeFiles(t, map[string]string{
		localFile("style"): article("Local style"),
		localFile("mine"):  article("Mine"),
		localConfigFile:    suppressYAML("article:kept"),
	})
	src := engine.NewLocalSource(fixedSource{snap: base}, filepath.Join(repo, ".goldie"))
	for i := range 2 {
		snap, err := src.Load(t.Context())
		if err != nil {
			t.Fatalf("Load %d: %v; loading twice over the same base must work", i+1, err)
		}
		if got := answer(snap, "", "style"); got != engine.ScopeLocal+"/Local style" {
			t.Errorf("Load %d: ArticleIn(style) answered %s; want the local copy", i+1, got)
		}
		if got := answer(snap, "", "kept"); got != "none" {
			t.Errorf("Load %d: ArticleIn(kept) answered %s; the local config.yaml suppresses it", i+1, got)
		}
	}
	for _, proj := range []string{"", govProject} {
		if got := answer(base, proj, "style"); got != engine.ScopeGlobal+"/Global style" {
			t.Errorf("base ArticleIn(%q, style) answered %s; the base Snapshot must not change", proj, got)
		}
		if got := answer(base, proj, "kept"); got != engine.ScopeGlobal+"/Kept" {
			t.Errorf("base ArticleIn(%q, kept) answered %s; the local suppression must not reach the base", proj, got)
		}
		if got := answer(base, proj, "mine"); got != "none" {
			t.Errorf("base ArticleIn(%q, mine) answered %s; the base must not gain local Items", proj, got)
		}
	}
}

func TestLocalSourcePassesTheBaseErrorThrough(t *testing.T) {
	baseErr := errors.New("the base broke")
	repo := writeFiles(t, map[string]string{localFile("mine"): article("Mine")})
	snap, err := engine.NewLocalSource(fixedSource{err: baseErr}, filepath.Join(repo, ".goldie")).Load(t.Context())
	if !errors.Is(err, baseErr) || snap != nil {
		t.Fatalf("Load = %v, %v; want no Snapshot and the base's error", snap, err)
	}
}

func TestLocalSourceLoadsNothingOnACancelledContext(t *testing.T) {
	src, _ := newLocalSource(t, map[string]string{localFile("mine"): article("Mine")})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if snap, err := src.Load(ctx); err == nil || snap != nil {
		t.Fatalf("Load on a cancelled context = %v, %v; want no Snapshot and an error", snap, err)
	}
}

// The local Scope's warnings come on top of the base's.
func TestLocalSourceKeepsTheBaseWarnings(t *testing.T) {
	src, dirs := newLocalSource(t, map[string]string{
		globalFile("kept"): article("Kept"),
		supFile:            suppressYAML("article:gone-from-project"),
		localConfigFile:    suppressYAML("article:gone-from-local"),
	})
	s := engine.NewSyncer(src)
	if err := s.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v; a dangling suppression is a warning", err)
	}
	w := syncWarnings(t, s.Status())
	if len(w) != 2 {
		t.Fatalf("Warnings = %q; want one from project.yaml and one from .goldie/config.yaml", w)
	}
	for _, want := range [][2]string{{supFile, "article:gone-from-project"}, {localConfigFile, "article:gone-from-local"}} {
		if !slices.ContainsFunc(w, func(s string) bool { return strings.Contains(s, want[0]) && strings.Contains(s, want[1]) }) {
			t.Errorf("Warnings = %q; none names %s and %s", w, want[0], want[1])
		}
	}
	for _, msg := range w {
		assertNoHostPath(t, msg, dirs)
	}
}

// --- Criterion 1: .goldie/config.yaml ---

func localSuppressionFiles(config string) map[string]string {
	return map[string]string{
		globalFile("platform/deploy"): article("Deploy", "rollout"),
		globalFile("style"):           article("Style", "rollout"),
		globalFile("kept"):            article("Kept", "rollout"),
		localConfigFile:               config,
	}
}

func TestLocalConfigSuppressHidesGlobalItemsInEveryView(t *testing.T) {
	snap := loadLocal(t, localSuppressionFiles(suppressYAML("article:platform/deploy", "article:Style")))
	for _, proj := range localViews {
		for _, opts := range [][]engine.ReadOptions{nil, {withDrafts}} {
			if got := hitIDList(snap.SearchIn(proj, "rollout", opts...)); !slices.Equal(got, []string{"kept"}) {
				t.Errorf("SearchIn(%q, %v) = %v; want only kept", proj, opts, got)
			}
			for _, id := range []string{"platform/deploy", "PLATFORM/Deploy", "style"} {
				if got := answer(snap, proj, id, opts...); got != "none" {
					t.Errorf("ArticleIn(%q, %q, %v) answered %s; the local config.yaml suppresses it", proj, id, opts, got)
				}
			}
			if got := answer(snap, proj, "kept", opts...); got != engine.ScopeGlobal+"/Kept" {
				t.Errorf("ArticleIn(%q, kept, %v) answered %s; only suppressed Items are hidden", proj, opts, got)
			}
		}
	}
}

// config.yaml takes the same suppress forms as project.yaml.
func TestLocalConfigAcceptsTheProjectYAMLForms(t *testing.T) {
	for name, tt := range map[string]struct {
		config string
		hidden []string
	}{
		"flow list":        {"suppress: [article:style]\n", []string{"style"}},
		"block list":       {"suppress:\n  - article:style\n  - article:kept\n", []string{"style", "kept"}},
		"leading marker":   {"---\nsuppress: [article:style]\n", []string{"style"}},
		"empty list":       {"suppress: []\n", nil},
		"comment only":     {"# nothing suppressed\n", nil},
		"empty file":       {"", nil},
		"duplicate entry":  {"suppress: [article:style, article:STYLE]\n", []string{"style"}},
		"deprecated items": {"suppress: [article:old]\n", []string{"old"}},
	} {
		t.Run(name, func(t *testing.T) {
			files := localSuppressionFiles(tt.config)
			files[globalFile("old")] = govArticle("Old", "status: deprecated")
			snap := loadLocal(t, files)
			for _, id := range []string{"style", "kept", "old", "platform/deploy"} {
				want := slices.Contains(tt.hidden, id)
				if _, ok := snap.ArticleIn("", id); ok == want {
					t.Errorf("ArticleIn(%q) found = %v; want hidden = %v", id, ok, want)
				}
			}
		})
	}
}

func TestLocalConfigIgnoresProjectAddrAndDir(t *testing.T) {
	for name, config := range map[string]string{
		"project":   "project: github.com/acme/shop\n",
		"addr":      "addr: https://goldie.example.test\n",
		"dir":       "dir: content\n",
		"all three": "project: github.com/acme/elsewhere\naddr: http://127.0.0.1:8080\ndir: ../content\n",
		"with suppress": "project: " + govProject + "\naddr: https://goldie.example.test\ndir: content\n" +
			"suppress: [article:style]\n",
	} {
		t.Run(name, func(t *testing.T) {
			src, _ := newLocalSource(t, map[string]string{
				globalFile("style"): article("Style"),
				projectFile("own"):  article("Own"),
				localFile("mine"):   article("Mine"),
				localConfigFile:     config,
			})
			s := engine.NewSyncer(src)
			if err := s.Poll(t.Context()); err != nil {
				t.Fatalf("Poll: %v; project, addr and dir are not the engine's to read", err)
			}
			if w := syncWarnings(t, s.Status()); len(w) != 0 {
				t.Errorf("Warnings = %q; an ignored key is not a finding", w)
			}
			snap := s.Current()
			wantStyle := engine.ScopeGlobal + "/Style"
			if strings.Contains(config, "suppress") {
				wantStyle = "none"
			}
			// project does not pick the view: the local Scope answers in every
			// one, and the suppression applies in every one.
			for _, proj := range localViews {
				if got := answer(snap, proj, "mine"); got != engine.ScopeLocal+"/Mine" {
					t.Errorf("ArticleIn(%q, mine) answered %s; want the local Item", proj, got)
				}
				if got := answer(snap, proj, "style"); got != wantStyle {
					t.Errorf("ArticleIn(%q, style) answered %s; want %s", proj, got, wantStyle)
				}
			}
		})
	}
}

// Any key but suppress, project, addr and dir fails the load. The error names
// the file and never echoes a value, which may be a secret.
func TestLocalConfigWithAnyOtherKeyFailsTheLoad(t *testing.T) {
	for name, tt := range map[string]struct{ config, secret string }{
		"misspelt suppress": {"supress: [article:style]\n", ""},
		"extra key":         {"suppress: [article:style]\nowner: me\n", ""},
		"token value":       {"token: sk-live-0123456789abcdef\n", "sk-live-0123456789abcdef"},
		"nested secret":     {"auth:\n  password: hunter2-correct-horse\n", "hunter2-correct-horse"},
		"url with password": {"upstream: https://user:s3cr3t-pass@goldie.example.test\n", "s3cr3t-pass"},
		"quoted value":      {"owner: \"Secret Owner Value\"\n", "Secret Owner Value"},
		"escape key":        {"\"\\e[31mkey\\e[0m\": 1\n", "\x1b"},
		"bidi key":          {"\"\\u202ekey\": 1\n", "\u202e"},
		"project misspelt":  {"projet: github.com/acme/shop\n", ""},
		"upper-case key":    {"Suppress: [article:style]\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadLocalErr(t, localSuppressionFiles(tt.config), localConfigFile)
			if tt.secret != "" && strings.Contains(msg, tt.secret) {
				t.Errorf("error %q echoes %q", msg, tt.secret)
			}
			if strings.Contains(msg, "project.yaml") {
				t.Errorf("error %q names project.yaml; the file is %s", msg, localConfigFile)
			}
			assertNoFormatRunes(t, msg)
			if len(msg) > 1<<10 {
				t.Errorf("error is %d bytes; want at most 1024", len(msg))
			}
		})
	}
}

func TestLocalConfigIsDecodedStrictly(t *testing.T) {
	bomb := "suppress:\n- &a [x, x, x, x, x, x, x, x, x]\n"
	for i, prev := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		next := string(rune('b' + i))
		bomb += "- &" + next + " [" + strings.Repeat("*"+prev+", ", 8) + "*" + prev + "]\n"
	}
	for name, config := range map[string]string{
		"bad YAML":       "suppress: [article:style\n",
		"duplicate key":  "suppress: [article:style]\nsuppress: []\n",
		"alias bomb":     bomb,
		"multi-document": "suppress: [article:style]\n---\nsuppress: []\n",
		"not a mapping":  "- article:style\n",
		"bare scalar":    "article:style\n",
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			loadLocalErr(t, localSuppressionFiles(config), localConfigFile)
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("Load took %v; hostile YAML must fail fast", d)
			}
		})
	}
}

func TestLocalSuppressionWithBadSyntaxFailsTheLoad(t *testing.T) {
	for name, config := range map[string]string{
		"no kind":            "suppress: [style]\n",
		"unknown kind":       "suppress: [skill:style]\n",
		"upper-case kind":    "suppress: [ARTICLE:style]\n",
		"dot-dot segment":    "suppress: [article:../style]\n",
		"leading slash":      "suppress: [article:/style]\n",
		"empty string":       "suppress: [\"\"]\n",
		"null entry":         "suppress: [~]\n",
		"number entry":       "suppress: [1]\n",
		"scalar not a list":  "suppress: article:style\n",
		"mapping not a list": "suppress: {article: style}\n",
		"null list":          "suppress:\n",
		"zero-width space":   "suppress: [\"article:st\\u200byle\"]\n",
		"oversized":          suppressYAML("article:" + strings.Repeat("a", 256-len("article:")+1)),
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadLocalErr(t, localSuppressionFiles(config), localConfigFile)
			assertNoFormatRunes(t, msg)
		})
	}
}

func TestLocalSuppressListLongerThanTheCapFailsTheLoad(t *testing.T) {
	refs := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("article:gone-%d", i)
		}
		return out
	}
	loadLocal(t, map[string]string{localConfigFile: suppressYAML(refs(maxSuppressionsInTest)...)})
	msg := loadLocalErr(t, map[string]string{localConfigFile: suppressYAML(refs(maxSuppressionsInTest + 1)...)}, localConfigFile)
	if len(msg) > 1<<10 {
		t.Errorf("error is %d bytes; it must not list the entries", len(msg))
	}
}

func TestLocalSuppressionOfAMissingItemIsAWarning(t *testing.T) {
	src, dirs := newLocalSource(t, localSuppressionFiles(suppressYAML("article:gone", "article:style")))
	s := engine.NewSyncer(src)
	if err := s.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v; a dangling suppression must not fail the load", err)
	}
	st := s.Status()
	if st.LastError != "" {
		t.Errorf("LastError = %q; a warning is not an error", st.LastError)
	}
	w := syncWarnings(t, st)
	assertOneWarning(t, w, dirs.content, localConfigFile, "article:gone")
	assertNoHostPath(t, w[0], dirs)
	if got := hitIDList(s.Current().SearchIn("", "rollout")); !slices.Equal(got, []string{"kept", "platform/deploy"}) {
		t.Errorf("SearchIn = %v; the other suppression still applies", got)
	}
}

func TestLocalSuppressionOfTheLocalScopesOwnItemFailsTheLoad(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"local only":         {localFile("own"): article("Own"), localConfigFile: suppressYAML("article:own")},
		"other case":         {localFile("own"): article("Own"), localConfigFile: suppressYAML("article:OWN")},
		"shadowing a global": {localFile("style"): article("Local style"), globalFile("style"): article("Style"), localConfigFile: suppressYAML("article:style")},
		"nested":             {localFile("team/own"): article("Own"), localConfigFile: suppressYAML("article:team/own")},
	} {
		t.Run(name, func(t *testing.T) { loadLocalErr(t, files, localConfigFile) })
	}
}

// --- Criterion 2: enforcement ---

func TestLocalItemShadowingAnEnforcedGlobalItemFailsTheLoad(t *testing.T) {
	for name, tt := range map[string]struct {
		globalID, localID string
		globalFront       []string
		localFront        []string
	}{
		"published by default":   {"rules", "rules", []string{"enforced: true"}, nil},
		"explicit published":     {"rules", "rules", []string{"enforced: true", "status: published"}, []string{"status: published"}},
		"enforced is deprecated": {"rules", "rules", []string{"enforced: true", "status: deprecated"}, nil},
		"local is deprecated":    {"rules", "rules", []string{"enforced: true"}, []string{"status: deprecated"}},
		"local is enforced too":  {"rules", "rules", []string{"enforced: true"}, []string{"enforced: true"}},
		"other case":             {"rules", "Rules", []string{"enforced: true"}, nil},
		"nested":                 {"team/policy", "Team/Policy", []string{"enforced: true"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			loadLocalErr(t, map[string]string{
				globalFile(tt.globalID): govArticle("Rules", tt.globalFront...),
				localFile(tt.localID):   govArticle("Local rules", tt.localFront...),
				globalFile("ok"):        article("Ok"),
			}, "article:"+tt.localID, "article:"+tt.globalID)
		})
	}
}

// "A narrower Scope cannot shadow it" holds for an Item a Project enforces.
// The local Scope answers in every Project's view, so it may shadow no
// Project's enforced Item.
func TestLocalItemShadowingAnEnforcedProjectItemFailsTheLoad(t *testing.T) {
	for name, tt := range map[string]struct {
		projectPath, localID string
		front                []string
	}{
		"published by default": {projectFile("rules"), "rules", []string{"enforced: true"}},
		"deprecated":           {projectFile("rules"), "rules", []string{"enforced: true", "status: deprecated"}},
		"other case":           {projectFile("rules"), "RULES", []string{"enforced: true"}},
		"another Project":      {otherProjectFile("rules"), "rules", []string{"enforced: true"}},
		"over a global copy":   {projectFile("rules"), "rules", []string{"enforced: true"}},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{
				tt.projectPath:        govArticle("Project rules", tt.front...),
				localFile(tt.localID): article("Local rules"),
			}
			if name == "over a global copy" {
				files[globalFile("rules")] = article("Global rules")
			}
			loadLocalErr(t, files, "article:"+tt.localID, "article:rules")
		})
	}
}

// A draft does not enforce, and a local draft shadows nothing in the default
// view, so neither fails the load. The enforced copy answers wherever it
// applies, in the drafts view too (#359).
func TestLocalDraftOverAnEnforcedItemLoadsAndTheEnforcedCopyAnswers(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalFile("rules"):  govArticle("Global rules", "enforced: true"),
		projectFile("shop"):  govArticle("Project shop", "enforced: true"),
		localFile("rules"):   govArticle("Local rules", "status: draft"),
		localFile("shop"):    govArticle("Local shop", "status: draft"),
		globalFile("kept"):   article("Kept"),
		localFile("unrelat"): article("Unrelated"),
	})
	for _, opts := range [][]engine.ReadOptions{nil, {withDrafts}} {
		for _, proj := range localViews {
			if got := answer(snap, proj, "rules", opts...); got != engine.ScopeGlobal+"/Global rules" {
				t.Errorf("ArticleIn(%q, rules, %v) answered %s; the enforced global Item must answer", proj, opts, got)
			}
		}
		if got := answer(snap, govProject, "shop", opts...); got != govProject+"/Project shop" {
			t.Errorf("ArticleIn(%q, shop, %v) answered %s; the enforced Project Item must answer", govProject, opts, got)
		}
	}
}

func TestLocalItemOverAnEnforcedDraftLoadsAndAnswers(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalFile("rules"): govArticle("Global rules", "enforced: true", "status: draft"),
		projectFile("shop"): govArticle("Project shop", "enforced: true", "status: draft"),
		localFile("rules"):  article("Local rules"),
		localFile("shop"):   article("Local shop"),
	})
	for _, proj := range []string{"", govProject} {
		if got := answer(snap, proj, "rules"); got != engine.ScopeLocal+"/Local rules" {
			t.Errorf("ArticleIn(%q, rules) answered %s; a draft does not enforce", proj, got)
		}
		if got := answer(snap, proj, "shop"); got != engine.ScopeLocal+"/Local shop" {
			t.Errorf("ArticleIn(%q, shop) answered %s; a draft does not enforce", proj, got)
		}
	}
}

// The enforcement error names Items, not their content.
func TestLocalEnforcementErrorDoesNotEchoContent(t *testing.T) {
	msg := loadLocalErr(t, map[string]string{
		globalFile("rules"): docWith("Secret global title", "Hush global body.", "enforced: true", "keywords: [hushkey]"),
		localFile("rules"):  docWith("Secret local title", "Hush local body."),
	}, "article:rules")
	for _, s := range []string{"Secret global title", "Secret local title", "Hush", "hushkey"} {
		if strings.Contains(msg, s) {
			t.Errorf("error %q echoes %q from the content", msg, s)
		}
	}
}

// Enforcement beats suppression (#360): config.yaml may not suppress an
// enforced Item, but may suppress an enforced draft, which does not enforce.
func TestLocalSuppressionOfAnEnforcedItemFailsTheLoad(t *testing.T) {
	for name, tt := range map[string]struct {
		id, ref string
		front   []string
	}{
		"published by default": {"rules", "article:rules", []string{"enforced: true"}},
		"deprecated":           {"rules", "article:rules", []string{"enforced: true", "status: deprecated"}},
		"other case":           {"rules", "article:RULES", []string{"enforced: true"}},
		"nested":               {"team/policy", "article:team/policy", []string{"enforced: true"}},
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadLocalErr(t, map[string]string{
				globalFile(tt.id): govArticle("Rules", tt.front...),
				globalFile("ok"):  article("Ok"),
				localConfigFile:   suppressYAML("article:ok", tt.ref),
			}, localConfigFile)
			if !strings.Contains(strings.ToLower(msg), tt.id) {
				t.Errorf("error %q does not name the enforced Item %s", msg, tt.id)
			}
		})
	}
	loadLocal(t, map[string]string{
		globalFile("rules"): govArticle("Rules", "enforced: true", "status: draft"),
		localConfigFile:     suppressYAML("article:rules"),
	})
}

// A broken local Scope keeps the last good Snapshot serving.
func TestBrokenLocalScopeKeepsTheLastGoodSnapshot(t *testing.T) {
	for name, tt := range map[string]struct{ file, content string }{
		"shadows an enforced Item":    {localFile("rules"), article("Local rules")},
		"suppresses an enforced Item": {localConfigFile, suppressYAML("article:rules")},
		"unknown config key":          {localConfigFile, "owner: me\n"},
		"dangling lookup":             {localFile("guide"), docWith("Guide", fence("goldie lookup gone"))},
		"bad frontmatter":             {localFile("bad"), "no frontmatter\n"},
	} {
		t.Run(name, func(t *testing.T) {
			src, dirs := newLocalSource(t, map[string]string{
				globalFile("rules"): govArticle("Rules", "enforced: true"),
				localFile("mine"):   article("Mine"),
			})
			s := engine.NewSyncer(src)
			if err := s.Poll(t.Context()); err != nil {
				t.Fatalf("first load: %v", err)
			}
			first := s.Current()
			writeTo(t, dirs.repo, tt.file, tt.content)
			err := s.Poll(t.Context())
			if err == nil {
				t.Fatal("Poll succeeded with a broken local Scope")
			}
			if s.Current() != first {
				t.Fatal("a failing load must keep the last good Snapshot")
			}
			assertNoHostPath(t, err.Error(), dirs)
			if st := s.Status(); st.LastError == "" {
				t.Error("LastError is empty after a failed load")
			} else {
				assertNoHostPath(t, st.LastError, dirs)
			}
		})
	}
}

// --- Criterion 3: references from local Items resolve in the local view ---

func TestLocalReferencesResolveInTheLocalView(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"lookup of a local Item": {
			localFile("team/target"): article("Target"),
			localFile("guide"):       docWith("Guide", fence("goldie lookup team/target")+"\nAlso `goldie lookup TEAM/Target`."),
		},
		"replaced_by a local Item": {
			localFile("new"): article("New"),
			localFile("old"): govArticle("Old", "status: deprecated", "replaced_by: article:New"),
		},
		"lookup of a global Item": {
			globalFile("kept"): article("Kept"),
			localFile("guide"): docWith("Guide", fence("goldie lookup kept")),
		},
		"replaced_by a global Item": {
			globalFile("kept"): article("Kept"),
			localFile("old"):   govArticle("Old", "status: deprecated", "replaced_by: article:kept"),
		},
		"lookup of a local Item that shadows a global": {
			globalFile("style"): article("Global style"),
			localFile("style"):  article("Local style"),
			localFile("guide"):  docWith("Guide", fence("goldie lookup style")),
		},
		"replaced_by a local Item that shadows a global": {
			globalFile("style"): govArticle("Global style", "status: draft"),
			localFile("style"):  article("Local style"),
			localFile("old"):    govArticle("Old", "status: deprecated", "replaced_by: article:Style"),
		},
		"lookup past a local draft to the global Item": {
			globalFile("style"): article("Global style"),
			localFile("style"):  govArticle("Local style", "status: draft"),
			localFile("guide"):  docWith("Guide", fence("goldie lookup style")),
		},
		"chain within the local Scope": {
			localFile("a"): govArticle("A", "status: deprecated", "replaced_by: article:b"),
			localFile("b"): govArticle("B", "status: deprecated", "replaced_by: article:c"),
			localFile("c"): article("C"),
		},
	} {
		t.Run(name, func(t *testing.T) { loadLocal(t, files) })
	}
}

func TestDanglingLocalReferenceFailsTheLoad(t *testing.T) {
	for name, tt := range map[string]struct {
		files map[string]string
		name  string
	}{
		"lookup of nothing": {map[string]string{
			localFile("guide"): docWith("Guide", fence("goldie lookup gone")),
		}, localFile("guide")},
		"replaced_by nothing": {map[string]string{
			localFile("old"): govArticle("Old", "replaced_by: article:gone"),
		}, localFile("old")},
		"lookup of a local draft": {map[string]string{
			localFile("wip"):   govArticle("Wip", "status: draft"),
			localFile("guide"): docWith("Guide", fence("goldie lookup wip")),
		}, localFile("guide")},
		"replaced_by a local draft": {map[string]string{
			localFile("wip"): govArticle("Wip", "status: draft"),
			localFile("old"): govArticle("Old", "replaced_by: article:wip"),
		}, localFile("old")},
		"replaced_by itself": {map[string]string{
			localFile("old"): govArticle("Old", "replaced_by: article:OLD"),
		}, localFile("old")},
		"replaced_by cycle": {map[string]string{
			localFile("a"): govArticle("A", "replaced_by: article:b"),
			localFile("b"): govArticle("B", "replaced_by: article:a"),
		}, ".goldie/articles/"},
		"lookup of another Project's Item": {map[string]string{
			otherProjectFile("theirs"): article("Theirs"),
			localFile("guide"):         docWith("Guide", fence("goldie lookup theirs")),
		}, localFile("guide")},
	} {
		t.Run(name, func(t *testing.T) { loadLocalErr(t, tt.files, tt.name) })
	}
}

// A reference from a local Item to a global Item the local config.yaml
// suppresses fails the load, naming the article and config.yaml, as #377 does
// for project.yaml.
func TestLocalReferenceToAnItemTheLocalConfigSuppressesFailsTheLoad(t *testing.T) {
	for name, tt := range map[string]struct{ file, content string }{
		"lookup":         {localFile("guide"), docWith("Guide", fence("goldie lookup platform/deploy"))},
		"inline lookup":  {localFile("guide"), docWith("Guide", "Read `goldie lookup Platform/DEPLOY` first.")},
		"replaced_by":    {localFile("old"), govArticle("Old", "status: deprecated", "replaced_by: article:platform/deploy")},
		"nested referer": {localFile("a/b/guide"), docWith("Guide", fence("goldie lookup PLATFORM/deploy"))},
	} {
		t.Run(name, func(t *testing.T) {
			loadLocalErr(t, map[string]string{
				globalFile("platform/deploy"): article("Deploy"),
				globalFile("kept"):            article("Kept"),
				localConfigFile:               suppressYAML("article:platform/deploy"),
				tt.file:                       tt.content,
			}, tt.file, localConfigFile)
		})
	}
	// A local draft copy neither shadows nor stops the suppression.
	loadLocalErr(t, map[string]string{
		globalFile("platform/deploy"): article("Deploy"),
		localFile("platform/deploy"):  govArticle("Draft deploy", "status: draft"),
		localConfigFile:               suppressYAML("article:platform/deploy"),
		localFile("guide"):            docWith("Guide", fence("goldie lookup platform/deploy")),
	}, localFile("guide"), localConfigFile)
}

// A global or Project Item's references resolve in its own view, which holds
// no local Item (ADR 0021): naming one fails, and a local copy changes nothing
// for them.
func TestWiderReferencesDoNotSeeLocalItems(t *testing.T) {
	for name, tt := range map[string]struct{ file, content string }{
		"global lookup":       {globalFile("guide"), docWith("Guide", fence("goldie lookup only-local"))},
		"global replaced_by":  {globalFile("old"), govArticle("Old", "replaced_by: article:only-local")},
		"project lookup":      {projectFile("guide"), docWith("Guide", fence("goldie lookup only-local"))},
		"project replaced_by": {projectFile("old"), govArticle("Old", "replaced_by: article:only-local")},
	} {
		t.Run(name, func(t *testing.T) {
			loadLocalErr(t, map[string]string{
				localFile("only-local"): article("Local only"),
				tt.file:                 tt.content,
			}, tt.file)
		})
	}
	// Local copies, drafts and a local suppression leave the wider references
	// alone: they still resolve to the global Items.
	snap := loadLocal(t, map[string]string{
		globalFile("style"):  article("Global style"),
		globalFile("deploy"): article("Deploy"),
		localFile("style"):   govArticle("Local style draft", "status: draft"),
		localConfigFile:      suppressYAML("article:deploy"),
		globalFile("old"):    govArticle("Old", "status: deprecated", "replaced_by: article:style"),
		globalFile("guide"):  docWith("Guide", fence("goldie lookup style", "goldie lookup deploy")),
		projectFile("old"):   govArticle("Project old", "status: deprecated", "replaced_by: article:style"),
	})
	if got := answer(snap, "", "old"); got != engine.ScopeGlobal+"/Old" {
		t.Errorf("ArticleIn(old) answered %s; want the global Item", got)
	}
}
