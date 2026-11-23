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

// #614: the loop Kind is in the table of Kinds a reference may name, so a loop
// can be deprecated with replaced_by: loop:<new> and a Project can suppress it.

func loopNamed(name string, front ...string) string {
	return "---\nname: " + name + "\ndescription: d\n" + strings.Join(front, "\n") + "\n---\nPrompt.\n"
}

func loopNameList(ls []engine.Loop) string {
	names := []string{}
	for _, l := range ls {
		names = append(names, l.Name)
	}
	return strings.Join(names, ",")
}

func TestLoopReplacedByALoopLoadsAndShowsAsDeprecated(t *testing.T) {
	snap := load(t, map[string]string{
		"global/loops/old.md": loopNamed("old", "interval: 5m", "stop: x", "status: deprecated", "replaced_by: loop:new"),
		"global/loops/new.md": loopNamed("new", "interval: 5m", "stop: x"),
	})
	old, ok := snap.LoopIn("", "old")
	if !ok || old.Status != engine.StatusDeprecated {
		t.Fatalf("LoopIn(old) = %+v, %v; want it served as deprecated", old, ok)
	}
	if n, ok := snap.LoopIn("", "new"); !ok || n.Status == engine.StatusDeprecated {
		t.Errorf("LoopIn(new) = %+v, %v; want it served and not deprecated", n, ok)
	}
	if got := loopNameList(snap.Loops("")); got != "new,old" {
		t.Errorf("Loops = %s, want new,old", got)
	}
}

func TestSuppressingAnAgentDoesNotHideTheLoopOfThatName(t *testing.T) {
	snap := load(t, map[string]string{
		"global/loops/same.md":  loopNamed("same", "interval: 5m", "stop: x"),
		"global/agents/same.md": agentDoc("same", "d"),
		supFile:                 suppressYAML("agent:same"),
	})
	if _, ok := snap.LoopIn(govProject, "same"); !ok {
		t.Error("suppressing agent:same hid the loop of that name")
	}
	if _, ok := snap.AgentIn(govProject, "same"); ok {
		t.Error("agent:same is still served in the suppressing Project")
	}
}
