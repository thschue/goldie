package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Acceptance tests for #609 (#17 slice 4): goldie pull materialises loops and
// prints the /loop invocation. They run at the HTTP contract seam
// (GET /api/v1/bundle) and the CLI boundary (goldie pull), over the loop
// fixture of loops_api_test.go (loopFixture: the global loops nightly, every
// 30m with agent reviewer and skill deploy, and watcher, dynamic with skill
// deploy; the Project github.com/acme/shop with its own reviewer and the loop
// shop-release) and the pull helpers of pull_test.go. They use only the
// bundle endpoint, which is on main; nothing here needs `goldie loops`.
//
// The contract they fix, beyond the issue:
//   - a loop entry skill is .claude/skills/goldie-loop-<name>/SKILL.md, one
//     per loop of the Project's view (a draft is left out). Its frontmatter
//     names it goldie-loop-<name> and carries the loop's description; its body
//     holds the iteration prompt and the stop condition;
//   - the loop's closure is in the pulled namespace as the view resolves it
//     (the Project's reviewer for shop-release, the global one for nightly);
//   - the loop entry skills are in the bundle, so the ETag changes when a loop
//     is added, removed or its file changes, and a pull after such a change
//     rewrites the entry skill;
//   - pull prints one line per loop, `/loop <interval> /goldie-loop-<name>`,
//     sorted by name; a dynamic loop has no interval, so its line is
//     `/loop /goldie-loop-<name>`. The line is printed by a pull that wrote
//     files; what an already-up-to-date pull prints is not fixed here;
//   - loop text from a Source never carries hidden or control characters into
//     the written file or the output.

const (
	pNightlyEntry = ".claude/skills/goldie-loop-nightly/SKILL.md"
	pWatcherEntry = ".claude/skills/goldie-loop-watcher/SKILL.md"
	pShopEntry    = ".claude/skills/goldie-loop-shop-release/SKILL.md"
)

const nightlyAgents = "reviewer"

func nightlyFile(interval, stop, prompt string) string {
	f := loopFile("nightly", interval, []string{nightlyAgents}, []string{"deploy"}, stop)
	return strings.Replace(f, "Iterate nightly.", prompt, 1)
}

// pulledLoopsRepo pulls content into a new checkout, for the Project when one
// is given and globally otherwise.
func pulledLoopsRepo(t *testing.T, content string, project ...string) (repo, out string) {
	t.Helper()
	repo = gitCheckout(t, nil)
	var args []string
	if len(project) > 0 {
		args = []string{"--project", project[0]}
	}
	out, err := pullCLI(t, loopServer(t, content).URL, repo, args...)
	if err != nil {
		t.Fatalf("goldie pull: %v\n%s", err, out)
	}
	return repo, out
}

func readPulled(t *testing.T, repo, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("the pull did not write %s: %v", rel, err)
	}
	return string(b)
}

func hasPulled(repo, rel string) bool {
	_, err := os.Stat(filepath.Join(repo, filepath.FromSlash(rel)))
	return err == nil
}

// loopLines counts the output lines that start with /loop.
func loopLines(out string) map[string]int {
	m := map[string]int{}
	for l := range strings.SplitSeq(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "/loop") {
			m[l]++
		}
	}
	return m
}

// Criterion: pull writes goldie-loop-<name> for each loop of the view.
func TestPullWritesALoopEntrySkillForEachLoop(t *testing.T) {
	repo, _ := pulledLoopsRepo(t, loopContent(t, nil), loopsShop)
	for rel, name := range map[string]string{pNightlyEntry: "nightly", pWatcherEntry: "watcher", pShopEntry: "shop-release"} {
		got := readPulled(t, repo, rel)
		for _, want := range []string{"name: goldie-loop-" + name, "Loop " + name, "Iterate " + name + "."} {
			if !strings.Contains(got, want) {
				t.Errorf("%s lacks %q:\n%s", rel, want, got)
			}
		}
		if !strings.HasPrefix(got, "---\n") {
			t.Errorf("%s does not start with frontmatter:\n%s", rel, got)
		}
	}
	if got := readPulled(t, repo, pNightlyEntry); !strings.Contains(got, "when CI is green") {
		t.Errorf("the nightly entry skill does not carry the stop condition:\n%s", got)
	}

	// Without a Project the view is global: the Project's loop is not there.
	global, _ := pulledLoopsRepo(t, loopContent(t, nil))
	readPulled(t, global, pNightlyEntry)
	if hasPulled(global, pShopEntry) {
		t.Errorf("a global pull wrote the Project loop %s", pShopEntry)
	}

	// A draft loop is not pulled.
	draft := strings.Replace(loopFile("watcher", "dynamic", nil, []string{"deploy"}, "when the queue is empty"),
		"stop:", "status: draft\nstop:", 1)
	repo2, _ := pulledLoopsRepo(t, loopContent(t, map[string]string{"global/loops/watcher.md": draft}))
	if hasPulled(repo2, pWatcherEntry) {
		t.Errorf("a draft loop was pulled as %s", pWatcherEntry)
	}
}

// Criterion: the agents and skills in a loop's closure are written with it, as
// the loop's view resolves them.
func TestPullWritesTheClosureOfEachLoop(t *testing.T) {
	repo, _ := pulledLoopsRepo(t, loopContent(t, nil), loopsShop)
	snap := namespaceFiles(snapshot(t, repo))
	for p, want := range map[string]string{
		".claude/agents/goldie-reviewer.md":                "shop reviewer",
		".claude/agents/goldie-planner.md":                 "global planner",
		".claude/skills/goldie-deploy/SKILL.md":            "# Deploy\n",
		".claude/skills/goldie-deploy/checklist.txt":       "check it\n",
		".claude/skills/goldie-loop-shop-release/SKILL.md": "Iterate shop-release.",
	} {
		got, ok := snap[p]
		if !ok {
			t.Errorf("the closure file %s was not written", p)
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to hold %q", p, got, want)
		}
	}

	// In a global view the closure of nightly is the global reviewer.
	global, _ := pulledLoopsRepo(t, loopContent(t, nil))
	if got := readPulled(t, global, ".claude/agents/goldie-reviewer.md"); !strings.Contains(got, "global reviewer") {
		t.Errorf("the global pull wrote the reviewer %q, want the global one", got)
	}
	readPulled(t, global, pNightlyEntry)
}

// Criterion: the bundle layout includes the loop entry skills.
func TestBundleHoldsTheLoopEntrySkills(t *testing.T) {
	srv := loopServer(t, loopContent(t, nil))
	resp, body := bundleGet(t, srv, loopsShop, "claude", nil)
	wantBundleOK(t, resp, body)
	files := bundleContents(readBundle(t, body))
	for _, p := range []string{pNightlyEntry, pWatcherEntry, pShopEntry} {
		if !strings.Contains(files[p], "goldie-loop-") {
			t.Errorf("the bundle has no loop entry skill %s: %v", p, files.paths())
		}
	}

	resp, body = bundleGet(t, srv, "", "claude", nil)
	wantBundleOK(t, resp, body)
	global := bundleContents(readBundle(t, body))
	if _, ok := global[pShopEntry]; ok {
		t.Errorf("the global bundle holds the Project loop %s", pShopEntry)
	}
	if _, ok := global[pNightlyEntry]; !ok {
		t.Errorf("the global bundle lacks %s", pNightlyEntry)
	}
}

// Criterion: the bundle digest includes the loop entry skills.
func TestBundleDigestChangesWithTheLoops(t *testing.T) {
	etag := func(edits map[string]string) string {
		resp, body := bundleGet(t, loopServer(t, loopContent(t, edits)), loopsShop, "claude", nil)
		wantBundleOK(t, resp, body)
		e := resp.Header.Get("ETag")
		if e == "" {
			t.Fatal("the bundle has no ETag")
		}
		return e
	}
	base := etag(nil)
	if again := etag(nil); again != base {
		t.Errorf("the same set has two digests: %s and %s", base, again)
	}
	for name, edits := range map[string]map[string]string{
		"a loop removed":             {"global/loops/watcher.md": ""},
		"a loop prompt":              {loopNightly: nightlyFile("30m", "when CI is green", "Iterate harder.")},
		"a loop stop":                {loopNightly: nightlyFile("30m", "when nothing is left", "Iterate nightly.")},
		"a loop interval":            {loopNightly: nightlyFile("45m", "when CI is green", "Iterate nightly.")},
		"a loop that became dynamic": {loopNightly: nightlyFile("dynamic", "when CI is green", "Iterate nightly.")},
	} {
		if got := etag(edits); got == base {
			t.Errorf("the digest did not change for %s", name)
		}
	}

	// The digest is the one a client sends back: an unchanged set is a 304.
	srv := loopServer(t, loopContent(t, nil))
	resp, _ := bundleGet(t, srv, loopsShop, "claude", nil)
	resp2, body := bundleGet(t, srv, loopsShop, "claude", func(r *http.Request) { r.Header.Set("If-None-Match", resp.Header.Get("ETag")) })
	if resp2.StatusCode != http.StatusNotModified || len(body) != 0 {
		t.Errorf("If-None-Match with the digest: status %d, %d body bytes, want 304 and none", resp2.StatusCode, len(body))
	}
}

// Criterion: a changed loop reaches the project on the next pull, and an
// unchanged set rewrites nothing.
func TestPullRewritesTheEntrySkillWhenTheLoopChanges(t *testing.T) {
	content := loopContent(t, nil)
	repo, _ := pulledLoopsRepo(t, content, loopsShop)
	before := snapshot(t, repo)
	out, err := pullCLI(t, loopServer(t, content).URL, repo, "--project", loopsShop)
	if err != nil {
		t.Fatalf("second pull: %v\n%s", err, out)
	}
	if after := snapshot(t, repo); !mapsEqual(before, after) {
		t.Errorf("a pull of an unchanged set changed the project")
	}

	writeRaw(t, content, loopNightly, nightlyFile("30m", "when CI is green", "Iterate harder."))
	srv := loopServer(t, content)
	if out, err := pullCLI(t, srv.URL, repo, "--project", loopsShop, "--check"); err == nil {
		t.Errorf("pull --check passed while a loop changed:\n%s", out)
	}
	if out, err := pullCLI(t, srv.URL, repo, "--project", loopsShop); err != nil {
		t.Fatalf("pull after the change: %v\n%s", err, out)
	}
	if got := readPulled(t, repo, pNightlyEntry); !strings.Contains(got, "Iterate harder.") {
		t.Errorf("the entry skill was not rewritten:\n%s", got)
	}
}

// Criterion: pull prints /loop <interval> /goldie-loop-<name> for each loop.
func TestPullPrintsTheLoopInvocationForEachLoop(t *testing.T) {
	_, out := pulledLoopsRepo(t, loopContent(t, nil), loopsShop)
	got := loopLines(out)
	want := []string{"/loop 30m /goldie-loop-nightly", "/loop /goldie-loop-watcher", "/loop 1h /goldie-loop-shop-release"}
	for _, w := range want {
		if got[w] != 1 {
			t.Errorf("pull prints %q %d times, want once:\n%s", w, got[w], out)
		}
	}
	if len(got) != len(want) {
		t.Errorf("pull prints the /loop lines %v, want exactly %v", got, want)
	}
	n, s, w := strings.Index(out, "goldie-loop-nightly"), strings.Index(out, "goldie-loop-shop-release"), strings.Index(out, "goldie-loop-watcher")
	if n < 0 || s < 0 || w < 0 || n > s || s > w {
		t.Errorf("the /loop lines are not sorted by name:\n%s", out)
	}

	// No Project, no Project loop.
	_, global := pulledLoopsRepo(t, loopContent(t, nil))
	if strings.Contains(global, "shop-release") {
		t.Errorf("a global pull prints the Project loop:\n%s", global)
	}

	// No loops, no /loop line.
	none := loopContent(t, map[string]string{
		loopNightly: "", "global/loops/watcher.md": "",
		"projects/" + loopsShop + "/loops/shop-release.md": "",
	})
	if _, out := pulledLoopsRepo(t, none, loopsShop); strings.Contains(out, "/loop") {
		t.Errorf("a pull without loops prints a /loop line:\n%s", out)
	}
}

// Security: text of a loop is untrusted; neither the written entry skill nor
// the output carries a hidden or control character.
func TestPullCleansLoopTextBeforeWritingAndPrinting(t *testing.T) {
	hostile := "---\nname: nightly\ndescription: \"Nightly \\u202ereversed \\u200bzero \\u001b[31mred\"\n" +
		"interval: 30m\nagents: []\nskills: []\nstop: when CI is green\n---\nIterate nightly.\n"
	repo, out := pulledLoopsRepo(t, loopContent(t, map[string]string{loopNightly: hostile}), loopsShop)
	got := readPulled(t, repo, pNightlyEntry)
	for _, s := range []string{got, out} {
		for _, r := range s {
			if r == '\u202e' || r == '\u200b' || r == 0x1b {
				t.Errorf("hidden or control character %U reached the output or the entry skill:\n%q", r, s)
				break
			}
		}
	}
	if !strings.Contains(got, "Nightly") {
		t.Errorf("the cleaned description is gone from the entry skill:\n%s", got)
	}
}
