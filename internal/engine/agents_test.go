package engine_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #405 (slice 2 of #15): an agent definition is an Item of Kind agent,
// the file agents/<name>.md, in the three Scopes: global/agents/,
// projects/<host>/<org>/<repo>/agents/ and .goldie/agents/. They resolve
// local > project > global as articles do (ADR 0021, Snapshot.winner).
//
// The API the tests assume, chosen from the articles API:
//
//	type Agent struct {
//		Name        string // the slug, equal to the frontmatter name (ADR 0015)
//		Description string // frontmatter description, required
//		Scope       string // ScopeGlobal, ScopeLocal or the Project
//		Status      string
//		Enforced    bool
//		ReplacedBy  string
//		Raw         []byte // the file as written, for pull
//	}
//	func (a Agent) Kind() string // "agent"
//	func (s *Snapshot) AgentIn(proj, name string, opts ...ReadOptions) (Agent, bool)
//	func (s *Snapshot) Agents(proj string, opts ...ReadOptions) []Agent // the view, sorted by Name
//
// Only name and description are parsed; every other frontmatter key (tools,
// model, ...) stays in Raw and is never rejected (PM decision, tier 1).

func globalAgentFile(name string) string { return "global/agents/" + name + ".md" }

func projectAgentFile(name string) string {
	return "projects/" + govProject + "/agents/" + name + ".md"
}

func localAgentFile(name string) string { return ".goldie/agents/" + name + ".md" }

// agentDoc is an agent file with the given extra frontmatter lines.
func agentDoc(name, description string, front ...string) string {
	extra := ""
	if len(front) > 0 {
		extra = strings.Join(front, "\n") + "\n"
	}
	return "---\nname: " + name + "\ndescription: " + description + "\n" + extra + "---\nYou are " + name + ".\n"
}

func agentNames(as []engine.Agent) []string {
	out := []string{}
	for _, a := range as {
		out = append(out, a.Name)
	}
	return out
}

// agentAnswer says which copy of name answers in proj, by Scope and
// Description, or "none".
func agentAnswer(snap *engine.Snapshot, proj, name string, opts ...engine.ReadOptions) string {
	a, ok := snap.AgentIn(proj, name, opts...)
	if !ok {
		return "none"
	}
	return a.Scope + "/" + a.Description
}

// quoteASCII is a YAML double-quoted scalar with every non-ASCII rune escaped.
func quoteASCII(s string) string { return fmt.Sprintf("%+q", s) }

// --- Criterion 1: loads agents/<name>.md from the three Scopes ---

func TestAgentsLoadFromTheGlobalScope(t *testing.T) {
	snap := load(t, map[string]string{
		globalAgentFile("reviewer"): agentDoc("reviewer", "Reviews code"),
		globalAgentFile("planner"):  agentDoc("planner", "Plans work"),
	})
	a, ok := snap.AgentIn("", "reviewer")
	if !ok {
		t.Fatal("AgentIn(reviewer) found nothing")
	}
	if a.Name != "reviewer" || a.Description != "Reviews code" || a.Scope != engine.ScopeGlobal {
		t.Errorf("agent = %+v", a)
	}
	if a.Kind() != "agent" {
		t.Errorf("Kind() = %q, want agent", a.Kind())
	}
	if a.Status != engine.StatusPublished || a.Enforced || a.ReplacedBy != "" {
		t.Errorf("lifecycle defaults = %q %v %q", a.Status, a.Enforced, a.ReplacedBy)
	}
	if got := agentNames(snap.Agents("")); !slices.Equal(got, []string{"planner", "reviewer"}) {
		t.Errorf("Agents(\"\") = %v, want planner, reviewer", got)
	}
	if _, ok := snap.AgentIn("", "missing"); ok {
		t.Error("AgentIn(missing) found an agent")
	}
}

func TestAgentLookupIgnoresCase(t *testing.T) {
	snap := load(t, map[string]string{globalAgentFile("reviewer"): agentDoc("reviewer", "d")})
	for _, name := range []string{"Reviewer", "REVIEWER"} {
		if a, ok := snap.AgentIn("", name); !ok || a.Name != "reviewer" {
			t.Errorf("AgentIn(%q) = %+v, %v", name, a, ok)
		}
	}
}

func TestAgentsLoadFromAProjectAndOnlyShowInThatView(t *testing.T) {
	snap := load(t, map[string]string{
		globalAgentFile("reviewer"):                    agentDoc("reviewer", "global"),
		projectAgentFile("shop-bot"):                   agentDoc("shop-bot", "shop only"),
		"projects/github.com/acme/other/articles/x.md": article("X"),
	})
	for _, proj := range []string{govProject, strings.ToUpper(govProject)} {
		if got := agentAnswer(snap, proj, "shop-bot"); got != govProject+"/shop only" {
			t.Errorf("AgentIn(%q, shop-bot) = %s", proj, got)
		}
		if got := agentNames(snap.Agents(proj)); !slices.Equal(got, []string{"reviewer", "shop-bot"}) {
			t.Errorf("Agents(%q) = %v", proj, got)
		}
	}
	for _, proj := range []string{"", "github.com/acme/other", "github.com/acme/unregistered"} {
		if got := agentAnswer(snap, proj, "shop-bot"); got != "none" {
			t.Errorf("AgentIn(%q, shop-bot) = %s; a Project agent is not in another view", proj, got)
		}
		if got := agentNames(snap.Agents(proj)); !slices.Equal(got, []string{"reviewer"}) {
			t.Errorf("Agents(%q) = %v, want only the global agent", proj, got)
		}
	}
}

func TestLocalAgentsAnswerInEveryView(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalAgentFile("reviewer"):  agentDoc("reviewer", "global"),
		projectAgentFile("shop-bot"): agentDoc("shop-bot", "shop"),
		localAgentFile("mine"):       agentDoc("mine", "local"),
	})
	for _, proj := range append([]string{""}, localViews...) {
		if got := agentAnswer(snap, proj, "mine"); got != engine.ScopeLocal+"/local" {
			t.Errorf("AgentIn(%q, mine) = %s", proj, got)
		}
		if !slices.Contains(agentNames(snap.Agents(proj)), "mine") {
			t.Errorf("Agents(%q) = %v, want mine in it", proj, agentNames(snap.Agents(proj)))
		}
	}
}

func TestAgentsResolveLocalThenProjectThenGlobal(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalAgentFile("a"):  agentDoc("a", "global a"),
		globalAgentFile("b"):  agentDoc("b", "global b"),
		globalAgentFile("c"):  agentDoc("c", "global c"),
		projectAgentFile("b"): agentDoc("b", "project b"),
		projectAgentFile("c"): agentDoc("c", "project c"),
		localAgentFile("c"):   agentDoc("c", "local c"),
	})
	want := map[string]map[string]string{
		"": {
			"a": "global/global a", "b": "global/global b", "c": "local/local c",
		},
		govProject: {
			"a": "global/global a", "b": govProject + "/project b", "c": "local/local c",
		},
	}
	for proj, names := range want {
		for name, w := range names {
			if got := agentAnswer(snap, proj, name); got != w {
				t.Errorf("AgentIn(%q, %s) = %s, want %s", proj, name, got, w)
			}
		}
		// One entry per name, the winner's.
		if got := agentNames(snap.Agents(proj)); !slices.Equal(got, []string{"a", "b", "c"}) {
			t.Errorf("Agents(%q) = %v, want one a, b, c each", proj, got)
		}
		for _, a := range snap.Agents(proj) {
			if w := names[a.Name]; a.Scope+"/"+a.Description != w {
				t.Errorf("Agents(%q) lists %s as %s/%s, want %s", proj, a.Name, a.Scope, a.Description, w)
			}
		}
	}
}

// An agent and an article may share a name: the Kind is part of the identity.
func TestAgentAndArticleOfTheSameNameBothLoad(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("helper"):      article("Helper article"),
		globalAgentFile("helper"): agentDoc("helper", "Helper agent"),
	})
	if a, ok := snap.Article("helper"); !ok || a.Name != "Helper article" {
		t.Errorf("Article(helper) = %+v, %v", a, ok)
	}
	if a, ok := snap.AgentIn("", "helper"); !ok || a.Description != "Helper agent" {
		t.Errorf("AgentIn(helper) = %+v, %v", a, ok)
	}
}

// An agent is not an article: it is not listed, read or found as one.
func TestAgentsAreNotArticles(t *testing.T) {
	snap := load(t, map[string]string{
		globalAgentFile("solo"): agentDoc("solo", "Only an agent", "keywords: [rollout]"),
		globalFile("kept"):      article("Kept", "rollout"),
	})
	if _, ok := snap.Article("solo"); ok {
		t.Error("Article(solo) served an agent")
	}
	if got := len(snap.Articles()); got != 1 {
		t.Errorf("Articles() = %d, want only the article", got)
	}
	if got := hitIDList(snap.Search("rollout")); !slices.Equal(got, []string{"kept"}) {
		t.Errorf("Search = %v, want only the article", got)
	}
}

func TestAgentsOfASnapshotWithoutAgents(t *testing.T) {
	snap := load(t, map[string]string{globalFile("a"): article("A")})
	if got := snap.Agents(""); len(got) != 0 {
		t.Errorf("Agents = %v, want none", got)
	}
}

func TestAgentsAreCopies(t *testing.T) {
	snap := load(t, map[string]string{globalAgentFile("x"): agentDoc("x", "d")})
	a, _ := snap.AgentIn("", "x")
	a.Description = "changed"
	if len(a.Raw) > 0 {
		a.Raw[0] = 'X'
	}
	list := snap.Agents("")
	if len(list) != 1 {
		t.Fatalf("Agents = %v, want x", agentNames(list))
	}
	list[0].Description = "changed"
	if len(list[0].Raw) > 0 {
		list[0].Raw[0] = 'X'
	}
	again, _ := snap.AgentIn("", "x")
	if again.Description != "d" || !bytes.HasPrefix(again.Raw, []byte("---\n")) {
		t.Errorf("the Snapshot changed through a returned Agent: %+v", again)
	}
}

// --- Criterion 1: frontmatter name, description, case collisions ---

func TestAgentNameMustEqualTheSlug(t *testing.T) {
	for name, file := range map[string]string{
		"global":  globalAgentFile("reviewer"),
		"project": projectAgentFile("reviewer"),
		"local":   localAgentFile("reviewer"),
	} {
		t.Run(name, func(t *testing.T) {
			loadLocalErr(t, map[string]string{file: agentDoc("something-else", "d")}, file)
		})
	}
}

func TestAgentFrontmatterErrorsNameTheFile(t *testing.T) {
	for name, content := range map[string]string{
		"no frontmatter":         "just text\n",
		"unclosed":               "---\nname: x\ndescription: d\n",
		"no name":                "---\ndescription: d\n---\n",
		"blank name":             "---\nname: \"  \"\ndescription: d\n---\n",
		"no description":         "---\nname: x\n---\nbody\n",
		"empty description":      "---\nname: x\ndescription: \"\"\n---\n",
		"blank description":      "---\nname: x\ndescription: \"   \"\n---\n",
		"description is a list":  "---\nname: x\ndescription: [a, b]\n---\n",
		"description is a map":   "---\nname: x\ndescription: {a: b}\n---\n",
		"malformed yaml":         "---\nname: [unclosed\ndescription: d\n---\n",
		"name is a list":         "---\nname: [x]\ndescription: d\n---\n",
		"empty frontmatter":      "---\n---\nbody",
		"only the closing fence": "---\nname: x\ndescription: d",
	} {
		t.Run(name, func(t *testing.T) {
			for _, file := range []string{globalAgentFile("x"), projectAgentFile("x"), localAgentFile("x")} {
				msg := loadLocalErr(t, map[string]string{file: content}, file)
				if strings.Contains(msg, "just text") {
					t.Errorf("error echoes the content: %q", msg)
				}
			}
		})
	}
}

func TestAgentNamesDifferingOnlyInCaseFailTheLoad(t *testing.T) {
	for name, files := range map[string][2]string{
		"global":  {globalAgentFile("Reviewer"), globalAgentFile("reviewer")},
		"project": {projectAgentFile("Reviewer"), projectAgentFile("reviewer")},
		"local":   {localAgentFile("Reviewer"), localAgentFile("reviewer")},
	} {
		t.Run(name, func(t *testing.T) {
			// Each file's name equals its own slug, so only the collision is wrong.
			loadLocalErr(t, map[string]string{
				files[0]: agentDoc("Reviewer", "One"),
				files[1]: agentDoc("reviewer", "Two"),
			}, files[0], files[1])
		})
	}
}

// The same name in two Scopes is shadowing, not a collision.
func TestAgentSameNameInThreeScopesLoads(t *testing.T) {
	loadLocal(t, map[string]string{
		globalAgentFile("x"):  agentDoc("x", "g"),
		projectAgentFile("x"): agentDoc("x", "p"),
		localAgentFile("x"):   agentDoc("x", "l"),
	})
}

// --- Criterion 1: status, enforced, replaced_by and suppress as for articles ---

func TestAgentStatusDraftIsHiddenUnlessAsked(t *testing.T) {
	snap := load(t, map[string]string{
		globalAgentFile("draft"): agentDoc("draft", "d", "status: draft"),
		globalAgentFile("old"):   agentDoc("old", "d", "status: deprecated", "replaced_by: agent:new"),
		globalAgentFile("new"):   agentDoc("new", "d"),
	})
	if _, ok := snap.AgentIn("", "draft"); ok {
		t.Error("a draft agent is served by default")
	}
	if a, ok := snap.AgentIn("", "draft", withDrafts); !ok || a.Status != engine.StatusDraft {
		t.Errorf("AgentIn(draft, drafts) = %+v, %v", a, ok)
	}
	if got := agentNames(snap.Agents("")); !slices.Equal(got, []string{"new", "old"}) {
		t.Errorf("Agents = %v; a draft must be left out and a deprecated agent served", got)
	}
	if got := agentNames(snap.Agents("", withDrafts)); !slices.Equal(got, []string{"draft", "new", "old"}) {
		t.Errorf("Agents(drafts) = %v", got)
	}
	if a, ok := snap.AgentIn("", "old"); !ok || a.Status != engine.StatusDeprecated || a.ReplacedBy != "agent:new" {
		t.Errorf("AgentIn(old) = %+v, %v", a, ok)
	}
}

func TestAgentDraftNeverShadowsInTheDefaultView(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalAgentFile("x"):  agentDoc("x", "global"),
		projectAgentFile("x"): agentDoc("x", "project draft", "status: draft"),
		localAgentFile("y"):   agentDoc("y", "local draft", "status: draft"),
		globalAgentFile("y"):  agentDoc("y", "global y"),
	})
	if got := agentAnswer(snap, govProject, "x"); got != "global/global" {
		t.Errorf("AgentIn(project, x) = %s, want the global copy", got)
	}
	if got := agentAnswer(snap, govProject, "x", withDrafts); got != govProject+"/project draft" {
		t.Errorf("AgentIn(project, x, drafts) = %s", got)
	}
	if got := agentAnswer(snap, "", "y"); got != "global/global y" {
		t.Errorf("AgentIn(y) = %s, want the global copy", got)
	}
}

func TestAgentInvalidLifecycleFrontmatterFailsTheLoad(t *testing.T) {
	for name, front := range map[string]string{
		"bad status":           "status: retired",
		"enforced yes":         "enforced: yes",
		"replaced_by no kind":  "replaced_by: other",
		"replaced_by bad kind": "replaced_by: skill:other",
	} {
		t.Run(name, func(t *testing.T) {
			loadErr(t, map[string]string{globalAgentFile("x"): agentDoc("x", "d", front)}, globalAgentFile("x"))
		})
	}
}

func TestEnforcedAgentCannotBeShadowed(t *testing.T) {
	t.Run("project over global", func(t *testing.T) {
		msg := loadErr(t, map[string]string{
			globalAgentFile("rules"):  agentDoc("rules", "g", "enforced: true"),
			projectAgentFile("rules"): agentDoc("rules", "p"),
		}, globalAgentFile("rules"), projectAgentFile("rules"))
		if strings.Contains(msg, "You are") {
			t.Errorf("error echoes agent content: %q", msg)
		}
	})
	t.Run("local over global", func(t *testing.T) {
		loadLocalErr(t, map[string]string{
			globalAgentFile("rules"): agentDoc("rules", "g", "enforced: true"),
			localAgentFile("rules"):  agentDoc("rules", "l"),
		}, "agent:rules", localAgentFile("rules"))
	})
	t.Run("local over project", func(t *testing.T) {
		loadLocalErr(t, map[string]string{
			projectAgentFile("rules"): agentDoc("rules", "p", "enforced: true"),
			localAgentFile("rules"):   agentDoc("rules", "l"),
		}, "agent:rules", localAgentFile("rules"))
	})
	t.Run("other case", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalAgentFile("rules"):  agentDoc("rules", "g", "enforced: true"),
			projectAgentFile("RULES"): agentDoc("RULES", "p"),
		}, globalAgentFile("rules"), projectAgentFile("RULES"))
	})
	t.Run("a draft over it is not a shadow", func(t *testing.T) {
		snap := load(t, map[string]string{
			globalAgentFile("rules"):  agentDoc("rules", "g", "enforced: true"),
			projectAgentFile("rules"): agentDoc("rules", "p", "status: draft"),
		})
		if got := agentAnswer(snap, govProject, "rules"); got != "global/g" {
			t.Errorf("AgentIn = %s, want the enforced copy", got)
		}
	})
	t.Run("an enforced article of the same name does not enforce the agent", func(t *testing.T) {
		snap := load(t, map[string]string{
			globalFile("rules"):       govArticle("Rules", "enforced: true"),
			projectAgentFile("rules"): agentDoc("rules", "p"),
		})
		if got := agentAnswer(snap, govProject, "rules"); got != govProject+"/p" {
			t.Errorf("AgentIn = %s", got)
		}
	})
}

func TestAgentReplacedByResolvesInTheReferrersView(t *testing.T) {
	t.Run("global to global", func(t *testing.T) {
		mustLoad(t, map[string]string{
			globalAgentFile("old"): agentDoc("old", "d", "status: deprecated", "replaced_by: agent:new"),
			globalAgentFile("new"): agentDoc("new", "d"),
		})
	})
	t.Run("project to its own agent", func(t *testing.T) {
		mustLoad(t, map[string]string{
			projectAgentFile("old"): agentDoc("old", "d", "status: deprecated", "replaced_by: agent:new"),
			projectAgentFile("new"): agentDoc("new", "d"),
		})
	})
	t.Run("local to its own agent", func(t *testing.T) {
		loadLocal(t, map[string]string{
			localAgentFile("old"): agentDoc("old", "d", "status: deprecated", "replaced_by: agent:new"),
			localAgentFile("new"): agentDoc("new", "d"),
		})
	})
	t.Run("an article may be replaced by an agent", func(t *testing.T) {
		mustLoad(t, map[string]string{
			globalFile("old"):      govArticle("Old", "status: deprecated", "replaced_by: agent:new"),
			globalAgentFile("new"): agentDoc("new", "d"),
		})
	})
	t.Run("an agent may be replaced by an article", func(t *testing.T) {
		mustLoad(t, map[string]string{
			globalAgentFile("old"): agentDoc("old", "d", "status: deprecated", "replaced_by: article:new"),
			globalFile("new"):      article("New"),
		})
	})
	t.Run("a Project agent sees a global one", func(t *testing.T) {
		mustLoad(t, map[string]string{
			projectAgentFile("old"): agentDoc("old", "d", "status: deprecated", "replaced_by: agent:new"),
			globalAgentFile("new"):  agentDoc("new", "d"),
		})
	})
	t.Run("dangling", func(t *testing.T) {
		msg := loadErr(t, map[string]string{
			globalAgentFile("old"): agentDoc("old", "d", "status: deprecated", "replaced_by: agent:nobody"),
		}, globalAgentFile("old"))
		if !strings.Contains(msg, "replaced_by") {
			t.Errorf("error %q does not name replaced_by", msg)
		}
	})
	t.Run("an article of that name is not an agent", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalAgentFile("old"): agentDoc("old", "d", "status: deprecated", "replaced_by: agent:new"),
			globalFile("new"):      article("New"),
		}, globalAgentFile("old"))
	})
	t.Run("a global agent cannot see a Project agent", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalAgentFile("old"):  agentDoc("old", "d", "status: deprecated", "replaced_by: agent:new"),
			projectAgentFile("new"): agentDoc("new", "d"),
		}, globalAgentFile("old"))
	})
	t.Run("a draft target", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalAgentFile("old"): agentDoc("old", "d", "status: deprecated", "replaced_by: agent:new"),
			globalAgentFile("new"): agentDoc("new", "d", "status: draft"),
		}, globalAgentFile("old"))
	})
	t.Run("itself", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalAgentFile("old"): agentDoc("old", "d", "status: deprecated", "replaced_by: agent:old"),
		}, globalAgentFile("old"))
	})
	t.Run("a cycle", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalAgentFile("a"): agentDoc("a", "d", "status: deprecated", "replaced_by: agent:b"),
			globalAgentFile("b"): agentDoc("b", "d", "status: deprecated", "replaced_by: agent:a"),
		})
	})
}

func TestProjectSuppressHidesGlobalAgentsInThatProjectOnly(t *testing.T) {
	snap := load(t, map[string]string{
		globalAgentFile("noisy"):                       agentDoc("noisy", "d"),
		globalAgentFile("kept"):                        agentDoc("kept", "d"),
		projectAgentFile("own"):                        agentDoc("own", "d"),
		"projects/github.com/acme/other/articles/x.md": article("X"),
		supFile: suppressYAML("agent:noisy"),
	})
	if got := agentNames(snap.Agents(govProject)); !slices.Equal(got, []string{"kept", "own"}) {
		t.Errorf("Agents(project) = %v, want noisy hidden", got)
	}
	if got := agentAnswer(snap, govProject, "NOISY", withDrafts); got != "none" {
		t.Errorf("AgentIn(project, NOISY) = %s", got)
	}
	for _, proj := range []string{"", "github.com/acme/other"} {
		if got := agentNames(snap.Agents(proj)); !slices.Contains(got, "noisy") {
			t.Errorf("Agents(%q) = %v, want noisy in it", proj, got)
		}
	}
	if got := suppressionsOf(t, snap, govProject); !slices.Equal(got, []string{"agent:noisy"}) {
		t.Errorf("Suppressions = %v", got)
	}
}

func TestLocalSuppressHidesGlobalAgentsInEveryView(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalAgentFile("noisy"):  agentDoc("noisy", "d"),
		globalAgentFile("kept"):   agentDoc("kept", "d"),
		projectAgentFile("other"): agentDoc("other", "d"),
		localConfigFile:           suppressYAML("agent:noisy"),
	})
	for _, proj := range localViews {
		if got := agentAnswer(snap, proj, "noisy", withDrafts); got != "none" {
			t.Errorf("AgentIn(%q, noisy) = %s; the local config suppresses it", proj, got)
		}
		if got := agentAnswer(snap, proj, "kept"); got != "global/d" {
			t.Errorf("AgentIn(%q, kept) = %s", proj, got)
		}
	}
}

func TestSuppressingAnAgentDoesNotHideTheArticleOfThatName(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("same"):      article("Article"),
		globalAgentFile("same"): agentDoc("same", "d"),
		supFile:                 suppressYAML("agent:same"),
	})
	if _, ok := snap.ArticleIn(govProject, "same"); !ok {
		t.Error("suppress: [agent:same] hid the article of that name")
	}
	if _, ok := snap.AgentIn(govProject, "same"); ok {
		t.Error("suppress: [agent:same] did not hide the agent")
	}
}

func TestSuppressingAnEnforcedAgentFailsTheLoad(t *testing.T) {
	loadErr(t, map[string]string{
		globalAgentFile("rules"): agentDoc("rules", "d", "enforced: true"),
		supFile:                  suppressYAML("agent:rules"),
	}, supFile)
	loadLocalErr(t, map[string]string{
		globalAgentFile("rules"): agentDoc("rules", "d", "enforced: true"),
		localConfigFile:          suppressYAML("agent:rules"),
	}, localConfigFile)
}

func TestSuppressingAMissingAgentIsAWarning(t *testing.T) {
	snap := load(t, map[string]string{
		globalAgentFile("kept"): agentDoc("kept", "d"),
		supFile:                 suppressYAML("agent:ghost"),
	})
	if w := snap.Warnings(); len(w) != 1 || !strings.Contains(w[0], "agent:ghost") {
		t.Errorf("Warnings = %v, want one naming agent:ghost", w)
	}
}

// --- Criterion 2: the original bytes are kept; only name and description are parsed ---

func TestAgentKeepsTheOriginalBytesForPull(t *testing.T) {
	// Odd spacing, a comment, a quoted description, unknown keys and a body
	// that looks like frontmatter: all must come back byte for byte.
	orig := "---\n# a comment\nname:    planner\ndescription: \"Plans: the work\"\ntools: Read, Grep,   Glob\nmodel: sonnet\nx-custom:\n  nested: [1, 2]\n---\n\n---\nBody with trailing space \n\n\n"
	for name, files := range map[string]map[string]string{
		"global":  {globalAgentFile("planner"): orig},
		"project": {projectAgentFile("planner"): orig},
		"local":   {localAgentFile("planner"): orig},
	} {
		t.Run(name, func(t *testing.T) {
			snap := loadLocal(t, files)
			a, ok := snap.AgentIn(govProject, "planner")
			if !ok {
				t.Fatal("agent not served")
			}
			if string(a.Raw) != orig {
				t.Errorf("Raw = %q, want the file as written %q", a.Raw, orig)
			}
			if a.Description != "Plans: the work" {
				t.Errorf("Description = %q", a.Description)
			}
		})
	}
}

func TestAgentFrontmatterOtherThanNameAndDescriptionIsNeverRejected(t *testing.T) {
	for name, front := range map[string][]string{
		"tools list":            {"tools: [Read, Grep]"},
		"tools string":          {"tools: Read, Grep"},
		"tools map":             {"tools: {a: b}"},
		"model":                 {"model: sonnet"},
		"model number":          {"model: 5"},
		"unknown key":           {"x-anything: [1, {a: b}]"},
		"null":                  {"color:"},
		"boolean spellings":     {"background: yes", "memory: on"},
		"keywords is a map":     {"keywords: {a: b}"},
		"keywords is a string":  {"keywords: rollout"},
		"anchors":               {"base: &b [1, 2]", "other: *b"},
		"a long value":          {"note: " + strings.Repeat("x", 4000)},
		"unicode key":           {"ключ: значение"},
		"many keys":             {"a: 1", "b: 2", "c: 3", "d: 4", "e: 5"},
		"multi-line block text": {"prompt: |", "  line one", "  line two"},
	} {
		t.Run(name, func(t *testing.T) {
			content := agentDoc("planner", "Plans", front...)
			snap := load(t, map[string]string{globalAgentFile("planner"): content})
			a, ok := snap.AgentIn("", "planner")
			if !ok {
				t.Fatal("agent not served")
			}
			if a.Name != "planner" || a.Description != "Plans" {
				t.Errorf("agent = %+v", a)
			}
			if string(a.Raw) != content {
				t.Errorf("Raw = %q, want %q", a.Raw, content)
			}
		})
	}
}

// The lifecycle keys keep their meaning next to the raw ones.
func TestAgentOtherFrontmatterDoesNotHideLifecycleKeys(t *testing.T) {
	snap := load(t, map[string]string{
		globalAgentFile("x"): agentDoc("x", "d", "tools: [Read]", "status: draft", "enforced: true"),
	})
	a, ok := snap.AgentIn("", "x", withDrafts)
	if !ok || a.Status != engine.StatusDraft || !a.Enforced {
		t.Errorf("agent = %+v, %v", a, ok)
	}
}

// --- Criterion 3: as for articles: symlinks, hidden runes, CRLF, 1 MiB ---

func outsideAgents(t *testing.T) string {
	t.Helper()
	return writeFiles(t, map[string]string{
		"agents/stolen.md": agentDoc("stolen", "Stolen"),
		"stolen.md":        agentDoc("stolen", "Stolen"),
	})
}

func TestSymlinkedProjectAgentsDirectoryIsRejected(t *testing.T) {
	for name, target := range map[string]string{
		"inside the root":  "../../../../global/agents",
		"outside the root": "outside",
	} {
		t.Run(name, func(t *testing.T) {
			root := writeFiles(t, map[string]string{globalAgentFile("g"): agentDoc("g", "g")})
			if target == "outside" {
				target = filepath.Join(outsideAgents(t), "agents")
			}
			mustMkdir(t, filepath.Join(root, "projects/github.com/acme/shop"))
			if err := os.Symlink(target, filepath.Join(root, "projects/github.com/acme/shop/agents")); err != nil {
				t.Skip("symlinks unavailable")
			}
			snap, err := engine.NewDirSource(root).Load(t.Context())
			if err == nil {
				t.Fatalf("a symlinked Project agents directory was accepted: %v", agentNames(snap.Agents(govProject)))
			}
			if want := "projects/github.com/acme/shop/agents: "; !strings.HasPrefix(err.Error(), want) || strings.Contains(err.Error(), root) {
				t.Fatalf("error = %q, want prefix %q and no host path", err, want)
			}
		})
	}
}

func TestSymlinkedLocalAgentsDirectoryIsRefused(t *testing.T) {
	outside := outsideAgents(t)
	for name, setup := range map[string]func(t *testing.T, repo string) error{
		"outside the repository": func(t *testing.T, repo string) error {
			return symlink(t, filepath.Join(outside, "agents"), filepath.Join(repo, ".goldie", "agents"))
		},
		"inside .goldie": func(t *testing.T, repo string) error {
			writeTo(t, repo, ".goldie/other/stolen.md", agentDoc("stolen", "Stolen"))
			return symlink(t, "other", filepath.Join(repo, ".goldie", "agents"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			src, dirs, ok := localFSSource(t, setup)
			if !ok {
				t.Skip("symlinks unavailable")
			}
			assertRefused(t, src, dirs, outside, ".goldie/agents")
		})
	}
}

// A symlinked global agents directory or agent file is never followed: Load
// refuses it, or loads without it. Nothing it points at is served.
func TestSymlinkedGlobalAgentsAreNotFollowed(t *testing.T) {
	outside := outsideAgents(t)
	for name, setup := range map[string]func(t *testing.T, root string) error{
		"directory outside the root": func(t *testing.T, root string) error {
			return symlink(t, filepath.Join(outside, "agents"), filepath.Join(root, "global", "agents"))
		},
		"file outside the root": func(t *testing.T, root string) error {
			writeTo(t, root, globalAgentFile("kept"), agentDoc("kept", "Kept"))
			return symlink(t, filepath.Join(outside, "stolen.md"), filepath.Join(root, filepath.FromSlash(globalAgentFile("stolen"))))
		},
		"file inside the root": func(t *testing.T, root string) error {
			writeTo(t, root, globalAgentFile("kept"), agentDoc("stolen", "Stolen"))
			return symlink(t, "kept.md", filepath.Join(root, filepath.FromSlash(globalAgentFile("stolen"))))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeFiles(t, map[string]string{globalFile("a"): article("A")})
			if err := setup(t, root); err != nil {
				t.Skip("symlinks unavailable")
			}
			snap, err := engine.NewDirSource(root).Load(t.Context())
			if err != nil {
				if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), outside) {
					t.Errorf("error %q leaks a host path", err)
				}
				return
			}
			if a, ok := snap.AgentIn("", "stolen", withDrafts); ok {
				t.Errorf("served the symlinked agent: %+v", a)
			}
		})
	}
}

func TestSymlinkedLocalAgentFileIsNotFollowed(t *testing.T) {
	outside := outsideAgents(t)
	src, dirs, ok := localFSSource(t, func(t *testing.T, repo string) error {
		writeTo(t, repo, localAgentFile("mine"), agentDoc("mine", "Mine"))
		return symlink(t, filepath.Join(outside, "stolen.md"), filepath.Join(repo, filepath.FromSlash(localAgentFile("stolen"))))
	})
	if !ok {
		t.Skip("symlinks unavailable")
	}
	snap, err := src.Load(t.Context())
	if err != nil {
		assertNoHostPath(t, err.Error(), dirs)
		if strings.Contains(err.Error(), outside) {
			t.Errorf("error %q leaks the symlink target", err)
		}
		return
	}
	for _, proj := range localViews {
		if a, ok := snap.AgentIn(proj, "stolen", withDrafts); ok {
			t.Errorf("AgentIn(%q, stolen) = %+v; a symlinked agent must not be served", proj, a)
		}
	}
}

// A reference to an agent holds the same rune rule as one to an article
// (contract.HiddenRune): replaced_by and suppress fail the load for every
// hidden rune, even when an agent with exactly that spelling exists, and the
// error never carries the rune raw.
func agentRefRunes() []rune {
	return append(append([]rune{0x200b, 0x200d, 0x202e, 0x2066, 0xfeff, 0x0085, 0x007f, 0xe0041}, invisibleOutsideCf...), notPrintable...)
}

func TestAgentReplacedByWithAHiddenRuneFailsTheLoad(t *testing.T) {
	for _, r := range agentRefRunes() {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			target := "a" + string(r) + "b"
			files := map[string]string{
				globalAgentFile("ref"): agentDoc("ref", "d", "status: deprecated", "replaced_by: "+quoteASCII("agent:"+target)),
			}
			if r != 0 {
				files[globalAgentFile(target)] = agentDoc(quoteASCII(target), "d")
			}
			msg := loadErr(t, files, globalAgentFile("ref"))
			if !strings.Contains(msg, "replaced_by") {
				t.Errorf("error %q does not name replaced_by", msg)
			}
			if strings.ContainsRune(msg, r) {
				t.Errorf("error %q carries %U unescaped", msg, r)
			}
		})
	}
}

func TestAgentSuppressWithAHiddenRuneFailsTheLoad(t *testing.T) {
	for _, r := range agentRefRunes() {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			files := map[string]string{
				globalAgentFile("kept"): agentDoc("kept", "d"),
				supFile:                 "suppress: [" + quoteASCII("agent:a"+string(r)+"b") + "]\n",
			}
			msg := loadErr(t, files, supFile)
			if strings.ContainsRune(msg, r) {
				t.Errorf("error %q carries %U unescaped", msg, r)
			}
		})
	}
}

// The negative: visible neighbours of the hidden runes stay valid in a name.
func TestAgentReferencesAcceptTheVisibleNeighboursOfHiddenRunes(t *testing.T) {
	for _, r := range visibleNeighbours {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			target := "a" + string(r) + "b"
			mustLoad(t, map[string]string{
				globalAgentFile("ref"):  agentDoc("ref", "d", "status: deprecated", "replaced_by: "+quoteASCII("agent:"+target)),
				globalAgentFile(target): agentDoc(quoteASCII(target), "d"),
			})
		})
	}
}

func TestAgentWithCRLFLineEndingsLoadsAndKeepsItsBytes(t *testing.T) {
	orig := "---\r\nname: planner\r\ndescription: Plans work\r\ntools: Read\r\n---\r\n\r\nBody\r\nmore\r\n"
	for name, files := range map[string]map[string]string{
		"global":  {globalAgentFile("planner"): orig},
		"project": {projectAgentFile("planner"): orig},
		"local":   {localAgentFile("planner"): orig},
	} {
		t.Run(name, func(t *testing.T) {
			snap := loadLocal(t, files)
			a, ok := snap.AgentIn(govProject, "planner")
			if !ok {
				t.Fatal("a CRLF agent is not served")
			}
			if a.Name != "planner" || a.Description != "Plans work" {
				t.Errorf("agent = %+v, want name and description parsed through CRLF", a)
			}
			if string(a.Raw) != orig {
				t.Errorf("Raw = %q, want the CRLF bytes as written", a.Raw)
			}
		})
	}
}

func TestAgentFileOfExactlyTheCapLoadsAndOneByteMoreFails(t *testing.T) {
	const capBytes = 1 << 20
	pad := func(total int) string {
		head := "---\nname: big\ndescription: Big\n---\n"
		return head + strings.Repeat("x", total-len(head))
	}
	for name, file := range map[string]string{
		"global":  globalAgentFile("big"),
		"project": projectAgentFile("big"),
		"local":   localAgentFile("big"),
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadLocalErr(t, map[string]string{file: pad(capBytes + 1)}, file, "larger than 1048576 bytes")
			if len(msg) > 1<<10 {
				t.Errorf("error is %d bytes; it must not carry the content", len(msg))
			}
			snap := loadLocal(t, map[string]string{file: pad(capBytes)})
			a, ok := snap.AgentIn(govProject, "big")
			if !ok || len(a.Raw) != capBytes {
				t.Errorf("an agent of exactly the cap: found %v, %d bytes; want it served whole", ok, len(a.Raw))
			}
		})
	}
}

// The cap counts the bytes on disk, so CRLF is not shortened before it.
func TestAgentCapCountsTheBytesOnDisk(t *testing.T) {
	const capBytes = 1 << 20
	head := "---\r\nname: big\r\ndescription: Big\r\n---\r\n"
	body := strings.Repeat("x\r\n", (capBytes-len(head))/3)
	content := head + body + strings.Repeat("y", capBytes+1-len(head)-len(body))
	loadErr(t, map[string]string{globalAgentFile("big"): content}, globalAgentFile("big"), "larger than 1048576 bytes")
}

// --- Criterion 4: the Kind table ---

// A reference to an agent is valid once the agent Kind is loaded, and a Kind
// that is not in the table still is not.
func TestAgentReferencesAreValidAndOtherKindsAreNot(t *testing.T) {
	mustLoad(t, map[string]string{
		globalFile("old"):      govArticle("Old", "status: deprecated", "replaced_by: agent:new"),
		globalAgentFile("new"): agentDoc("new", "d"),
	})
	for _, kind := range []string{"skill", "loop", "instruction", "agent2", ""} {
		loadErr(t, map[string]string{
			globalFile("old"):      govArticle("Old", "status: deprecated", "replaced_by: "+kind+":new"),
			globalAgentFile("new"): agentDoc("new", "d"),
		}, globalFile("old"))
	}
}

// A Kind is written in lower-case letters (ADR 0021), agent as any other.
func TestAgentReferenceKindMustBeLowerCase(t *testing.T) {
	loadErr(t, map[string]string{
		globalFile("old"):      govArticle("Old", "status: deprecated", "replaced_by: Agent:new"),
		globalAgentFile("new"): agentDoc("new", "d"),
	}, globalFile("old"))
}

func TestAgentErrorsNameTheFileAndNoHostPath(t *testing.T) {
	root := writeFiles(t, map[string]string{globalAgentFile("x"): "---\nname: y\ndescription: d\n---\n"})
	_, err := engine.NewDirSource(root).Load(t.Context())
	if err == nil {
		t.Fatal("a name that differs from the slug loaded")
	}
	if want := globalAgentFile("x") + ": "; strings.Contains(err.Error(), root) || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error = %q, want prefix %q and no host path", err, want)
	}
}
