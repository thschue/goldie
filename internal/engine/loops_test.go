package engine_test

import (
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// #17 slice 1: the cases the API acceptance tests (internal/app/loops_api_test.go)
// leave open, at the engine seam: what a loop file must say to load, and how
// a reference to a draft resolves.

const loopFilePath = "global/loops/nightly.md"

func loopDoc(front ...string) string {
	return "---\nname: nightly\ndescription: d\n" + strings.Join(front, "\n") + "\n---\nPrompt.\n"
}

func TestLoopFrontmatterIsValidatedAtLoad(t *testing.T) {
	cases := map[string]struct {
		doc  string
		want string // a fragment of the error, "" for a load that succeeds
	}{
		"interval":          {loopDoc("interval: 30m", "stop: done"), ""},
		"fractional":        {loopDoc("interval: 1.5h", "stop: done"), ""},
		"dynamic":           {loopDoc("dynamic: true", "stop: done"), ""},
		"dynamic false too": {loopDoc("interval: 5m", "dynamic: false", "stop: done"), ""},
		"both":              {loopDoc("interval: 5m", "dynamic: true", "stop: done"), "not both"},
		"neither":           {loopDoc("stop: done"), "interval or dynamic"},
		"dynamic false":     {loopDoc("dynamic: false", "stop: done"), "interval or dynamic"},
		"zero":              {loopDoc("interval: 0s", "stop: done"), "positive duration"},
		"negative":          {loopDoc("interval: -5m", "stop: done"), "positive duration"},
		"no unit":           {loopDoc("interval: 30", "stop: done"), "positive duration"},
		"words":             {loopDoc("interval: often", "stop: done"), "positive duration"},
		"no stop":           {loopDoc("interval: 5m"), "stop"},
		"stop list":         {loopDoc("interval: 5m", "stop: [a, b]"), "stop"},
		"agents scalar":     {loopDoc("interval: 5m", "stop: x", "agents: reviewer"), "agents must be a list"},
		"skills path":       {loopDoc("interval: 5m", "stop: x", "skills: [a/b]"), "plain name"},
		"duplicate":         {loopDoc("interval: 5m", "stop: x", "skills: [a, A]"), "twice"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{loopFilePath: tc.doc, "global/skills/a/SKILL.md": "# a\n"}
			_, err := engine.NewDirSource(writeFiles(t, files)).Load(t.Context())
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("Load: %v", err)
			case tc.want != "" && err == nil:
				t.Errorf("Load accepted the file, want an error with %q", tc.want)
			case tc.want != "" && (!strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), loopFilePath)):
				t.Errorf("error = %q, want it to name %s and say %q", err, loopFilePath, tc.want)
			}
		})
	}
}

func TestLoopReferencesResolveAsPublishedItems(t *testing.T) {
	draftAgent := "---\nname: wip\ndescription: d\nstatus: draft\n---\nx\n"
	loop := loopDoc("interval: 5m", "stop: x", "agents: [wip]")
	files := map[string]string{loopFilePath: loop, "global/agents/wip.md": draftAgent}
	if _, err := engine.NewDirSource(writeFiles(t, files)).Load(t.Context()); err == nil ||
		!strings.Contains(err.Error(), "wip") || !strings.Contains(err.Error(), loopFilePath) {
		t.Errorf("a loop that names only a draft agent: err = %v, want it dangling", err)
	}
}

func TestLoopClosureIsNotServedWhereAReferenceIsSuppressed(t *testing.T) {
	snap := load(t, map[string]string{
		loopFilePath:                               loopDoc("interval: 5m", "stop: x", "agents: [reviewer]"),
		"global/agents/reviewer.md":                agentDoc("reviewer", "d"),
		"projects/" + govProject + "/project.yaml": "suppress:\n  - agent:reviewer\n",
	})
	if _, ok := snap.LoopIn("", "nightly"); !ok {
		t.Fatal("the global view does not serve nightly")
	}
	if l, ok := snap.LoopIn(govProject, "nightly"); ok {
		t.Errorf("the Project suppresses the reviewer, yet nightly is served: %+v", l)
	}
	if got := snap.Loops(govProject); len(got) != 0 {
		t.Errorf("Loops(project) = %+v, want none", got)
	}
}
