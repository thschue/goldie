package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #17 (Loops), slice 1: the engine and the HTTP contract
// seam (list and get), through the public handler PublicHandler builds. Every
// response is checked against the served spec by do. The pattern followed is
// skills_agents_api_test.go (itemsGet, tokens, the spec check) and
// governance_test.go (writeRaw, a load that fails keeps the last good
// Snapshot).
//
// The contract they fix, beyond the issue:
//   - a loop is the file loops/<name>.md in each Scope (global/loops,
//     projects/<host>/<org>/<repo>/loops), with frontmatter name (equal to the
//     file name), description, exactly one of interval (a string such as
//     "30m") or dynamic: true, agents and skills (lists of names) and stop (a
//     sentence); the body is the iteration prompt;
//   - a name in agents or skills resolves in the loop's own view: a Project
//     loop sees its Project's agents and skills, then global; a global loop
//     sees global only. A name that resolves nowhere fails the load as every
//     other dangling reference does: a fresh start refuses (the error names
//     the loop file relative to the content directory, the missing name, and
//     never the content path) and a running server keeps its last good
//     Snapshot;
//   - GET /api/v1/loops answers {"loops": [...], "hint"?} sorted by name, one
//     entry per name (the nearest Scope wins), and GET /api/v1/loops/{name}
//     answers one entry. Both honour project= and drafts= as for agents;
//   - an entry has name, description, interval (omitted for a dynamic loop),
//     dynamic (bool), stop, closure and digest. The closure is the resolved
//     dependencies: closure.agents and closure.skills, each a list of
//     {name, scope} sorted by name, where scope is the Scope that answered in
//     the loop's view. A get also has content, the body. The digest is a
//     64-character hex string, the same in the list and the get;
//   - the digest covers the loop file and the bytes of every agent file and
//     every skill file in the closure, as resolved in the view asked for: it
//     changes when any of them changes and not when an unrelated Item does.
//
// Not fixed here: what a loop with both or neither of interval and dynamic
// does, and which interval strings are valid; the issue does not say.

const (
	loopsAPI     = "/api/v1/loops"
	loopsShop    = "github.com/acme/shop"
	loopsOther   = "github.com/acme/other"
	loopNightly  = "global/loops/nightly.md"
	loopAgentRev = "global/agents/reviewer.md"
)

type loopRef struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

type loopEntry struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Interval    *string `json:"interval"`
	Dynamic     bool    `json:"dynamic"`
	Stop        string  `json:"stop"`
	Scope       *string `json:"scope"`
	Content     string  `json:"content"`
	Digest      string  `json:"digest"`
	Closure     struct {
		Agents []loopRef `json:"agents"`
		Skills []loopRef `json:"skills"`
	} `json:"closure"`
}

func loopFile(name, interval string, agents, skills []string, stop string) string {
	timing := "interval: " + interval
	if interval == "dynamic" {
		timing = "dynamic: true"
	}
	return "---\nname: " + name + "\ndescription: Loop " + name + "\n" + timing +
		"\nagents: [" + strings.Join(agents, ", ") + "]\nskills: [" + strings.Join(skills, ", ") +
		"]\nstop: " + stop + "\n---\nIterate " + name + ".\n"
}

func loopAgent(name, body string) string {
	return "---\nname: " + name + "\ndescription: Agent " + name + "\n---\n" + body + "\n"
}

// loopFixture is the base content: global agents reviewer and planner, the
// global skill deploy (two files), the global loops nightly (every 30 minutes,
// reviewer and deploy) and watcher (dynamic, deploy only), and the Project
// github.com/acme/shop with its own reviewer and the loop shop-release.
func loopFixture() map[string]string {
	return map[string]string{
		loopAgentRev:                                    loopAgent("reviewer", "global reviewer"),
		"global/agents/planner.md":                      loopAgent("planner", "global planner"),
		"global/skills/deploy/SKILL.md":                 "# Deploy\n",
		"global/skills/deploy/checklist.txt":            "check it\n",
		loopNightly:                                     loopFile("nightly", "30m", []string{"reviewer"}, []string{"deploy"}, "when CI is green"),
		"global/loops/watcher.md":                       loopFile("watcher", "dynamic", nil, []string{"deploy"}, "when the queue is empty"),
		"projects/" + loopsShop + "/agents/reviewer.md": loopAgent("reviewer", "shop reviewer"),
		"projects/" + loopsShop + "/loops/shop-release.md": loopFile("shop-release", "1h",
			[]string{"reviewer", "planner"}, []string{"deploy"}, "when released"),
	}
}

// loopContent writes the fixture into a new content directory, after applying
// edits (a "" value removes the file).
func loopContent(t *testing.T, edits map[string]string) string {
	t.Helper()
	files := loopFixture()
	for k, v := range edits {
		if v == "" {
			delete(files, k)
		} else {
			files[k] = v
		}
	}
	dir := t.TempDir()
	for name, content := range files {
		writeRaw(t, dir, name, content)
	}
	return dir
}

func loopServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: dir, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)
	return srv
}

func getLoop(t *testing.T, srv *httptest.Server, token, path string) loopEntry {
	t.Helper()
	status, body := itemsGet(t, srv, token, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, status, body)
	}
	var e loopEntry
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, body)
	}
	return e
}

func listLoops(t *testing.T, srv *httptest.Server, token, path string) []loopEntry {
	t.Helper()
	status, body := itemsGet(t, srv, token, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, status, body)
	}
	var out struct {
		Loops *[]loopEntry `json:"loops"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Loops == nil {
		t.Fatalf("GET %s: no loops list (%v): %s", path, err, body)
	}
	return *out.Loops
}

func loopNames(es []loopEntry) []string {
	names := []string{}
	for _, e := range es {
		names = append(names, e.Name)
	}
	return names
}

func loopRefs(rs []loopRef) []loopRef {
	if rs == nil {
		return []loopRef{}
	}
	return rs
}

// loopDigest serves dir and returns the digest of one loop in a view.
func loopDigest(t *testing.T, dir, project, name string) string {
	t.Helper()
	srv := loopServer(t, dir)
	path := loopsAPI + "/" + name
	if project != "" {
		path += "?project=" + project
	}
	return getLoop(t, srv, operatorToken, path).Digest
}

var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Criterion: the served spec declares the two operations.
func TestServedSpecDeclaresLoopOperations(t *testing.T) {
	ops := specOperations(t)
	for _, want := range []string{"GET /api/v1/loops", "GET /api/v1/loops/{name}"} {
		if !slices.Contains(ops, want) {
			t.Errorf("the spec declares no %s; it declares %v", want, ops)
		}
	}
}

// Criterion: API loops list, with the resolved closure.
func TestListLoopsCarriesEachLoopWithItsResolvedClosure(t *testing.T) {
	srv := loopServer(t, loopContent(t, nil))
	loops := listLoops(t, srv, readerToken, loopsAPI)
	if got := loopNames(loops); !slices.Equal(got, []string{"nightly", "watcher"}) {
		t.Fatalf("global view lists %v, want [nightly watcher] and not the Project's loop", got)
	}
	nightly, watcher := loops[0], loops[1]
	if nightly.Description != "Loop nightly" || nightly.Interval == nil || *nightly.Interval != "30m" ||
		nightly.Dynamic || nightly.Stop != "when CI is green" {
		t.Errorf("nightly = %+v", nightly)
	}
	if !slices.Equal(loopRefs(nightly.Closure.Agents), []loopRef{{"reviewer", "global"}}) ||
		!slices.Equal(loopRefs(nightly.Closure.Skills), []loopRef{{"deploy", "global"}}) {
		t.Errorf("nightly closure = %+v, want agent reviewer and skill deploy, both global", nightly.Closure)
	}
	if !watcher.Dynamic || watcher.Interval != nil || watcher.Stop != "when the queue is empty" {
		t.Errorf("watcher = %+v, want dynamic with no interval", watcher)
	}
	if len(loopRefs(watcher.Closure.Agents)) != 0 || !slices.Equal(loopRefs(watcher.Closure.Skills), []loopRef{{"deploy", "global"}}) {
		t.Errorf("watcher closure = %+v, want no agents and skill deploy", watcher.Closure)
	}
	for _, l := range loops {
		if !hexDigest.MatchString(l.Digest) {
			t.Errorf("%s digest %q is not 64 hex characters", l.Name, l.Digest)
		}
	}
	if nightly.Digest == watcher.Digest {
		t.Error("two different loops share a digest")
	}
}

// Criterion: API loops get, with the resolved closure.
func TestGetLoopCarriesItsBodyClosureAndTheDigestOfTheList(t *testing.T) {
	srv := loopServer(t, loopContent(t, nil))
	listed := listLoops(t, srv, readerToken, loopsAPI)[0]
	got := getLoop(t, srv, readerToken, loopsAPI+"/nightly")
	if got.Name != "nightly" || got.Content != "Iterate nightly.\n" {
		t.Errorf("get = %+v, want name nightly and its body as content", got)
	}
	if got.Digest != listed.Digest || !hexDigest.MatchString(got.Digest) {
		t.Errorf("get digest %q, list digest %q: want the same, 64 hex characters", got.Digest, listed.Digest)
	}
	if !slices.Equal(loopRefs(got.Closure.Agents), []loopRef{{"reviewer", "global"}}) ||
		!slices.Equal(loopRefs(got.Closure.Skills), []loopRef{{"deploy", "global"}}) {
		t.Errorf("closure = %+v", got.Closure)
	}
}

// Criterion: references resolve in the same Project's view.
func TestLoopClosureResolvesInTheProjectView(t *testing.T) {
	srv := loopServer(t, loopContent(t, nil))
	q := "?project=" + loopsShop

	if got := loopNames(listLoops(t, srv, readerToken, loopsAPI+q)); !slices.Equal(got, []string{"nightly", "shop-release", "watcher"}) {
		t.Errorf("shop view lists %v", got)
	}
	nightly := getLoop(t, srv, readerToken, loopsAPI+"/nightly"+q)
	if !slices.Equal(loopRefs(nightly.Closure.Agents), []loopRef{{"reviewer", loopsShop}}) {
		t.Errorf("nightly in the shop view: agents %+v, want the Project's reviewer", nightly.Closure.Agents)
	}
	release := getLoop(t, srv, readerToken, loopsAPI+"/shop-release"+q)
	if !slices.Equal(loopRefs(release.Closure.Agents), []loopRef{{"planner", "global"}, {"reviewer", loopsShop}}) {
		t.Errorf("shop-release agents %+v, want global planner and the Project's reviewer", release.Closure.Agents)
	}

	status, body := itemsGet(t, srv, readerToken, loopsAPI+"/shop-release")
	if status != http.StatusNotFound {
		t.Errorf("a Project loop in the global view: status %d, want 404: %s", status, body)
	}
}

// Criterion: dangling references are rejected. A fresh start refuses.
func TestDanglingLoopReferencesFailTheLoad(t *testing.T) {
	shop := "projects/" + loopsShop + "/"
	cases := map[string]struct {
		edits   map[string]string
		file    string
		missing string
	}{
		"agent missing": {
			map[string]string{loopNightly: loopFile("nightly", "30m", []string{"ghost"}, []string{"deploy"}, "x")},
			loopNightly, "ghost"},
		"skill missing": {
			map[string]string{loopNightly: loopFile("nightly", "30m", []string{"reviewer"}, []string{"phantom"}, "x")},
			loopNightly, "phantom"},
		"global loop names an agent only a Project has": {
			map[string]string{
				shop + "agents/shop-only.md": loopAgent("shop-only", "x"),
				loopNightly:                  loopFile("nightly", "30m", []string{"shop-only"}, nil, "x"),
			}, loopNightly, "shop-only"},
		"Project loop names another Project's skill": {
			map[string]string{
				"projects/" + loopsOther + "/skills/other-skill/SKILL.md": "# Other\n",
				shop + "loops/shop-release.md":                            loopFile("shop-release", "1h", nil, []string{"other-skill"}, "x"),
			}, shop + "loops/shop-release.md", "other-skill"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := loopContent(t, tc.edits)
			_, err := PublicHandler(activityConfig(platform.Config{ContentDir: dir, RateLimit: roomyRateLimit}), nil)
			if err == nil {
				t.Fatal("PublicHandler accepted a loop with a dangling reference")
			}
			if !strings.Contains(err.Error(), tc.file) || !strings.Contains(err.Error(), tc.missing) || strings.Contains(err.Error(), dir) {
				t.Errorf("error = %q, want it to name %s and %s, relative to the content directory", err, tc.file, tc.missing)
			}
		})
	}
}

// Criterion: dangling references are rejected. A running server keeps its
// last good Snapshot, loop and closure included.
func TestServerKeepsTheLastGoodLoopsWhenAReferenceDangles(t *testing.T) {
	dir := loopContent(t, nil)
	syncer := engine.NewSyncer(engine.NewDirSource(dir))
	if err := syncer.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	srv := providerServer(t, syncer.Current)
	before := getLoop(t, srv, testToken, loopsAPI+"/nightly")

	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(loopAgentRev))); err != nil {
		t.Fatal(err)
	}
	if err := syncer.Poll(t.Context()); err == nil {
		t.Fatal("the load succeeded after the agent a loop needs was removed")
	}
	after := getLoop(t, srv, testToken, loopsAPI+"/nightly")
	if after.Digest != before.Digest || !slices.Equal(loopRefs(after.Closure.Agents), []loopRef{{"reviewer", "global"}}) {
		t.Errorf("after the broken load: %+v, want the last good answer %+v", after, before)
	}
}

// Criterion: the closure digest changes when a dependency changes.
func TestLoopDigestChangesWhenADependencyChanges(t *testing.T) {
	base := loopDigest(t, loopContent(t, nil), "", "nightly")
	if again := loopDigest(t, loopContent(t, nil), "", "nightly"); again != base {
		t.Fatalf("the same content gives digests %s and %s", base, again)
	}
	for name, edits := range map[string]map[string]string{
		"the agent's file":       {loopAgentRev: loopAgent("reviewer", "a different reviewer")},
		"a skill's SKILL.md":     {"global/skills/deploy/SKILL.md": "# Deploy v2\n"},
		"a skill's other file":   {"global/skills/deploy/checklist.txt": "check it twice\n"},
		"the loop's stop":        {loopNightly: loopFile("nightly", "30m", []string{"reviewer"}, []string{"deploy"}, "when CI is red")},
		"the loop's interval":    {loopNightly: loopFile("nightly", "45m", []string{"reviewer"}, []string{"deploy"}, "when CI is green")},
		"the loop's closure set": {loopNightly: loopFile("nightly", "30m", []string{"reviewer", "planner"}, []string{"deploy"}, "when CI is green")},
	} {
		t.Run(name, func(t *testing.T) {
			if got := loopDigest(t, loopContent(t, edits), "", "nightly"); got == base {
				t.Errorf("changing %s leaves the digest %s", name, base)
			}
		})
	}
}

// Criterion: the digest covers the dependencies and only them, in the view
// asked for.
func TestLoopDigestIgnoresUnrelatedItemsAndFollowsTheView(t *testing.T) {
	base := loopDigest(t, loopContent(t, nil), "", "nightly")
	unrelated := map[string]string{
		"global/agents/planner.md":          loopAgent("planner", "a different planner"),
		"global/loops/watcher.md":           loopFile("watcher", "5m", nil, []string{"deploy"}, "else"),
		"global/agents/extra.md":            loopAgent("extra", "new"),
		"global/skills/other/SKILL.md":      "# Other\n",
		"global/articles/unrelated-note.md": "---\nname: Note\n---\nnote\n",
	}
	for name, content := range unrelated {
		t.Run(name, func(t *testing.T) {
			if got := loopDigest(t, loopContent(t, map[string]string{name: content}), "", "nightly"); got != base {
				t.Errorf("changing %s moved the digest of nightly from %s to %s", name, base, got)
			}
		})
	}
	if got := loopDigest(t, loopContent(t, nil), loopsShop, "nightly"); got == base {
		t.Error("nightly has the same digest in the shop view, where its reviewer is the Project's, as in the global view")
	}
	shopFile := "projects/" + loopsShop + "/agents/reviewer.md"
	edited := loopContent(t, map[string]string{shopFile: loopAgent("reviewer", "shop reviewer v2")})
	if got := loopDigest(t, edited, "", "nightly"); got != base {
		t.Error("a change to the Project's reviewer moved the digest of nightly in the global view")
	}
	shop := loopDigest(t, loopContent(t, nil), loopsShop, "nightly")
	if got := loopDigest(t, edited, loopsShop, "nightly"); got == shop {
		t.Error("a change to the Project's reviewer left the digest of nightly in the shop view")
	}
}

// Criterion: drafts, misses, bad input and the token behave as for agents.
func TestLoopOperationsFollowTheRulesOfAgents(t *testing.T) {
	draft := "---\nname: wip-loop\ndescription: Loop wip-loop\ninterval: 5m\nagents: []\nskills: []\nstop: never\nstatus: draft\n---\nwip-marker\n"
	srv := loopServer(t, loopContent(t, map[string]string{"global/loops/wip-loop.md": draft}))

	if got := loopNames(listLoops(t, srv, operatorToken, loopsAPI)); slices.Contains(got, "wip-loop") {
		t.Errorf("an operator without drafts sees %v", got)
	}
	if status, body := itemsGet(t, srv, operatorToken, loopsAPI+"/wip-loop"); status != http.StatusNotFound {
		t.Errorf("a draft loop without drafts: status %d, want 404: %s", status, body)
	}
	if got := loopNames(listLoops(t, srv, operatorToken, loopsAPI+"?drafts=true")); !slices.Contains(got, "wip-loop") {
		t.Errorf("an operator with drafts=true sees %v", got)
	}
	for _, p := range []string{loopsAPI + "?drafts=true", loopsAPI + "/wip-loop?drafts=true"} {
		status, body := itemsGet(t, srv, readerToken, p)
		if status != http.StatusForbidden || strings.Contains(string(body), "wip-marker") {
			t.Errorf("reader GET %s: status %d, want 403 without the draft: %s", p, status, body)
		}
	}

	status, body := itemsGet(t, srv, readerToken, loopsAPI+"/nope")
	if status != http.StatusNotFound {
		t.Fatalf("a missing loop: status %d, want 404: %s", status, body)
	}
	if code, hint := errorHint(t, body); code != "not_found" || hint == "" || strings.Contains(string(body), "nope") {
		t.Errorf("a missing loop: code %q hint %q body %s, want not_found with a hint that echoes nothing", code, hint, body)
	}
	for _, p := range []string{loopsAPI + "?project=..%2Fetc", loopsAPI + "/nightly?project=..%2Fetc"} {
		if status, body := itemsGet(t, srv, readerToken, p); status != http.StatusBadRequest {
			t.Errorf("GET %s: status %d, want 400: %s", p, status, body)
		}
	}
	for _, p := range []string{loopsAPI, loopsAPI + "/nightly"} {
		if status, body := itemsGet(t, srv, "", p); status != http.StatusUnauthorized {
			t.Errorf("GET %s without a token: status %d, want 401: %s", p, status, body)
		}
	}
}

// #614: the Loop payload names the Scope of the copy it serves, as Article has
// since #577 and SearchHit always did.

func wantLoopScope(t *testing.T, what string, got loopEntry, want string) {
	t.Helper()
	if got.Scope == nil {
		t.Errorf("%s: scope is absent, want %q", what, want)
	} else if *got.Scope != want {
		t.Errorf("%s: scope = %q, want %q", what, *got.Scope, want)
	}
}

// Criterion: the spec declares Loop.scope, a string, optional, no enum.
func TestServedSpecDeclaresAnOptionalScopeOnLoop(t *testing.T) {
	doc := specDoc(t)
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	loop, _ := schemas["Loop"].(map[string]any)
	props, _ := loop["properties"].(map[string]any)
	p := contracttest.ResolveRef(doc, props["scope"])
	if p == nil {
		t.Fatal("Loop declares no scope property")
	}
	if p["type"] != "string" {
		t.Errorf("Loop.scope type %v, want string", p["type"])
	}
	if _, ok := p["enum"]; ok {
		t.Errorf("Loop.scope has an enum %v; the Scope is open (ADR 0011)", p["enum"])
	}
	if d, _ := p["description"].(string); strings.TrimSpace(d) == "" {
		t.Error("Loop.scope has no description")
	}
	req, _ := loop["required"].([]any)
	for _, r := range req {
		if r == "scope" {
			t.Error("Loop requires scope; the field is optional (additive, ADR 0011)")
		}
	}
}

// Criterion: list and get carry scope: global, or the Project when its copy wins.
func TestLoopsNameTheScopeOfTheCopyThatWins(t *testing.T) {
	shopNightly := loopFile("nightly", "10m", []string{"reviewer"}, []string{"deploy"}, "when shop is green")
	srv := loopServer(t, loopContent(t, map[string]string{
		"projects/" + loopsShop + "/loops/nightly.md": shopNightly,
	}))
	shop := "?project=" + loopsShop

	for _, e := range listLoops(t, srv, readerToken, loopsAPI) {
		wantLoopScope(t, "list "+e.Name+", no Project", e, "global")
	}
	wantLoopScope(t, "get nightly, no Project", getLoop(t, srv, readerToken, loopsAPI+"/nightly"), "global")

	byName := map[string]loopEntry{}
	for _, e := range listLoops(t, srv, readerToken, loopsAPI+shop) {
		byName[e.Name] = e
	}
	wantLoopScope(t, "list nightly, shop view", byName["nightly"], loopsShop)
	wantLoopScope(t, "list shop-release, shop view", byName["shop-release"], loopsShop)
	wantLoopScope(t, "list watcher, shop view", byName["watcher"], "global")
	wantLoopScope(t, "get nightly, shop view", getLoop(t, srv, readerToken, loopsAPI+"/nightly"+shop), loopsShop)
	wantLoopScope(t, "get shop-release, shop view", getLoop(t, srv, readerToken, loopsAPI+"/shop-release"+shop), loopsShop)
	wantLoopScope(t, "get watcher, shop view", getLoop(t, srv, readerToken, loopsAPI+"/watcher"+shop), "global")
	// Another Project's view must not show the shop copy's scope.
	wantLoopScope(t, "get nightly, other view", getLoop(t, srv, readerToken, loopsAPI+"/nightly?project="+loopsOther), "global")
}

// Criterion: a Source with replaced_by: loop:<new> loads and the loop is served.
func TestLoopReplacedByALoopLoadsOverHTTP(t *testing.T) {
	old := "---\nname: old\ndescription: Loop old\ninterval: 5m\nagents: []\nskills: []\nstop: x\nstatus: deprecated\nreplaced_by: loop:nightly\n---\nIterate old.\n"
	srv := loopServer(t, loopContent(t, map[string]string{"global/loops/old.md": old}))
	if got := loopNames(listLoops(t, srv, readerToken, loopsAPI)); !slices.Equal(got, []string{"nightly", "old", "watcher"}) {
		t.Errorf("list = %v, want the deprecated loop old served beside the rest", got)
	}
	if got := getLoop(t, srv, readerToken, loopsAPI+"/old"); got.Name != "old" {
		t.Errorf("get old = %+v", got)
	}

	// A replacement that does not exist fails a fresh start and names the file.
	dangling := strings.Replace(old, "loop:nightly", "loop:ghost", 1)
	dir := loopContent(t, map[string]string{"global/loops/old.md": dangling})
	if _, err := engine.NewDirSource(dir).Load(t.Context()); err == nil ||
		!strings.Contains(err.Error(), "global/loops/old.md") || strings.Contains(err.Error(), dir) {
		t.Errorf("replaced_by: loop:ghost: err = %v, want a load error naming global/loops/old.md and not the content path", err)
	}
}

// Criterion: a Project that suppresses a loop no longer lists it.
func TestProjectSuppressingALoopHidesItInThatProjectOnly(t *testing.T) {
	srv := loopServer(t, loopContent(t, map[string]string{
		"projects/" + loopsShop + "/project.yaml": "suppress: [loop:watcher]\n",
	}))
	shop := "?project=" + loopsShop
	if got := loopNames(listLoops(t, srv, readerToken, loopsAPI+shop)); !slices.Equal(got, []string{"nightly", "shop-release"}) {
		t.Errorf("shop view lists %v, want watcher suppressed", got)
	}
	if status, body := itemsGet(t, srv, readerToken, loopsAPI+"/watcher"+shop); status != http.StatusNotFound {
		t.Errorf("get a suppressed loop: status %d, want 404: %s", status, body)
	}
	if got := loopNames(listLoops(t, srv, readerToken, loopsAPI)); !slices.Equal(got, []string{"nightly", "watcher"}) {
		t.Errorf("global view lists %v, want watcher", got)
	}
	if got := loopNames(listLoops(t, srv, readerToken, loopsAPI+"?project="+loopsOther)); !slices.Contains(got, "watcher") {
		t.Errorf("another Project lists %v, want watcher", got)
	}
}
