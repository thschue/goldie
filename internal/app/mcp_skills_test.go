package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Acceptance tests for #410 (slice 6 of #15), the MCP protocol seam: the tools
// find_skills, get_skill, list_skills and list_agents, over the real public
// handler and the skills-search-content fixture (see skillSearchContent): the
// skills canary and deployer (keywords rollout and ship; the global bundle has
// page.html and scripts/run.sh), the agents canary-agent and releaser and a
// draft wip-rollout, the article "Rollout guide", the word zebrafruit only in
// descriptions, and a Project github.com/acme/shop whose deployer bundle
// (SKILL.md and checklist.txt) shadows the global one.
//
// The contract they fix, beyond the issue:
//   - the four tools are advertised next to the four existing ones, and no
//     others: there is no agent-by-name tool;
//   - find_skills takes query and an optional project, like search_knowledge,
//     and answers skills only (no article, no agent), matching keywords and
//     names, never descriptions (ADR 0005); no match is not an error;
//   - get_skill takes name and an optional project and answers the skill's
//     name, its file paths and the text of its SKILL.md, so an agent can use
//     it; a miss is a tool error with a hint;
//   - list_skills and list_agents take an optional project only and answer
//     every name in the Project's view, sorted; an agent carries its
//     description; a draft is never listed;
//   - an unregistered Project's answer carries the server's hint;
//   - errors name no address, token or path.

func connectSkills(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	return session(t, skillSearchServer(t).URL, token)
}

func TestMCPSkillToolsAreAdvertised(t *testing.T) {
	res, err := connectSkills(t, readerToken).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
		if tl.Description == "" {
			t.Errorf("tool %s has no description", tl.Name)
		}
	}
	slices.Sort(names)
	want := []string{"current_context", "find_skills", "get_article", "get_skill", "list_agents", "list_projects", "list_skills", "search_knowledge"}
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
}

func TestMCPSkillToolInputSchemas(t *testing.T) {
	res, err := connectSkills(t, readerToken).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"find_skills": "query", "get_skill": "name", "list_skills": "", "list_agents": ""}
	seen := 0
	for _, tl := range res.Tools {
		arg, ok := want[tl.Name]
		if !ok {
			continue
		}
		seen++
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Type       string `json:"type"`
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: input schema: %v", tl.Name, err)
		}
		wantProps, wantRequired := 1, []string(nil)
		if arg != "" {
			wantProps, wantRequired = 2, []string{arg}
			if schema.Properties[arg].Type != "string" {
				t.Errorf("%s: %q is not a string property: %s", tl.Name, arg, raw)
			}
		}
		if schema.Type != "object" || len(schema.Properties) != wantProps || schema.Properties["project"].Type != "string" ||
			!slices.Equal(schema.Required, wantRequired) {
			t.Errorf("%s: want an optional string project and required %v, got %s", tl.Name, wantRequired, raw)
		}
	}
	if seen != len(want) {
		t.Errorf("saw %d of the %d tools", seen, len(want))
	}
}

// Criterion: find_skills finds skills by keyword, and only skills.
func TestMCPFindSkillsAnswersSkillsOnly(t *testing.T) {
	cs := connectSkills(t, readerToken)
	text, isErr := call(t, cs, "find_skills", map[string]any{"query": "rollout"})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	if !strings.Contains(text, "deployer") {
		t.Errorf("the skill deployer (keyword rollout) is missing: %s", text)
	}
	for _, other := range []string{"canary", "releaser", "rollout-guide", "Rollout guide", "wip-rollout"} {
		if strings.Contains(text, other) {
			t.Errorf("find_skills answered %q, which is no skill that matches: %s", other, text)
		}
	}
}

// Negative: a description is not searched (ADR 0005); no match is an answer.
func TestMCPFindSkillsDoesNotMatchDescriptionsAndAnEmptyAnswerIsNotAnError(t *testing.T) {
	cs := connectSkills(t, readerToken)
	for _, q := range []string{"zebrafruit", "zzzqqq"} {
		text, isErr := call(t, cs, "find_skills", map[string]any{"query": q})
		if isErr || !strings.Contains(text, "No skills") || strings.Contains(text, "deployer") {
			t.Errorf("query %q: want an empty answer naming no skill, not an error: %v %s", q, isErr, text)
		}
	}
}

// Criterion: find_skills takes a project.
func TestMCPFindSkillsAcceptsAProject(t *testing.T) {
	cs := connectSkills(t, readerToken)
	text, isErr := call(t, cs, "find_skills", map[string]any{"query": "rollout", "project": "github.com/acme/shop"})
	if isErr || !strings.Contains(text, "deployer") {
		t.Errorf("want deployer for the Project: %v %s", isErr, text)
	}
}

// Criterion: get_skill reads one skill: name, files and SKILL.md.
func TestMCPGetSkillAnswersNameFilesAndSkillText(t *testing.T) {
	cs := connectSkills(t, readerToken)
	text, isErr := call(t, cs, "get_skill", map[string]any{"name": "deployer"})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	for _, want := range []string{"deployer", "SKILL.md", "scripts/run.sh", "page.html", "# Deployer global"} {
		if !strings.Contains(text, want) {
			t.Errorf("get_skill lacks %q: %s", want, text)
		}
	}
	if strings.Contains(text, "checklist.txt") || strings.Contains(text, "Deployer shop") {
		t.Errorf("the global answer shows the shop bundle: %s", text)
	}
}

// Criterion: the Project's bundle answers whole.
func TestMCPGetSkillForAProjectAnswersItsBundle(t *testing.T) {
	cs := connectSkills(t, readerToken)
	text, isErr := call(t, cs, "get_skill", map[string]any{"name": "deployer", "project": "github.com/acme/shop"})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	for _, want := range []string{"SKILL.md", "checklist.txt", "# Deployer shop"} {
		if !strings.Contains(text, want) {
			t.Errorf("get_skill for the Project lacks %q: %s", want, text)
		}
	}
	for _, gone := range []string{"scripts/run.sh", "page.html", "Deployer global"} {
		if strings.Contains(text, gone) {
			t.Errorf("get_skill for the Project shows %q of the global bundle: %s", gone, text)
		}
	}
}

// Negative: a miss is a tool error with a hint, with no address or path.
func TestMCPGetSkillMissIsAToolErrorWithHint(t *testing.T) {
	cs := connectSkills(t, readerToken)
	text, isErr := call(t, cs, "get_skill", map[string]any{"name": "no-such-skill"})
	if !isErr || !strings.Contains(text, "\nHint: ") || !strings.Contains(text, "list_skills") {
		t.Errorf("want a tool error with a hint to list_skills: %v %s", isErr, text)
	}
	for _, bad := range []string{"127.0.0.1", "http://", "/api/"} {
		if strings.Contains(text, bad) {
			t.Errorf("error leaks %q: %s", bad, text)
		}
	}
}

// Abuse: a name that is empty, dots, or leaves the skill is never another
// endpoint's answer, and no file or article content comes back.
func TestMCPGetSkillRefusesNamesThatEscape(t *testing.T) {
	cs := connectSkills(t, readerToken)
	for _, name := range []string{"", ".", "..", "deployer/scripts", "../articles/rollout-guide", "deployer/files/SKILL.md", "deployer%2Fpage.html", "deployer\x00"} {
		text, isErr := call(t, cs, "get_skill", map[string]any{"name": name})
		if !isErr {
			t.Errorf("name %q: want a tool error, got %s", name, text)
		}
		for _, leak := range []string{"# Deployer", "rollout-guide-body", "alert(1)", "echo hi"} {
			if strings.Contains(text, leak) {
				t.Errorf("name %q: the answer leaks %q: %s", name, leak, text)
			}
		}
	}
}

// Criterion: list_skills lists every skill of the view, sorted.
func TestMCPListSkillsListsEverySkillSorted(t *testing.T) {
	cs := connectSkills(t, readerToken)
	text, isErr := call(t, cs, "list_skills", nil)
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	c, d := strings.Index(text, "canary"), strings.Index(text, "deployer")
	if c < 0 || d < 0 || c > d {
		t.Errorf("want canary then deployer: %s", text)
	}
	for _, other := range []string{"releaser", "canary-agent", "rollout-guide"} {
		if strings.Contains(text, other) {
			t.Errorf("list_skills lists %q, which is no skill: %s", other, text)
		}
	}
}

// Criterion: list_agents lists every agent of the view, sorted, with its
// description; a draft is not listed.
func TestMCPListAgentsListsEveryAgentSortedWithoutDrafts(t *testing.T) {
	cs := connectSkills(t, readerToken)
	text, isErr := call(t, cs, "list_agents", nil)
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	c, r := strings.Index(text, "canary-agent"), strings.Index(text, "releaser")
	if c < 0 || r < 0 || c > r {
		t.Errorf("want canary-agent then releaser: %s", text)
	}
	for _, want := range []string{"Watches canaries", "Cuts zebrafruit releases"} {
		if !strings.Contains(text, want) {
			t.Errorf("list_agents lacks the description %q: %s", want, text)
		}
	}
	for _, bad := range []string{"wip-rollout", "Not ready", "deployer"} {
		if strings.Contains(text, bad) {
			t.Errorf("list_agents shows %q: %s", bad, text)
		}
	}
}

// Criterion: an unregistered Project gets the global answer and the server's hint.
func TestMCPListToolsForAnUnregisteredProjectCarryTheHint(t *testing.T) {
	cs := connectSkills(t, readerToken)
	for tool, want := range map[string]string{"list_skills": "deployer", "list_agents": "releaser"} {
		text, isErr := call(t, cs, tool, map[string]any{"project": "github.com/acme/none"})
		if isErr || !strings.Contains(text, want) || !strings.Contains(text, "not registered") {
			t.Errorf("%s: want the global list and the hint: %v %s", tool, isErr, text)
		}
	}
}

// Abuse: a project that is not a Project id is a tool error pointing to
// list_projects, for every new tool.
func TestMCPSkillToolsRefuseAnInvalidProject(t *testing.T) {
	cs := connectSkills(t, readerToken)
	for tool, args := range map[string]map[string]any{
		"find_skills": {"query": "rollout"},
		"get_skill":   {"name": "deployer"},
		"list_skills": {},
		"list_agents": {},
	} {
		args["project"] = "not a project"
		text, isErr := call(t, cs, tool, args)
		if !isErr || !strings.Contains(text, "list_projects") || strings.Contains(text, "not a project") {
			t.Errorf("%s: want a tool error pointing to list_projects, echoing nothing: %v %s", tool, isErr, text)
		}
	}
}

var skillToolCalls = map[string]map[string]any{
	"find_skills": {"query": "rollout"},
	"get_skill":   {"name": "deployer"},
	"list_skills": nil,
	"list_agents": nil,
}

// Abuse: a wrong token is the 401 message as a tool error, leaking nothing.
func TestMCPSkillToolsWithAWrongTokenLeakNothing(t *testing.T) {
	cs := connectSkills(t, "wrong-token-0123456789abcdef")
	for tool, args := range skillToolCalls {
		text, isErr := call(t, cs, tool, args)
		if !isErr || !strings.Contains(text, "missing or invalid API token") || !strings.Contains(text, "Authorization") {
			t.Errorf("%s: want the 401 message and hint as a tool error: %v %s", tool, isErr, text)
		}
		for _, bad := range []string{"127.0.0.1", "http://", "wrong-token", readerToken, "/api/"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: error leaks %q: %s", tool, bad, text)
			}
		}
	}
}

// Abuse: an unreachable API does not leak its address.
func TestMCPSkillToolsUnreachableAPIDoNotLeakTheAddress(t *testing.T) {
	cs := session(t, "http://127.0.0.1:1", readerToken)
	for tool, args := range skillToolCalls {
		text, isErr := call(t, cs, tool, args)
		if !isErr || strings.Contains(text, "127.0.0.1") || strings.Contains(text, "/api/") {
			t.Errorf("%s: want a tool error without the address: %v %s", tool, isErr, text)
		}
	}
}
