package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/contract"
	gmcp "github.com/thschue/goldie/internal/mcp"
)

// Acceptance tests for #411 (slice 7 of #15), the MCP protocol seam: one prompt
// per skill and the goldie:// resources, over the real public handler and the
// skills-search-content fixture (see skillSearchContent): the skills canary and
// deployer (global bundle: SKILL.md, page.html, scripts/run.sh; the Project
// github.com/acme/shop bundle: SKILL.md and checklist.txt), the agents canary-agent
// and releaser and a draft wip-rollout, and the article "Rollout guide".
//
// The contract they fix, beyond the issue:
//   - prompts/list names exactly one prompt per skill, goldie-<skill name>,
//     sorted as the skills are, and none for an agent or an article;
//   - prompts/get goldie-<name> returns the text of the skill's SKILL.md; the
//     session's default Project (WithProject) picks the bundle, as for the tools;
//     a name that is no prompt of the list is a protocol error that names no
//     address, token or path;
//   - resources/templates/list advertises goldie://skills/{name}/{path} and
//     goldie://agents/{name}, and resources/read answers them. A skill file is
//     read by its path in the bundle (nested paths too); an agent is read as its
//     name and description (the API answers no more);
//   - a miss (unknown skill, file or agent, a draft agent) is the protocol's
//     resource-not-found error. A URI that is malformed, of another scheme or
//     host, or that leaves the bundle is an error too, and never another
//     file's, agent's or article's content. Nothing in the URI selects a
//     Project: the session does.

// sessionFor is session with the options of the MCP server.
func sessionFor(t *testing.T, url, token string, opts ...gmcp.Option) *mcp.ClientSession {
	t.Helper()
	cl, err := contract.NewClientWithResponses(url, contract.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+token)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := gmcp.New(cl, opts...).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func promptNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatalf("prompts/list: %v", err)
	}
	var names []string
	for _, p := range res.Prompts {
		names = append(names, p.Name)
	}
	return names
}

func getPrompt(cs *mcp.ClientSession, name string) (string, error) {
	res, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: name})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, m := range res.Messages {
		if tc, ok := m.Content.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), nil
}

func readResource(cs *mcp.ClientSession, uri string) (string, *mcp.ResourceContents, error) {
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	for _, c := range res.Contents {
		b.WriteString(c.Text)
		b.Write(c.Blob)
	}
	if len(res.Contents) == 0 {
		return "", nil, nil
	}
	return b.String(), res.Contents[0], nil
}

// leaks are what no error of the prompts and resources may carry.
var promptResourceLeaks = []string{"127.0.0.1", "http://", "/api/", readerToken, "wrong-token"}

// Criterion: one prompt per skill, named goldie-<name>.
func TestMCPPromptsListOnePerSkill(t *testing.T) {
	cs := connectSkills(t, readerToken)
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
	if want := []string{"goldie-canary", "goldie-deployer"}; !slices.Equal(names, want) {
		t.Errorf("prompts = %v, want %v: one per skill, none for an agent or an article", names, want)
	}
	if caps := cs.InitializeResult().Capabilities; caps == nil || caps.Prompts == nil || caps.Resources == nil {
		t.Errorf("the server does not advertise prompts and resources: %+v", caps)
	}
}

// The prompt list follows the session's default Project, like the tools.
func TestMCPPromptsForAProjectSessionListItsSkills(t *testing.T) {
	cs := sessionFor(t, skillSearchServer(t).URL, readerToken, gmcp.WithProject("github.com/acme/shop", "test"))
	if got, want := promptNames(t, cs), []string{"goldie-canary", "goldie-deployer"}; !slices.Equal(got, want) {
		t.Errorf("prompts = %v, want %v", got, want)
	}
}

// Criterion: prompts/get returns the skill's content.
func TestMCPPromptGetReturnsTheSkillContent(t *testing.T) {
	cs := connectSkills(t, readerToken)
	res, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "goldie-deployer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) == 0 {
		t.Fatal("the prompt has no message")
	}
	text, err := getPrompt(cs, "goldie-deployer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# Deployer global") {
		t.Errorf("the prompt lacks the SKILL.md text: %q", text)
	}
	if strings.Contains(text, "Deployer shop") || strings.Contains(text, "alert(1)") {
		t.Errorf("the global prompt shows another bundle's or file's content: %q", text)
	}
}

func TestMCPPromptGetForAProjectSessionReturnsItsBundle(t *testing.T) {
	cs := sessionFor(t, skillSearchServer(t).URL, readerToken, gmcp.WithProject("github.com/acme/shop", "test"))
	text, err := getPrompt(cs, "goldie-deployer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# Deployer shop") || strings.Contains(text, "Deployer global") {
		t.Errorf("want the Project's SKILL.md, not the global one: %q", text)
	}
}

// Negative and abuse: a name that is no prompt is an error that leaks nothing
// and never returns another skill's, agent's or article's content.
func TestMCPPromptGetRefusesNamesThatAreNoPrompt(t *testing.T) {
	cs := connectSkills(t, readerToken)
	for _, name := range []string{
		"goldie-no-such-skill", "deployer", "", "goldie-", "goldie-..", "goldie-../deployer", "goldie-deployer/files/SKILL.md",
		"goldie-deployer%2Fpage.html", "goldie-deployer\x00", "goldie-releaser", "goldie-rollout-guide", "GOLDIE-DEPLOYER",
	} {
		text, err := getPrompt(cs, name)
		if err == nil {
			t.Errorf("prompt %q: want an error, got %q", name, text)
			continue
		}
		for _, bad := range append([]string{"# Deployer", "You are", "rollout-guide-body", "alert(1)"}, promptResourceLeaks...) {
			if strings.Contains(err.Error(), bad) || strings.Contains(text, bad) {
				t.Errorf("prompt %q: the answer leaks %q: %v", name, bad, err)
			}
		}
	}
}

// Criterion: the resources are advertised as the two URI templates.
func TestMCPResourceTemplatesAreAdvertised(t *testing.T) {
	cs := connectSkills(t, readerToken)
	res, err := cs.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, rt := range res.ResourceTemplates {
		got = append(got, rt.URITemplate)
		if rt.Name == "" {
			t.Errorf("resource template %s has no name", rt.URITemplate)
		}
	}
	slices.Sort(got)
	if want := []string{"goldie://agents/{name}", "goldie://skills/{name}/{+path}"}; !slices.Equal(got, want) {
		t.Errorf("resource templates = %v, want %v", got, want)
	}
	// resources/list answers; whatever it lists lies under the two roots,
	// except the one fixed resource goldie://instructions (#562).
	list, err := cs.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("resources/list: %v", err)
	}
	for _, r := range list.Resources {
		if r.URI != "goldie://instructions" && !strings.HasPrefix(r.URI, "goldie://skills/") && !strings.HasPrefix(r.URI, "goldie://agents/") {
			t.Errorf("resources/list names %q, outside goldie://skills/, goldie://agents/ and goldie://instructions", r.URI)
		}
	}
}

// Criterion: goldie://skills/{name}/{path} reads a file of the skill's bundle.
func TestMCPResourceReadAnswersASkillFile(t *testing.T) {
	cs := connectSkills(t, readerToken)
	for uri, want := range map[string]string{
		"goldie://skills/deployer/SKILL.md":       "# Deployer global",
		"goldie://skills/deployer/scripts/run.sh": "echo hi",
		"goldie://skills/canary/SKILL.md":         "# Canary",
	} {
		text, c, err := readResource(cs, uri)
		if err != nil {
			t.Errorf("read %s: %v", uri, err)
			continue
		}
		if !strings.Contains(text, want) {
			t.Errorf("read %s: want %q, got %q", uri, want, text)
		}
		if c.URI != uri {
			t.Errorf("read %s: the contents carry the URI %q", uri, c.URI)
		}
	}
}

// Abuse: a file is data. Whatever its name, it is never served as a page.
func TestMCPResourceReadDoesNotServeAFileAsHTML(t *testing.T) {
	cs := connectSkills(t, readerToken)
	text, c, err := readResource(cs, "goldie://skills/deployer/page.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "alert(1)") || strings.Contains(strings.ToLower(c.MIMEType), "html") {
		t.Errorf("want the bytes with a mime type that is not html: %q %q", c.MIMEType, text)
	}
}

// The session's default Project picks the bundle; the URI cannot.
func TestMCPResourceReadForAProjectSessionAnswersItsBundle(t *testing.T) {
	cs := sessionFor(t, skillSearchServer(t).URL, readerToken, gmcp.WithProject("github.com/acme/shop", "test"))
	for uri, want := range map[string]string{
		"goldie://skills/deployer/SKILL.md":      "# Deployer shop",
		"goldie://skills/deployer/checklist.txt": "check it",
	} {
		if text, _, err := readResource(cs, uri); err != nil || !strings.Contains(text, want) {
			t.Errorf("read %s: want %q: %q %v", uri, want, text, err)
		}
	}
	if text, _, err := readResource(cs, "goldie://skills/deployer/page.html"); err == nil {
		t.Errorf("the global bundle's page.html answers for the Project: %q", text)
	}
}

// Abuse: a query or a fragment is not a way to pick a Project, or another file.
func TestMCPResourceReadIgnoresNothingInTheURIThatSelectsAProject(t *testing.T) {
	cs := connectSkills(t, readerToken)
	for _, uri := range []string{
		"goldie://skills/deployer/SKILL.md?project=github.com/acme/shop",
		"goldie://skills/deployer/SKILL.md#github.com/acme/shop",
	} {
		text, _, err := readResource(cs, uri)
		if err == nil && strings.Contains(text, "Deployer shop") {
			t.Errorf("read %s: the URI selected the Project: %q", uri, text)
		}
	}
}

// Criterion: goldie://agents/{name} reads an agent.
func TestMCPResourceReadAnswersAnAgent(t *testing.T) {
	cs := connectSkills(t, readerToken)
	text, _, err := readResource(cs, "goldie://agents/releaser")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"releaser", "Cuts zebrafruit releases"} {
		if !strings.Contains(text, want) {
			t.Errorf("the agent lacks %q: %q", want, text)
		}
	}
	if strings.Contains(text, "canary-agent") || strings.Contains(text, "Watches canaries") {
		t.Errorf("the answer shows another agent: %q", text)
	}
}

func resourceNotFound(t *testing.T, err error) {
	t.Helper()
	var je *jsonrpc.Error
	if !errors.As(err, &je) || je.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("want the resource-not-found protocol error, got %v", err)
	}
}

// Negative: a miss is resource-not-found, with no address, path or token.
func TestMCPResourceReadMissIsResourceNotFound(t *testing.T) {
	cs := connectSkills(t, readerToken)
	for _, uri := range []string{
		"goldie://skills/no-such-skill/SKILL.md",
		"goldie://skills/deployer/missing.txt",
		"goldie://skills/deployer/scripts/missing.sh",
		"goldie://agents/no-such-agent",
		"goldie://agents/wip-rollout", // a draft is never served
		"goldie://agents/deployer",    // a skill is no agent
		"goldie://skills/releaser/SKILL.md",
	} {
		text, _, err := readResource(cs, uri)
		if err == nil {
			t.Errorf("read %s: want an error, got %q", uri, text)
			continue
		}
		resourceNotFound(t, err)
		for _, bad := range append([]string{"wip-rollout-body", "Not ready"}, promptResourceLeaks...) {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("read %s: the error leaks %q: %v", uri, bad, err)
			}
		}
	}
}

// Abuse: a URI that leaves the bundle, is empty in a part, or is of another
// scheme or host is an error, and no other file, agent or article answers.
func TestMCPResourceReadRefusesHostileAndMalformedURIs(t *testing.T) {
	cs := connectSkills(t, readerToken)
	for _, uri := range []string{
		"",
		"goldie://",
		"goldie://skills",
		"goldie://skills/",
		"goldie://skills/deployer",
		"goldie://skills/deployer/",
		"goldie://skills//SKILL.md",
		"goldie://skills/deployer/../deployer/SKILL.md",
		"goldie://skills/deployer/../../articles/rollout-guide",
		"goldie://skills/deployer/../../agents/releaser",
		"goldie://skills/../agents/releaser",
		"goldie://skills/deployer/%2e%2e/%2e%2e/articles/rollout-guide",
		"goldie://skills/deployer/..%2f..%2farticles%2frollout-guide",
		"goldie://skills/deployer/./SKILL.md",
		"goldie://skills/deployer//etc/passwd",
		"goldie://skills/deployer/\\SKILL.md",
		"goldie://skills/deployer/SKILL.md\x00",
		"goldie://skills/deployer%2Fscripts/run.sh",
		"goldie://skills/deployer/files/SKILL.md",
		"goldie://agents",
		"goldie://agents/",
		"goldie://agents/..",
		"goldie://agents/../skills/deployer/SKILL.md",
		"goldie://agents/releaser/extra",
		"goldie://agents/releaser/..",
		"goldie://agents/%2e%2e",
		"goldie://articles/rollout-guide",
		"goldie://unknown/deployer",
		"goldie:/skills/deployer/SKILL.md",
		"goldie:skills/deployer/SKILL.md",
		"GOLDIE://skills/deployer/SKILL.md/../../../etc/passwd",
		"http://127.0.0.1/api/v1/skills/deployer",
		"https://skills/deployer/SKILL.md",
		"file:///etc/passwd",
		"skills/deployer/SKILL.md",
		"goldie://user:pw@skills/deployer/SKILL.md",
	} {
		text, _, err := readResource(cs, uri)
		if err == nil {
			t.Errorf("read %q: want an error, got %q", uri, text)
			continue
		}
		for _, bad := range append([]string{"rollout-guide-body", "You are", "root:", "# Deployer", "echo hi"}, promptResourceLeaks...) {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("read %q: the error leaks %q: %v", uri, bad, err)
			}
		}
	}
}

// Abuse: a wrong token is no way to read a prompt or a resource, and the
// errors leak nothing.
func TestMCPPromptsAndResourcesWithAWrongTokenLeakNothing(t *testing.T) {
	cs := connectSkills(t, "wrong-token-0123456789abcdef")
	var seen []string
	if res, err := cs.ListPrompts(context.Background(), nil); err != nil {
		seen = append(seen, err.Error())
	} else if len(res.Prompts) != 0 {
		t.Errorf("a wrong token lists prompts: %v", res.Prompts)
	}
	if text, err := getPrompt(cs, "goldie-deployer"); err == nil {
		t.Errorf("a wrong token gets the prompt: %q", text)
	} else {
		seen = append(seen, err.Error())
	}
	for _, uri := range []string{"goldie://skills/deployer/SKILL.md", "goldie://agents/releaser"} {
		if text, _, err := readResource(cs, uri); err == nil {
			t.Errorf("a wrong token reads %s: %q", uri, text)
		} else {
			seen = append(seen, err.Error())
		}
	}
	for _, s := range seen {
		for _, bad := range promptResourceLeaks {
			if strings.Contains(s, bad) {
				t.Errorf("error leaks %q: %s", bad, s)
			}
		}
	}
}

// Abuse: an unreachable API does not stop the session, and its address leaks
// nowhere.
func TestMCPPromptsAndResourcesWithAnUnreachableAPIDoNotLeakTheAddress(t *testing.T) {
	cs := session(t, "http://127.0.0.1:1", readerToken)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) == 0 {
		t.Fatalf("the tools are gone with the API unreachable: %v", err)
	}
	var seen []string
	if res, err := cs.ListPrompts(context.Background(), nil); err != nil {
		seen = append(seen, err.Error())
	} else if len(res.Prompts) != 0 {
		t.Errorf("prompts listed with no API: %v", res.Prompts)
	}
	for _, uri := range []string{"goldie://skills/deployer/SKILL.md", "goldie://agents/releaser"} {
		if text, _, err := readResource(cs, uri); err == nil {
			t.Errorf("read %s with no API: %q", uri, text)
		} else {
			seen = append(seen, err.Error())
		}
	}
	for _, s := range seen {
		if strings.Contains(s, "127.0.0.1") || strings.Contains(s, "/api/") {
			t.Errorf("error leaks the address: %s", s)
		}
	}
}
