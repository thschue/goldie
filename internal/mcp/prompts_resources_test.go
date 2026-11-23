package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Acceptance tests for #411 at the MCP protocol seam, against a fake API: the
// prompt list is fixed when a session starts, and what a Source says in a skill
// name, a SKILL.md or an agent cannot reach an agent unclean through a prompt
// or a resource. The answers of a real server are tested in internal/app
// (mcp_prompts_resources_test.go).
//
// list_changed: go-sdk v1.8.0 can send notifications/prompts/list_changed and
// notifications/resources/list_changed (the server does when a prompt or
// resource is added or removed and its capabilities say listChanged). The
// issue asks for it "when the SDK supports it", after a sync. Nothing in this
// component sees a sync: it reaches the API only through the client, and the
// API has no change event. So there is no observable trigger at this seam and
// no test for it; see the PR.

// skillsAPI is a fake API whose skill names can change while it runs.
type skillsAPI struct {
	names atomic.Pointer[[]string]
	body  string // the SKILL.md text and agent description
	srv   *httptest.Server
}

func newSkillsAPI(t *testing.T, body string, names ...string) *skillsAPI {
	t.Helper()
	a := &skillsAPI{body: body}
	a.names.Store(&names)
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.Contains(p, "/files/") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("# " + a.body + "\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		skill := func(n string) string {
			return `{"name":"` + jsonText(n) + `","files":[{"path":"SKILL.md","size":3}]}`
		}
		switch {
		case strings.HasSuffix(p, "/skills"):
			var list []string
			for _, n := range *a.names.Load() {
				list = append(list, skill(n))
			}
			_, _ = w.Write([]byte(`{"skills":[` + strings.Join(list, ",") + `]}`))
		case strings.Contains(p, "/agents/"):
			_, _ = w.Write([]byte(`{"name":"` + jsonText(a.body) + `","description":"` + jsonText(a.body) + `"}`))
		case strings.Contains(p, "/skills/"):
			_, _ = w.Write([]byte(skill(p[strings.LastIndex(p, "/")+1:])))
		default:
			_, _ = w.Write([]byte(`{"agents":[]}`))
		}
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *skillsAPI) set(names ...string) { a.names.Store(&names) }

func promptList(t *testing.T, cs *sdk.ClientSession) []string {
	t.Helper()
	res, err := cs.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatalf("prompts/list: %v", err)
	}
	var names []string
	for _, p := range res.Prompts {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	return names
}

// Criterion: the prompt list is fixed when a session starts. A skill added or
// removed on the server later changes neither the list nor what a call to
// prompts/get may name; a new session sees the change.
func TestPromptListIsFixedWhenTheSessionStarts(t *testing.T) {
	api := newSkillsAPI(t, "body", "alpha", "beta")
	cs := toolSession(t, api.srv.URL)
	want := []string{"goldie-alpha", "goldie-beta"}
	if got := promptList(t, cs); !slices.Equal(got, want) {
		t.Fatalf("prompts = %v, want %v", got, want)
	}

	api.set("alpha", "gamma")
	if got := promptList(t, cs); !slices.Equal(got, want) {
		t.Errorf("the list changed in the session: %v, want %v", got, want)
	}
	if res, err := cs.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "goldie-gamma"}); err == nil {
		t.Errorf("a skill added after the start is a prompt: %+v", res)
	}

	if got, want := promptList(t, toolSession(t, api.srv.URL)), []string{"goldie-alpha", "goldie-gamma"}; !slices.Equal(got, want) {
		t.Errorf("a new session lists %v, want %v", got, want)
	}
}

// A session with no skills lists no prompt, and is no error.
func TestPromptListIsEmptyWithoutSkills(t *testing.T) {
	cs := toolSession(t, newSkillsAPI(t, "body").srv.URL)
	if got := promptList(t, cs); len(got) != 0 {
		t.Errorf("prompts = %v, want none", got)
	}
}

// Abuse: a skill name comes from a Source. A prompt name is never forged with a
// line break, a hidden character or an escape sequence, and a name that is no
// path segment is no prompt.
func TestPromptNamesCannotCarryWhatASourceSays(t *testing.T) {
	api := newSkillsAPI(t, "body", "ok", hostile, "a/b", "..", "")
	for _, name := range promptList(t, toolSession(t, api.srv.URL)) {
		if !strings.HasPrefix(name, "goldie-") {
			t.Errorf("prompt %q lacks the goldie- prefix", name)
		}
		if strings.ContainsAny(name, "\n\r\u202e\u200b\x1b/\\") || strings.ContainsRune(name, '\U000E0049') || name == "goldie-" || name == "goldie-.." {
			t.Errorf("prompt name %q carries what a Source forged", name)
		}
	}
}

// Abuse: a SKILL.md is text from a Source; the prompt cannot carry a hidden
// character or an escape sequence to the agent.
func TestPromptGetCleansWhatASourceSays(t *testing.T) {
	api := newSkillsAPI(t, hostile, "deployer")
	cs := toolSession(t, api.srv.URL)
	res, err := cs.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "goldie-deployer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) == 0 {
		t.Fatal("no message")
	}
	var text string
	for _, m := range res.Messages {
		text += m.Content.(*sdk.TextContent).Text + "\n"
	}
	if strings.ContainsAny(text, "\u202e\u200b\x1b") || strings.ContainsRune(text, '\U000E0049') {
		t.Errorf("the prompt keeps a hidden or control character: %q", text)
	}
	if !strings.Contains(text, "forged") {
		t.Errorf("the prompt lacks the SKILL.md text: %q", text)
	}
}

// Abuse: the text of a SKILL.md and an agent, read as a resource, is cleaned
// the way the tools clean it.
func TestResourceReadCleansWhatASourceSays(t *testing.T) {
	api := newSkillsAPI(t, hostile, "deployer")
	cs := toolSession(t, api.srv.URL)
	for _, uri := range []string{"goldie://skills/deployer/SKILL.md", "goldie://agents/releaser"} {
		res, err := cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: uri})
		if err != nil {
			t.Errorf("read %s: %v", uri, err)
			continue
		}
		for _, c := range res.Contents {
			if strings.ContainsAny(c.Text, "\u202e\u200b\x1b") || strings.ContainsRune(c.Text, '\U000E0049') {
				t.Errorf("read %s keeps a hidden or control character: %q", uri, c.Text)
			}
			for _, line := range strings.Split(c.Text, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "- name: forged") {
					t.Errorf("read %s: a Source forged the line %q", uri, line)
				}
			}
		}
	}
}

// Abuse: a hostile URI is refused before any request to the API: a name or path
// that leaves the bundle is never forwarded.
func TestResourceReadRefusesHostileURIsBeforeAnyRequest(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/files/") || strings.Contains(r.URL.Path, "/agents/") {
			requests.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"skills":[],"agents":[]}`))
	}))
	t.Cleanup(srv.Close)
	cs := toolSession(t, srv.URL)
	for _, uri := range []string{
		"goldie://skills/../agents/releaser",
		"goldie://skills/deployer/../../articles/x",
		"goldie://skills/deployer/%2e%2e/x",
		"goldie://skills/./SKILL.md",
		"goldie://skills//SKILL.md",
		"goldie://skills/deployer/",
		"goldie://agents/..",
		"goldie://agents/.",
		"goldie://agents/a/b",
		"goldie://agents/%2e%2e",
		"goldie://agents/a%2Fb",
		"goldie://agents/a\\b",
		"goldie://agents/a\x00b",
	} {
		if res, err := cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("read %q: want an error, got %+v", uri, res)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the API got %d file or agent requests for hostile URIs, want none", n)
	}
}
