package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	gmcp "github.com/thschue/goldie/internal/mcp"
)

// Acceptance tests for #607 (slice 2 of #17), the MCP protocol seam: the tools
// list_loops and get_loop and one prompt per loop, over the real public
// handler and the loop fixture of loops_api_test.go (loopFixture): the global
// loops nightly (every 30m, agent reviewer, skill deploy) and watcher
// (dynamic, skill deploy), the Project github.com/acme/shop with its own
// reviewer and the loop shop-release (1h, agents reviewer and planner, skill
// deploy). Pattern followed: mcp_skills_test.go and mcp_prompts_resources_test.go.
//
// The contract they fix, beyond the issue:
//   - the two tools are advertised next to the eight existing ones. list_loops
//     takes an optional project only; get_loop takes name and an optional
//     project. Like every tool they say that what they return from a Source is
//     data (checked for all tools in mcp_test.go);
//   - list_loops answers every loop of the Project's view, sorted by name, with
//     its description, its interval (or that it is dynamic) and its stop
//     sentence; no loop is not an error. get_loop answers the loop's text,
//     the same fields, the closure (agents and skills, each with the Scope
//     that answered) and the digest the API serves, so an agent can compare it
//     with a pulled copy;
//   - a miss is a tool error with a hint to list_loops, naming no address,
//     token or path;
//   - prompts/list names one prompt per loop, goldie-loop-<name>, beside the
//     prompts of the skills, in the session's Project view (the issue does not
//     name the prompt; this is the decision, and loopPrompt is the one place
//     that holds it). prompts/get returns the loop's iteration prompt (its
//     body) and the /loop invocation: "/loop <interval> <prompt>" for an
//     interval loop, "/loop <prompt>" for a dynamic one. It notes that
//     `goldie pull` is required when the closure is not empty, and says
//     nothing of a pull when it is;
//   - a prompt that is no loop of the session's view is a protocol error that
//     names no address, token or path.

func loopPrompt(name string) string { return "goldie-loop-" + name }

func connectLoops(t *testing.T, edits map[string]string, opts ...gmcp.Option) *mcp.ClientSession {
	t.Helper()
	return sessionFor(t, loopServer(t, loopContent(t, edits)).URL, readerToken, opts...)
}

func shopSession() gmcp.Option { return gmcp.WithProject(loopsShop, "test") }

// soloEdits add a loop with an empty closure: no agent and no skill.
var soloEdits = map[string]string{
	"global/loops/solo.md": loopFile("solo", "15m", nil, nil, "never"),
}

func TestMCPLoopToolsAreAdvertisedWithTheirInputSchemas(t *testing.T) {
	res, err := connectLoops(t, nil).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	type schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	want := map[string][]string{"list_loops": nil, "get_loop": {"name"}}
	seen := 0
	for _, tl := range res.Tools {
		required, ok := want[tl.Name]
		if !ok {
			continue
		}
		seen++
		if tl.Description == "" {
			t.Errorf("%s has no description", tl.Name)
		}
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var s schema
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("%s: input schema: %v", tl.Name, err)
		}
		wantProps := 1 + len(required)
		if s.Type != "object" || len(s.Properties) != wantProps || s.Properties["project"].Type != "string" ||
			!slices.Equal(s.Required, required) {
			t.Errorf("%s: want an optional string project and required %v, got %s", tl.Name, required, raw)
		}
		for _, r := range required {
			if s.Properties[r].Type != "string" {
				t.Errorf("%s: %q is not a string property: %s", tl.Name, r, raw)
			}
		}
	}
	if seen != len(want) {
		t.Errorf("saw %d of the %d loop tools", seen, len(want))
	}
}

// Criterion: list_loops lists the loops of the view.
func TestMCPListLoopsAnswersTheGlobalViewSorted(t *testing.T) {
	text, isErr := call(t, connectLoops(t, nil), "list_loops", nil)
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	for _, want := range []string{"nightly", "Loop nightly", "30m", "when CI is green",
		"watcher", "Loop watcher", "dynamic", "when the queue is empty"} {
		if !strings.Contains(text, want) {
			t.Errorf("list_loops lacks %q: %s", want, text)
		}
	}
	if strings.Index(text, "nightly") > strings.Index(text, "watcher") {
		t.Errorf("loops are not sorted by name: %s", text)
	}
	if strings.Contains(text, "shop-release") {
		t.Errorf("the global view lists the Project's loop: %s", text)
	}
}

func TestMCPListLoopsAcceptsAProjectAndFollowsTheSessionProject(t *testing.T) {
	cs := connectLoops(t, nil)
	text, isErr := call(t, cs, "list_loops", map[string]any{"project": loopsShop})
	if isErr || !strings.Contains(text, "shop-release") || !strings.Contains(text, "1h") || !strings.Contains(text, "nightly") {
		t.Errorf("project argument: want shop-release and the global loops: %v %s", isErr, text)
	}
	text, isErr = call(t, connectLoops(t, nil, shopSession()), "list_loops", nil)
	if isErr || !strings.Contains(text, "shop-release") {
		t.Errorf("session Project: want shop-release: %v %s", isErr, text)
	}
	// A Project that is not registered gets the global answer, not an error.
	text, isErr = call(t, cs, "list_loops", map[string]any{"project": "github.com/acme/unknown"})
	if isErr || !strings.Contains(text, "nightly") || strings.Contains(text, "shop-release") {
		t.Errorf("unregistered Project: want the global loops: %v %s", isErr, text)
	}
}

// Negative: no loop is an answer, not an error.
func TestMCPListLoopsWithNoLoopsIsNotAnError(t *testing.T) {
	edits := map[string]string{}
	for k := range loopFixture() {
		if strings.Contains(k, "/loops/") {
			edits[k] = ""
		}
	}
	text, isErr := call(t, connectLoops(t, edits), "list_loops", nil)
	if isErr || !strings.Contains(text, "No loops") {
		t.Errorf("want an empty answer, not an error: %v %s", isErr, text)
	}
}

// Criterion: get_loop answers the loop, its closure and the digest.
func TestMCPGetLoopAnswersTheLoopItsClosureAndTheAPIDigest(t *testing.T) {
	srv := loopServer(t, loopContent(t, nil))
	cs := sessionFor(t, srv.URL, readerToken)
	text, isErr := call(t, cs, "get_loop", map[string]any{"name": "nightly"})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	api := getLoop(t, srv, readerToken, loopsAPI+"/nightly")
	for _, want := range []string{"nightly", "Loop nightly", "30m", "when CI is green", "Iterate nightly.",
		"reviewer", "deploy", "global", api.Digest} {
		if !strings.Contains(text, want) {
			t.Errorf("get_loop lacks %q: %s", want, text)
		}
	}
	if strings.Contains(text, "planner") || strings.Contains(text, "shop-release") {
		t.Errorf("get_loop shows what is not in the closure: %s", text)
	}
	text, isErr = call(t, cs, "get_loop", map[string]any{"name": "watcher"})
	if isErr || !strings.Contains(text, "dynamic") || strings.Contains(text, "30m") || !strings.Contains(text, "Iterate watcher.") {
		t.Errorf("watcher: want a dynamic loop with its body: %v %s", isErr, text)
	}
}

func TestMCPGetLoopResolvesTheClosureInTheProjectView(t *testing.T) {
	srv := loopServer(t, loopContent(t, nil))
	cs := sessionFor(t, srv.URL, readerToken)
	api := getLoop(t, srv, readerToken, loopsAPI+"/shop-release?project="+loopsShop)
	text, isErr := call(t, cs, "get_loop", map[string]any{"name": "shop-release", "project": loopsShop})
	if isErr || !strings.Contains(text, "planner") || !strings.Contains(text, loopsShop) || !strings.Contains(text, api.Digest) {
		t.Errorf("project argument: want the closure with the Project's reviewer scope and the API digest: %v %s", isErr, text)
	}
	shop := sessionFor(t, srv.URL, readerToken, shopSession())
	text, isErr = call(t, shop, "get_loop", map[string]any{"name": "shop-release"})
	if isErr || !strings.Contains(text, api.Digest) {
		t.Errorf("session Project: want the loop with the API digest: %v %s", isErr, text)
	}
	// The shop view of nightly differs from the global one: its reviewer is the
	// Project's, so the digest and the closure scope change.
	g, _ := call(t, cs, "get_loop", map[string]any{"name": "nightly"})
	p, _ := call(t, cs, "get_loop", map[string]any{"name": "nightly", "project": loopsShop})
	if g == p {
		t.Errorf("nightly answers the same in the global and the shop view")
	}
}

// Negative: a miss is a tool error with a hint, and a hostile name is no path.
func TestMCPGetLoopMissIsAToolErrorWithHint(t *testing.T) {
	cs := connectLoops(t, nil)
	for _, name := range []string{"no-such-loop", "shop-release", "..", "../nightly", "nightly/../watcher", "", "nightly\x00",
		strings.Repeat("a", 5000)} {
		text, isErr := call(t, cs, "get_loop", map[string]any{"name": name})
		if !isErr || !strings.Contains(text, "list_loops") {
			t.Errorf("name %q: want a tool error with a hint to list_loops: %v %s", name, isErr, text)
		}
		for _, bad := range []string{"127.0.0.1", "http://", "/api/", "Iterate", "global/loops", loopsShop} {
			if strings.Contains(text, bad) {
				t.Errorf("name %q: error leaks %q: %s", name, bad, text)
			}
		}
	}
}

// Abuse: a wrong token is the 401 message, an unreachable API leaks no
// address; both as tool errors.
func TestMCPLoopToolsWithAWrongTokenOrNoAPILeakNothing(t *testing.T) {
	wrong := sessionFor(t, loopServer(t, loopContent(t, nil)).URL, "wrong-token-0123456789abcdef")
	down := session(t, "http://127.0.0.1:1", readerToken)
	for who, cs := range map[string]*mcp.ClientSession{"wrong token": wrong, "no API": down} {
		for tool, args := range map[string]map[string]any{"list_loops": nil, "get_loop": {"name": "nightly"}} {
			text, isErr := call(t, cs, tool, args)
			if !isErr {
				t.Errorf("%s, %s: want a tool error: %s", who, tool, text)
			}
			for _, bad := range []string{"127.0.0.1", "http://", "wrong-token", readerToken, "/api/", "Iterate nightly"} {
				if strings.Contains(text, bad) {
					t.Errorf("%s, %s: error leaks %q: %s", who, tool, bad, text)
				}
			}
		}
	}
	text, _ := call(t, wrong, "get_loop", map[string]any{"name": "nightly"})
	if !strings.Contains(text, "missing or invalid API token") {
		t.Errorf("a wrong token: want the 401 message: %s", text)
	}
}

// Criterion: one prompt per loop.
func TestMCPPromptsListOnePerLoopBesideTheSkills(t *testing.T) {
	cs := connectLoops(t, nil)
	res, err := cs.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range res.Prompts {
		names = append(names, p.Name)
		if p.Description == "" {
			t.Errorf("prompt %s has no description", p.Name)
		}
	}
	want := []string{"goldie-deploy", loopPrompt("nightly"), loopPrompt("watcher")}
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("prompts = %v, want %v: the skill and one per loop, none for an agent or another Project's loop", names, want)
	}
	if got := promptNames(t, connectLoops(t, nil, shopSession())); !slices.Contains(got, loopPrompt("shop-release")) {
		t.Errorf("the shop session's prompts = %v, want %s among them", got, loopPrompt("shop-release"))
	}
}

// Criterion: the prompt returns the iteration prompt and the /loop invocation,
// and notes the pull.
func TestMCPLoopPromptReturnsTheIterationPromptTheInvocationAndThePullNote(t *testing.T) {
	cs := connectLoops(t, soloEdits)
	text, err := getPrompt(cs, loopPrompt("nightly"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Iterate nightly.", "/loop 30m", "goldie pull"} {
		if !strings.Contains(text, want) {
			t.Errorf("the nightly prompt lacks %q: %s", want, text)
		}
	}
	res, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: loopPrompt("nightly")})
	if err != nil || len(res.Messages) == 0 {
		t.Fatalf("the prompt has no message: %v", err)
	}
}

func TestMCPLoopPromptOfADynamicLoopHasNoInterval(t *testing.T) {
	text, err := getPrompt(connectLoops(t, soloEdits), loopPrompt("watcher"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "/loop") || !strings.Contains(text, "Iterate watcher.") {
		t.Errorf("the watcher prompt lacks the invocation or the body: %s", text)
	}
	for _, bad := range []string{"/loop dynamic", "/loop true", "/loop 0", "30m"} {
		if strings.Contains(text, bad) {
			t.Errorf("a dynamic loop's invocation carries %q: %s", bad, text)
		}
	}
}

// Negative: a loop whose closure is empty needs no pull, and says so by saying
// nothing of one.
func TestMCPLoopPromptOfALoopWithAnEmptyClosureNotesNoPull(t *testing.T) {
	text, err := getPrompt(connectLoops(t, soloEdits), loopPrompt("solo"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "/loop 15m") || !strings.Contains(text, "Iterate solo.") {
		t.Errorf("the solo prompt lacks the invocation or the body: %s", text)
	}
	if strings.Contains(strings.ToLower(text), "pull") {
		t.Errorf("a loop with no agent and no skill must not ask for a pull: %s", text)
	}
}

func TestMCPLoopPromptFollowsTheSessionProject(t *testing.T) {
	text, err := getPrompt(connectLoops(t, nil, shopSession()), loopPrompt("shop-release"))
	if err != nil || !strings.Contains(text, "Iterate shop-release.") || !strings.Contains(text, "/loop 1h") {
		t.Errorf("the shop session's prompt: %v %q", err, text)
	}
}

// Negative and abuse: a name that is no loop of the view is an error that
// leaks nothing and never returns another Item's content.
func TestMCPLoopPromptGetRefusesNamesThatAreNoPrompt(t *testing.T) {
	cs := connectLoops(t, nil)
	for _, name := range []string{
		"goldie-loop-no-such-loop", "goldie-loop-shop-release", "goldie-loop-", "goldie-loop-..", "goldie-loop-../nightly",
		"goldie-loop-nightly/../watcher", "goldie-loop-nightly\x00", "goldie-loop-nightly%2F", "GOLDIE-LOOP-NIGHTLY",
		"goldie-loop-reviewer", "loop-nightly", "nightly",
	} {
		text, err := getPrompt(cs, name)
		if err == nil {
			t.Errorf("prompt %q: want an error, got %q", name, text)
			continue
		}
		for _, bad := range append([]string{"Iterate", "global reviewer", "global/loops"}, promptResourceLeaks...) {
			if strings.Contains(err.Error(), bad) || strings.Contains(text, bad) {
				t.Errorf("prompt %q: the answer leaks %q: %v", name, bad, err)
			}
		}
	}
}

func TestMCPLoopPromptsWithAWrongTokenLeakNothing(t *testing.T) {
	cs := sessionFor(t, loopServer(t, loopContent(t, nil)).URL, "wrong-token-0123456789abcdef")
	text, err := getPrompt(cs, loopPrompt("nightly"))
	if err == nil {
		t.Fatalf("a wrong token gets the loop prompt: %q", text)
	}
	for _, bad := range append([]string{"Iterate"}, promptResourceLeaks...) {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error leaks %q: %v", bad, err)
		}
	}
}
