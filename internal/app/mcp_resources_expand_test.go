package app

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// expand fills a skill-file template the way an RFC 6570 client does: {name}
// is simple expansion, {+path} reserved expansion, which keeps slashes.
func expand(tmpl, name, path string) string {
	return strings.NewReplacer("{name}", name, "{+path}", path).Replace(tmpl)
}

func skillFileTemplateOf(t *testing.T, cs *mcp.ClientSession) string {
	t.Helper()
	res, err := cs.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, rt := range res.ResourceTemplates {
		if strings.HasPrefix(rt.URITemplate, "goldie://skills/") {
			return rt.URITemplate
		}
	}
	t.Fatal("no skill file template")
	return ""
}

// The URI a compliant client builds from the advertised template reads a
// nested file.
func TestMCPResourceReadOfAnExpandedTemplateAnswersANestedFile(t *testing.T) {
	cs := connectSkills(t, readerToken)
	tmpl := skillFileTemplateOf(t, cs)
	if !strings.Contains(tmpl, "{+path}") {
		t.Fatalf("template %q does not expand a path with slashes", tmpl)
	}
	text, _, err := readResource(cs, expand(tmpl, "deployer", "scripts/run.sh"))
	if err != nil || !strings.Contains(text, "echo hi") {
		t.Errorf("nested file: %q %v", text, err)
	}
}

// Over HTTP (stateless, one MCP server per request) a skill file, a nested
// one and an agent read, and a prompt answers.
func TestMCPPromptsAndResourcesOverHTTP(t *testing.T) {
	cs := mustConnectHTTP(t, skillSearchServer(t).URL+mcpEndpoint, withBearer(readerToken, http.DefaultTransport))
	tmpl := skillFileTemplateOf(t, cs)
	for uri, want := range map[string]string{
		expand(tmpl, "deployer", "SKILL.md"):       "# Deployer global",
		expand(tmpl, "deployer", "scripts/run.sh"): "echo hi",
		"goldie://agents/releaser":                 "Cuts zebrafruit releases",
	} {
		if text, _, err := readResource(cs, uri); err != nil || !strings.Contains(text, want) {
			t.Errorf("read %s over HTTP: want %q: %q %v", uri, want, text, err)
		}
	}
	if got := promptNames(t, cs); len(got) != 2 || got[0] != "goldie-canary" {
		t.Errorf("prompts over HTTP = %v", got)
	}
	if text, err := getPrompt(cs, "goldie-deployer"); err != nil || !strings.Contains(text, "# Deployer global") {
		t.Errorf("prompt over HTTP: %q %v", text, err)
	}
}
