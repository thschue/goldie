package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #107 over streamable HTTP: /mcp has the Project
// tools, there is no default Project, and the server never detects one from
// its own working directory or environment.

// projectCheckout makes a git checkout of the registered Project
// github.com/acme/shop in every way detection looks: .goldie/config.yaml and
// the upstream and origin remotes. The origin URL carries a credential that
// must never be printed.
func projectCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		".git/HEAD": "ref: refs/heads/main\n",
		".git/config": "[core]\n\trepositoryformatversion = 0\n" +
			"[remote \"upstream\"]\n\turl = git@github.com:acme/shop.git\n" +
			"[remote \"origin\"]\n\turl = https://leak-user:leak-secret@github.com/acme/shop.git\n",
		".goldie/config.yaml": "project: github.com/acme/shop\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Criterion 3: over HTTP, current_context says there is no default and tells
// the agent to pass project as host/org/repo.
func TestMCPOverHTTPCurrentContextHasNoDefault(t *testing.T) {
	srv := newServer(t)
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	text, isErr := call(t, cs, "current_context", map[string]any{})
	assertNoDefaultProject(t, "over HTTP", text, isErr)
	assertLeaksNoAddress(t, "over HTTP", text, strings.TrimPrefix(srv.URL, "http://"))
}

// Criterion 5: a server started inside a checkout of a registered Project,
// with GOLDIE_PROJECT naming it, still has no default over HTTP: current_context
// says so, and search_knowledge and get_article without project give the
// global answer. current_context prints neither the directory, the remote
// URLs and their credential, the address nor the token.
func TestMCPOverHTTPNeverDetectsAProjectFromTheServersWorkingDirectory(t *testing.T) {
	content, err := filepath.Abs(testContent)
	if err != nil {
		t.Fatal(err)
	}
	dir := projectCheckout(t)
	t.Chdir(dir)
	t.Setenv(platform.EnvProject, "github.com/acme/shop")
	srv := newServerWith(t, platform.Config{ContentDir: content})
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))

	text, isErr := call(t, cs, "current_context", map[string]any{})
	assertNoDefaultProject(t, "from a checkout", text, isErr)
	assertLeaksNoAddress(t, "from a checkout", text, strings.TrimPrefix(srv.URL, "http://"), dir, content,
		"leak-user", "leak-secret", "git@", ".git", ".goldie")

	if text, isErr := call(t, cs, "get_article", map[string]any{"id": "platform/deploy"}); isErr ||
		!strings.Contains(text, "Deploying to Kubernetes") || strings.Contains(text, "Shop deploy") {
		t.Errorf("get_article without project from a checkout: tool error %v, want the global article:\n%s", isErr, text)
	}
	if text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "kubernetes"}); isErr ||
		!strings.Contains(text, "Deploying to Kubernetes") || strings.Contains(text, "Shop deploy") {
		t.Errorf("search_knowledge without project from a checkout: tool error %v, want the global results:\n%s", isErr, text)
	}
	// Negative: the Project is registered, so passing it does scope the answer.
	if text, isErr := call(t, cs, "get_article", map[string]any{"id": "platform/deploy", "project": "github.com/acme/shop"}); isErr ||
		!strings.Contains(text, "Shop deploy") {
		t.Errorf("get_article with the Project from a checkout: tool error %v, want the Project's article:\n%s", isErr, text)
	}
}

// Criteria 1 and 2 over HTTP: list_projects lists the registered Projects, and
// an invalid project is refused with a hint to list_projects.
func TestMCPOverHTTPProjectTools(t *testing.T) {
	srv := newServer(t)
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	if text, isErr := call(t, cs, "list_projects", map[string]any{}); isErr || !strings.Contains(text, "github.com/acme/shop") {
		t.Errorf("list_projects over HTTP: tool error %v, want github.com/acme/shop:\n%s", isErr, text)
	}
	for name, project := range hostileProjects {
		text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "kubernetes", "project": project})
		assertInvalidProjectError(t, "over HTTP/"+name, text, isErr)
	}
	text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "kubernetes", "project": "github.com/acme/none"})
	if isErr || !strings.Contains(text, wantUnregisteredHint("github.com/acme/none")) {
		t.Errorf("search_knowledge over HTTP with an unregistered project: tool error %v, want the API's hint:\n%s", isErr, text)
	}
}
