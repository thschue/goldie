package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/cli"
	gmcp "github.com/thschue/goldie/internal/mcp"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #220: goldie mcp over stdio defaults to the Project of
// the checkout it runs in, found the way the CLI finds it (GOLDIE_PROJECT,
// then .goldie/config.yaml, then the git remotes upstream and origin), once at
// startup. search_knowledge and get_article fall back to it when project is
// left out, and current_context names it and its source with the words
// `goldie status` uses. The cut is RunMCP over pipes, started in a temporary
// git checkout, against the real public handler with the Project content of
// testdata/content (github.com/acme/shop).
//
// It also pins #341: an empty project counts as omitted, and a
// whitespace-only one is refused before any request.

// The words `goldie status` prints for each source (internal/cli/project.go).
const (
	wordsEnv      = "GOLDIE_PROJECT"
	wordsConfig   = ".goldie/config.yaml"
	wordsUpstream = "git remote upstream"
	wordsOrigin   = "git remote origin"
)

// leakyRemote is a remote URL detection rejects (a nested group), with a
// credential and a private host that must never be printed.
const leakyRemote = "https://leak-user:leak-secret@git.internal.example/grp/sub/repo.git"

// checkoutLeaks are the parts of the checkouts below that no text may carry.
var checkoutLeaks = []string{"leak-user", "leak-secret", "git.internal.example", "grp/sub", "git@", "leak-content-marker"}

// gitCheckout makes a temporary git checkout with files, paths relative to
// its root, beside .git/HEAD. It returns the root.
func gitCheckout(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	all := map[string]string{".git/HEAD": "ref: refs/heads/main\n", ".git/config": "[core]\n\trepositoryformatversion = 0\n"}
	for name, body := range files {
		all[name] = body
	}
	for name, body := range all {
		writeCheckoutFile(t, dir, name, body)
	}
	return dir
}

func writeCheckoutFile(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// gitRemotes is a .git/config with the given remotes, name to URL.
func gitRemotes(remotes map[string]string) string {
	var b strings.Builder
	b.WriteString("[core]\n\trepositoryformatversion = 0\n")
	for _, name := range []string{"upstream", "origin"} {
		if u, ok := remotes[name]; ok {
			b.WriteString("[remote \"" + name + "\"]\n\turl = " + u + "\n")
		}
	}
	return b.String()
}

// outsideRepository is an empty temporary directory with no git repository
// in it or above it.
func outsideRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			t.Fatalf("the temporary directory %s is inside a git repository at %s; set TMPDIR outside one", dir, d)
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

// projectServer is the real public handler over testdata/content, by its
// absolute path, so the tests can change the working directory.
func projectServer(t *testing.T) string {
	t.Helper()
	content, err := filepath.Abs(testContent)
	if err != nil {
		t.Fatal(err)
	}
	return newServerWith(t, platform.Config{ContentDir: content}).URL
}

// projectEnv is the environment RunMCP sees. A test may change it after
// startup.
type projectEnv struct {
	mu   sync.Mutex
	vars map[string]string
}

func (e *projectEnv) lookup(k string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.vars[k]
	return v, ok
}

func (e *projectEnv) set(k, v string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vars[k] = v
}

// recordedOut is RunMCP's stdout: it passes every byte on to the client and
// keeps a copy.
type recordedOut struct {
	w   io.WriteCloser
	buf lockedBuffer
}

func (o *recordedOut) Write(p []byte) (int, error) {
	_, _ = o.buf.Write(p)
	return o.w.Write(p)
}

func (o *recordedOut) Close() error { return o.w.Close() }

// projectStdio is a running goldie mcp --transport stdio session.
type projectStdio struct {
	cs      *mcp.ClientSession
	env     *projectEnv
	stdout  *recordedOut  // every byte RunMCP wrote to its stdout
	results *lockedBuffer // everything RunMCP wrote to env.Out
}

// stdioIn changes the working directory to dir, runs RunMCP there against
// addr with testToken and the environment vars, and connects an MCP client to
// its stdin and stdout. RunMCP must keep running until the session closes.
func stdioIn(t *testing.T, dir, addr string, vars map[string]string) *projectStdio {
	t.Helper()
	t.Chdir(dir)
	if vars == nil {
		vars = map[string]string{}
	}
	p := &projectStdio{env: &projectEnv{vars: vars}, results: &lockedBuffer{}}
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	p.stdout = &recordedOut{w: serverOut}
	ctx, cancel := context.WithCancel(context.Background())
	env := cli.Env{Lookup: p.env.lookup, ReadFile: os.ReadFile, Out: p.results}
	done := make(chan error, 1)
	go func() {
		done <- RunMCP(ctx, []string{"--transport", "stdio", "--addr", addr, "--token", testToken}, env, serverIn, p.stdout)
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: clientIn, Writer: clientOut}, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect to RunMCP: %v", err)
	}
	p.cs = cs
	t.Cleanup(func() {
		_ = cs.Close()
		cancel()
		_ = clientOut.Close()
		_ = clientIn.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("RunMCP: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("RunMCP did not return after the session closed")
		}
	})
	return p
}

// pathsOf are dir as given and with its symbolic links resolved.
func pathsOf(t *testing.T, dir string) []string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return []string{dir, real}
}

// assertDefaultContext checks current_context's text with a default Project:
// it names the Project and the source in status's words, does not say there
// is no default, and no longer tells the agent to pass project itself.
func assertDefaultContext(t *testing.T, what, text string, isErr bool, id, source string) {
	t.Helper()
	if isErr || !strings.Contains(text, id) || !strings.Contains(text, source) {
		t.Errorf("%s: current_context: tool error %v, want %s and its source %q named:\n%s", what, isErr, id, source, text)
	}
	low := strings.ToLower(text)
	if strings.Contains(low, "no default") || strings.Contains(low, "problem") {
		t.Errorf("%s: current_context with the default %s says there is none or a problem:\n%s", what, id, text)
	}
	for _, old := range []string{"pass it as project", "to scope an answer to it"} {
		if strings.Contains(low, old) {
			t.Errorf("%s: with a default, current_context still tells the agent to pass project explicitly (%q):\n%s", what, old, text)
		}
	}
	if !strings.Contains(text, "search_knowledge") || !strings.Contains(text, "get_article") {
		t.Errorf("%s: current_context does not say which tools use the default:\n%s", what, text)
	}
}

// assertProjectAnswer checks that search_knowledge and get_article without
// project answer for id: the Project's article for the registered
// github.com/acme/shop, the global answer with the API's unregistered hint
// for any other id, and the global answer without a hint for "".
func assertProjectAnswer(t *testing.T, what string, cs *mcp.ClientSession, args map[string]any, id string) {
	t.Helper()
	for tool, a := range map[string]map[string]any{
		"search_knowledge": {"query": "kubernetes"},
		"get_article":      {"id": "platform/deploy"},
	} {
		for k, v := range args {
			a[k] = v
		}
		text, isErr := call(t, cs, tool, a)
		label := what + "/" + tool
		switch id {
		case "github.com/acme/shop":
			if isErr || !strings.Contains(text, "Shop deploy") || strings.Contains(text, "Deploying to Kubernetes") {
				t.Errorf("%s: tool error %v, want the Project's article for %s and not the global one:\n%s", label, isErr, id, text)
			}
			if strings.Contains(text, "not registered") {
				t.Errorf("%s: carries the unregistered hint for the registered %s:\n%s", label, id, text)
			}
		case "":
			if isErr || !strings.Contains(text, "Deploying to Kubernetes") || strings.Contains(text, "Shop deploy") {
				t.Errorf("%s: tool error %v, want the global answer only:\n%s", label, isErr, text)
			}
			if strings.Contains(text, "not registered") {
				t.Errorf("%s: carries an unregistered hint without a Project:\n%s", label, text)
			}
		default:
			if isErr || !strings.Contains(text, wantUnregisteredHint(id)) || !strings.Contains(text, "Deploying to Kubernetes") {
				t.Errorf("%s: tool error %v, want the global answer with the API's hint for %s:\n%s", label, isErr, id, text)
			}
		}
	}
}

// Criterion 1: from a temporary git checkout, search_knowledge and get_article
// without project resolve for the Project of GOLDIE_PROJECT, then
// .goldie/config.yaml, then the remote upstream, then origin, in the CLI's
// order; current_context names the Project and its source. The cases where
// the higher source names the unregistered github.com/acme/none prove the
// order: the registered Project below it is not used.
func TestMCPStdioDefaultsToTheDetectedProjectInTheCLIsOrder(t *testing.T) {
	addr := projectServer(t)
	for _, c := range []struct {
		name   string
		env    map[string]string
		files  map[string]string
		subdir string
		id     string
		source string
	}{
		{"GOLDIE_PROJECT over everything", map[string]string{platform.EnvProject: "github.com/acme/shop"}, map[string]string{
			".goldie/config.yaml": "project: github.com/acme/none\n",
			".git/config":         gitRemotes(map[string]string{"upstream": "https://github.com/acme/none.git", "origin": "https://github.com/acme/none.git"}),
		}, "", "github.com/acme/shop", wordsEnv},
		{"GOLDIE_PROJECT beats a registered config", map[string]string{platform.EnvProject: "github.com/acme/none"}, map[string]string{
			".goldie/config.yaml": "project: github.com/acme/shop\n",
		}, "", "github.com/acme/none", wordsEnv},
		{"GOLDIE_PROJECT as a remote URL with a credential", map[string]string{platform.EnvProject: "https://leak-user:leak-secret@github.com/acme/shop.git"}, nil,
			"", "github.com/acme/shop", wordsEnv},
		{"config over the remotes", nil, map[string]string{
			".goldie/config.yaml": "project: github.com/acme/shop\n",
			".git/config":         gitRemotes(map[string]string{"upstream": "https://github.com/acme/none.git", "origin": leakyRemote}),
		}, "", "github.com/acme/shop", wordsConfig},
		{"config beats a registered upstream", nil, map[string]string{
			".goldie/config.yaml": "project: github.com/acme/none\n",
			".git/config":         gitRemotes(map[string]string{"upstream": "git@github.com:acme/shop.git"}),
		}, "", "github.com/acme/none", wordsConfig},
		{"config from a subdirectory of the checkout", nil, map[string]string{
			".goldie/config.yaml": "project: github.com/acme/shop\n",
			"src/deep/.keep":      "",
		}, "src/deep", "github.com/acme/shop", wordsConfig},
		{"upstream over origin", nil, map[string]string{
			".git/config": gitRemotes(map[string]string{"upstream": "git@github.com:acme/shop.git", "origin": "https://github.com/acme/none.git"}),
		}, "", "github.com/acme/shop", wordsUpstream},
		{"upstream beats a registered origin", nil, map[string]string{
			".git/config": gitRemotes(map[string]string{"upstream": "https://github.com/acme/none.git", "origin": "https://github.com/acme/shop.git"}),
		}, "", "github.com/acme/none", wordsUpstream},
		{"origin alone, with a credential", nil, map[string]string{
			".git/config": gitRemotes(map[string]string{"origin": "https://leak-user:leak-secret@github.com/acme/shop.git"}),
		}, "", "github.com/acme/shop", wordsOrigin},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := gitCheckout(t, c.files)
			p := stdioIn(t, filepath.Join(dir, filepath.FromSlash(c.subdir)), addr, c.env)
			text, isErr := call(t, p.cs, "current_context", map[string]any{})
			assertDefaultContext(t, c.name, text, isErr, c.id, c.source)
			assertLeaksNoAddress(t, c.name, text, append(append(pathsOf(t, dir), strings.TrimPrefix(addr, "http://")), checkoutLeaks...)...)
			assertProjectAnswer(t, c.name, p.cs, nil, c.id)
		})
	}
}

// Criterion 1 with the shared fixture: the checkout of #107, where every
// source names github.com/acme/shop, defaults to it from .goldie/config.yaml,
// and current_context prints neither the directory, the remote URLs and
// their credential, the address nor the token.
func TestMCPStdioFromTheProjectCheckoutLeaksNothing(t *testing.T) {
	addr := projectServer(t)
	dir := projectCheckout(t)
	p := stdioIn(t, dir, addr, nil)
	text, isErr := call(t, p.cs, "current_context", map[string]any{})
	assertDefaultContext(t, "project checkout", text, isErr, "github.com/acme/shop", wordsConfig)
	assertLeaksNoAddress(t, "project checkout", text, append(append(pathsOf(t, dir), strings.TrimPrefix(addr, "http://")), checkoutLeaks...)...)
	assertProjectAnswer(t, "project checkout", p.cs, nil, "github.com/acme/shop")
}

// Criterion 2: outside a repository, with no GOLDIE_PROJECT, there is no
// default: current_context says so and to pass project as host/org/repo, and
// the tools give the global answer.
func TestMCPStdioOutsideARepositoryHasNoDefault(t *testing.T) {
	addr := projectServer(t)
	dir := outsideRepository(t)
	p := stdioIn(t, dir, addr, nil)
	text, isErr := call(t, p.cs, "current_context", map[string]any{})
	assertNoDefaultProject(t, "outside a repository", text, isErr)
	assertLeaksNoAddress(t, "outside a repository", text, append(pathsOf(t, dir), strings.TrimPrefix(addr, "http://"))...)
	assertProjectAnswer(t, "outside a repository", p.cs, nil, "")
	// Negative: the session works, so an explicit project still scopes.
	assertProjectAnswer(t, "outside a repository, explicit project", p.cs, map[string]any{"project": "github.com/acme/shop"}, "github.com/acme/shop")
}

// Criterion 2: a checkout where nothing names a Project (no config, no
// remote) has no default either.
func TestMCPStdioCheckoutWithoutAProjectHasNoDefault(t *testing.T) {
	addr := projectServer(t)
	p := stdioIn(t, gitCheckout(t, nil), addr, nil)
	text, isErr := call(t, p.cs, "current_context", map[string]any{})
	assertNoDefaultProject(t, "checkout without a Project", text, isErr)
	assertProjectAnswer(t, "checkout without a Project", p.cs, nil, "")
}

// brokenConfigs are .goldie/config.yaml files detection must refuse, each
// with the words `goldie status` prints for it on its Project line (#190).
// None of the problem words may echo the file's content or a path.
func brokenConfigs() []struct{ name, body, want string } {
	return []struct{ name, body, want string }{
		{"malformed YAML", "project: [unclosed leak-content-marker\n",
			".goldie/config.yaml is not valid YAML with a string `project:` key"},
		{"project is not a string", "project: {leak-content-marker: 1}\n",
			".goldie/config.yaml is not valid YAML with a string `project:` key"},
		{"project not host/org/repo", "project: leak-content-marker\n",
			".goldie/config.yaml: the Project is not host/org/repo"},
		{"project a rejected URL with a credential", "project: " + leakyRemote + "\n",
			".goldie/config.yaml: the Project is not a host/org/repo name or a git remote URL"},
		{"project with a bidi override", "project: \"github.com/acme/leak-content-marker\\u202e\"\n",
			".goldie/config.yaml: the Project is not a host/org/repo name or a git remote URL"},
	}
}

// brokenCheckout is a checkout of the registered Project by its upstream
// remote, whose .goldie/config.yaml is body: a broken config is obeyed, not
// skipped in favour of the remote.
func brokenCheckout(t *testing.T, body string) string {
	t.Helper()
	return gitCheckout(t, map[string]string{
		".goldie/config.yaml": body,
		".git/config":         gitRemotes(map[string]string{"upstream": "git@github.com:acme/shop.git", "origin": leakyRemote}),
	})
}

// assertProblemContext checks current_context's text for a Project problem:
// a reply, not a tool error, that reports the problem in status's words and
// names no Project.
func assertProblemContext(t *testing.T, what, text string, isErr bool, want string) {
	t.Helper()
	if isErr || !strings.Contains(strings.ToLower(text), "problem") || !strings.Contains(text, want) {
		t.Errorf("%s: current_context: tool error %v, want the problem reported as %q:\n%s", what, isErr, want, text)
	}
	if strings.Contains(text, "github.com/acme/shop") {
		t.Errorf("%s: current_context names a Project despite the problem:\n%s", what, text)
	}
}

// Criterion 3: a broken .goldie/config.yaml doesn't end the session.
// current_context reports the problem in the words status uses, and
// search_knowledge and get_article without project fail with it, as search
// and lookup do in the CLI; nothing names the directory or the file's
// content. Calls that do not rely on the default still answer.
func TestMCPStdioBrokenConfigIsReportedAndFailsDefaultedCalls(t *testing.T) {
	addr := projectServer(t)
	for _, c := range brokenConfigs() {
		t.Run(c.name, func(t *testing.T) {
			dir := brokenCheckout(t, c.body)
			p := stdioIn(t, dir, addr, nil)
			leaks := append(append(pathsOf(t, dir), strings.TrimPrefix(addr, "http://"), "\u202e", "unclosed"), checkoutLeaks...)

			text, isErr := call(t, p.cs, "current_context", map[string]any{})
			assertProblemContext(t, c.name, text, isErr, c.want)
			assertLeaksNoAddress(t, c.name, text, leaks...)

			for tool, args := range map[string]map[string]any{
				"search_knowledge": {"query": "kubernetes"},
				"get_article":      {"id": "platform/deploy"},
			} {
				text, isErr := call(t, p.cs, tool, args)
				if !isErr || !strings.Contains(text, c.want) {
					t.Errorf("%s/%s without project: tool error %v, want it to fail with %q:\n%s", c.name, tool, isErr, c.want, text)
				}
				if strings.Contains(text, "Deploying to Kubernetes") || strings.Contains(text, "Shop deploy") {
					t.Errorf("%s/%s without project: answered despite the problem:\n%s", c.name, tool, text)
				}
				assertLeaksNoAddress(t, c.name+"/"+tool, text, leaks...)
			}

			// The session goes on: calls that do not rely on the default answer.
			assertProjectAnswer(t, c.name+", explicit project", p.cs, map[string]any{"project": "github.com/acme/shop"}, "github.com/acme/shop")
			if text, isErr := call(t, p.cs, "list_projects", map[string]any{}); isErr || !strings.Contains(text, "github.com/acme/shop") {
				t.Errorf("%s: list_projects: tool error %v, want github.com/acme/shop:\n%s", c.name, isErr, text)
			}
		})
	}
}

// Criterion 3: an unreadable .goldie/config.yaml (here a directory) is a
// problem reported the same way.
func TestMCPStdioUnreadableConfigIsReported(t *testing.T) {
	addr := projectServer(t)
	dir := gitCheckout(t, map[string]string{".goldie/config.yaml/leak-content-marker": "x"})
	p := stdioIn(t, dir, addr, nil)
	const want = "cannot read .goldie/config.yaml"
	text, isErr := call(t, p.cs, "current_context", map[string]any{})
	assertProblemContext(t, "unreadable config", text, isErr, want)
	assertLeaksNoAddress(t, "unreadable config", text, append(pathsOf(t, dir), checkoutLeaks...)...)
	if text, isErr := call(t, p.cs, "search_knowledge", map[string]any{"query": "kubernetes"}); !isErr || !strings.Contains(text, want) {
		t.Errorf("search_knowledge without project: tool error %v, want it to fail with %q:\n%s", isErr, want, text)
	}
}

// Criterion 3: a call that fails on the Project problem sends no request,
// as the CLI's search and lookup fail before any; with an explicit project
// the request is sent.
func TestMCPStdioBrokenConfigFailsBeforeAnyRequest(t *testing.T) {
	addr, requests := countingStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
	})
	p := stdioIn(t, brokenCheckout(t, "project: [unclosed\n"), addr, nil)
	for tool, args := range map[string]map[string]any{
		"search_knowledge": {"query": "kubernetes"},
		"get_article":      {"id": "platform/deploy"},
	} {
		if text, isErr := call(t, p.cs, tool, args); !isErr {
			t.Errorf("%s without project: want a tool error, got %s", tool, text)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the API got %d requests for calls that fail on the Project problem, want none", n)
	}
	if text, isErr := call(t, p.cs, "search_knowledge", map[string]any{"query": "kubernetes", "project": "github.com/acme/shop"}); isErr {
		t.Errorf("search_knowledge with an explicit project: tool error %s", text)
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("the API got %d requests, want 1 for the explicit project", n)
	}
}

// Criterion 4: detection runs once, at startup. Changing the checkout's
// config, its remotes, GOLDIE_PROJECT and even the working directory after
// startup, before the first call, changes neither current_context nor the
// Project the tools answer for.
func TestMCPStdioDetectsTheProjectOnceAtStartup(t *testing.T) {
	addr := projectServer(t)
	for _, c := range []struct {
		name   string
		change func(t *testing.T, p *projectStdio, dir string)
	}{
		{"config changed", func(t *testing.T, _ *projectStdio, dir string) {
			writeCheckoutFile(t, dir, ".goldie/config.yaml", "project: github.com/acme/none\n")
		}},
		{"config broken", func(t *testing.T, _ *projectStdio, dir string) {
			writeCheckoutFile(t, dir, ".goldie/config.yaml", "project: [unclosed\n")
		}},
		{"config removed, remotes changed", func(t *testing.T, _ *projectStdio, dir string) {
			if err := os.RemoveAll(filepath.Join(dir, ".goldie", "config.yaml")); err != nil {
				t.Fatal(err)
			}
			writeCheckoutFile(t, dir, ".git/config", gitRemotes(map[string]string{"upstream": "https://github.com/acme/none.git"}))
		}},
		{"GOLDIE_PROJECT set", func(t *testing.T, p *projectStdio, _ string) {
			p.env.set(platform.EnvProject, "github.com/acme/none")
		}},
		{"working directory changed", func(t *testing.T, _ *projectStdio, _ string) {
			t.Chdir(gitCheckout(t, map[string]string{".goldie/config.yaml": "project: github.com/acme/none\n"}))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := gitCheckout(t, map[string]string{".goldie/config.yaml": "project: github.com/acme/shop\n"})
			p := stdioIn(t, dir, addr, nil)
			c.change(t, p, dir)
			for round := range 2 {
				text, isErr := call(t, p.cs, "current_context", map[string]any{})
				assertDefaultContext(t, c.name, text, isErr, "github.com/acme/shop", wordsConfig)
				assertProjectAnswer(t, c.name, p.cs, nil, "github.com/acme/shop")
				if round == 0 {
					c.change(t, p, dir) // and again between calls
				}
			}
		})
	}
}

// Negative for criterion 4: a problem found at startup stays until the next
// start, even when the config is fixed meanwhile, and a session started
// without a default does not gain one.
func TestMCPStdioStartupStateIsKeptForTheSession(t *testing.T) {
	addr := projectServer(t)
	t.Run("broken at startup, fixed after", func(t *testing.T) {
		dir := brokenCheckout(t, "project: [unclosed\n")
		p := stdioIn(t, dir, addr, nil)
		writeCheckoutFile(t, dir, ".goldie/config.yaml", "project: github.com/acme/shop\n")
		text, isErr := call(t, p.cs, "current_context", map[string]any{})
		assertProblemContext(t, "fixed after startup", text, isErr, ".goldie/config.yaml is not valid YAML")
		if text, isErr := call(t, p.cs, "search_knowledge", map[string]any{"query": "kubernetes"}); !isErr {
			t.Errorf("search_knowledge without project after the fix: want the startup problem, got %s", text)
		}
	})
	t.Run("no default at startup, checkout after", func(t *testing.T) {
		p := stdioIn(t, outsideRepository(t), addr, nil)
		t.Chdir(projectCheckout(t))
		p.env.set(platform.EnvProject, "github.com/acme/shop")
		text, isErr := call(t, p.cs, "current_context", map[string]any{})
		assertNoDefaultProject(t, "checkout after startup", text, isErr)
		assertProjectAnswer(t, "checkout after startup", p.cs, nil, "")
	})
}

// Criterion 4: a remote that exists but is not host/org/repo shows up by
// name only: current_context says there is no default and names the remote,
// never its URL, credential or host, and the tools give the global answer.
func TestMCPStdioRejectedRemoteShowsUpByNameOnly(t *testing.T) {
	addr := projectServer(t)
	for _, c := range []struct {
		name    string
		remotes map[string]string
		words   string
	}{
		{"origin", map[string]string{"origin": leakyRemote}, wordsOrigin},
		{"upstream", map[string]string{"upstream": "ssh://leak-user@git.internal.example:2222/grp/sub/repo.git"}, wordsUpstream},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := gitCheckout(t, map[string]string{".git/config": gitRemotes(c.remotes)})
			p := stdioIn(t, dir, addr, nil)
			text, isErr := call(t, p.cs, "current_context", map[string]any{})
			assertNoDefaultProject(t, "rejected "+c.name, text, isErr)
			if !strings.Contains(text, c.words) {
				t.Errorf("rejected %s: current_context does not name the remote as %q:\n%s", c.name, c.words, text)
			}
			assertLeaksNoAddress(t, "rejected "+c.name, text, append(append(pathsOf(t, dir), strings.TrimPrefix(addr, "http://"), "2222"), checkoutLeaks...)...)
			assertProjectAnswer(t, "rejected "+c.name, p.cs, nil, "")
		})
	}
}

// Criterion 5: nothing but MCP messages is ever written to stdout: every
// line RunMCP writes is one JSON-RPC 2.0 message, through a session with a
// default, a broken config, a rejected remote and no repository, and nothing
// at all goes to the results writer.
func TestMCPStdioWritesOnlyMCPMessagesToStdout(t *testing.T) {
	addr := projectServer(t)
	for name, dir := range map[string]func(t *testing.T) string{
		"default":       projectCheckout,
		"broken config": func(t *testing.T) string { return brokenCheckout(t, "project: [unclosed leak-content-marker\n") },
		"rejected remote": func(t *testing.T) string {
			return gitCheckout(t, map[string]string{".git/config": gitRemotes(map[string]string{"origin": leakyRemote})})
		},
		"no repository": outsideRepository,
	} {
		t.Run(name, func(t *testing.T) {
			p := stdioIn(t, dir(t), addr, nil)
			for tool, args := range map[string]map[string]any{
				"current_context":  {},
				"list_projects":    {},
				"search_knowledge": {"query": "kubernetes"},
				"get_article":      {"id": "platform/deploy"},
			} {
				call(t, p.cs, tool, args)
			}
			lines := p.stdout.buf.lines()
			if len(lines) == 0 {
				t.Fatal("RunMCP wrote nothing to stdout")
			}
			for _, l := range lines {
				var msg struct {
					JSONRPC string `json:"jsonrpc"`
				}
				if err := json.Unmarshal([]byte(l), &msg); err != nil || msg.JSONRPC != "2.0" {
					t.Errorf("%s: stdout carries a line that is not an MCP message: %q", name, l)
				}
			}
			if got := p.results.lines(); len(got) != 0 {
				t.Errorf("%s: RunMCP wrote to the results writer: %q", name, got)
			}
		})
	}
}

// Criterion 6: with a stdio default, an explicit project beats it, in both
// tools; mixed case is lower-cased as before.
func TestMCPStdioExplicitProjectBeatsTheDefault(t *testing.T) {
	addr := projectServer(t)
	p := stdioIn(t, projectCheckout(t), addr, nil)
	assertProjectAnswer(t, "explicit unregistered project", p.cs, map[string]any{"project": "github.com/acme/none"}, "github.com/acme/none")
	assertProjectAnswer(t, "explicit mixed-case project", p.cs, map[string]any{"project": "GitHub.com/ACME/Shop"}, "github.com/acme/shop")
	// The other way round: an explicit registered project beats an
	// unregistered default.
	q := stdioIn(t, gitCheckout(t, map[string]string{".goldie/config.yaml": "project: github.com/acme/none\n"}), addr, nil)
	assertProjectAnswer(t, "explicit registered project", q.cs, map[string]any{"project": "github.com/acme/shop"}, "github.com/acme/shop")
	assertProjectAnswer(t, "unregistered default", q.cs, nil, "github.com/acme/none")
}

// whitespaceProjects are project values made only of whitespace.
var whitespaceProjects = map[string]string{
	"one space":   " ",
	"spaces":      "    ",
	"tab":         "\t",
	"newline":     "\n",
	"mixed":       " \t\r\n ",
	"long spaces": strings.Repeat(" ", 4096),
}

// Criterion 6: with a stdio default, an empty project counts as omitted, so
// the default applies; a whitespace-only one is refused before any request,
// with the hint naming list_projects.
func TestMCPStdioEmptyProjectTakesTheDefaultAndWhitespaceIsRefused(t *testing.T) {
	addr := projectServer(t)
	p := stdioIn(t, projectCheckout(t), addr, nil)
	assertProjectAnswer(t, "empty project with a default", p.cs, map[string]any{"project": ""}, "github.com/acme/shop")

	stub, requests := countingStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
	})
	q := stdioIn(t, projectCheckout(t), stub, nil)
	for name, ws := range whitespaceProjects {
		for tool, args := range map[string]map[string]any{
			"search_knowledge": {"query": "kubernetes", "project": ws},
			"get_article":      {"id": "platform/deploy", "project": ws},
		} {
			text, isErr := call(t, q.cs, tool, args)
			assertInvalidProjectError(t, "with a default/"+name+"/"+tool, text, isErr)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the API got %d requests for whitespace-only projects, want none", n)
	}
}

// queryStub is a stub API that records the query of every request.
func queryStub(t *testing.T) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var queries []string
	addr := mcpStub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"a","name":"A","keywords":[],"content":"body"}`))
	})
	return addr, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), queries...)
	}
}

// Criterion 6 (#341): without a default, an empty project sends the request
// with no project parameter and no error; a whitespace-only one is refused
// before any request, with the hint naming list_projects.
func TestMCPEmptyProjectWithoutADefaultSendsNoProject(t *testing.T) {
	addr, queries := queryStub(t)
	cs := projectSession(t, addr, testToken)
	for tool, args := range map[string]map[string]any{
		"search_knowledge": {"query": "kubernetes", "project": ""},
		"get_article":      {"id": "a", "project": ""},
	} {
		if text, isErr := call(t, cs, tool, args); isErr {
			t.Errorf("%s with an empty project: tool error %s", tool, text)
		}
	}
	got := queries()
	if len(got) != 2 {
		t.Fatalf("the API got %d requests, want 2: %q", len(got), got)
	}
	for _, q := range got {
		if strings.Contains(q, "project") {
			t.Errorf("an empty project was sent as a parameter: %q", q)
		}
	}
	for name, ws := range whitespaceProjects {
		for tool, args := range map[string]map[string]any{
			"search_knowledge": {"query": "kubernetes", "project": ws},
			"get_article":      {"id": "a", "project": ws},
		} {
			text, isErr := call(t, cs, tool, args)
			assertInvalidProjectError(t, "without a default/"+name+"/"+tool, text, isErr)
		}
	}
	if n := len(queries()); n != 2 {
		t.Errorf("the API got %d requests after the whitespace-only projects, want still 2", n)
	}
}

// Criterion 6: the omitted-project fallback lives with current_context: a
// server given a default by gmcp.WithProject answers search_knowledge and
// get_article without project for it, sends it as the project parameter, and
// current_context no longer tells the agent to pass project explicitly.
func TestMCPWithProjectIsTheFallbackForAnOmittedProject(t *testing.T) {
	cs := projectSession(t, projectServer(t), testToken, gmcp.WithProject("github.com/acme/shop", wordsConfig))
	text, isErr := call(t, cs, "current_context", map[string]any{})
	assertDefaultContext(t, "WithProject", text, isErr, "github.com/acme/shop", wordsConfig)
	assertProjectAnswer(t, "WithProject", cs, nil, "github.com/acme/shop")

	addr, queries := queryStub(t)
	cs = projectSession(t, addr, testToken, gmcp.WithProject("github.com/acme/shop", wordsConfig))
	call(t, cs, "search_knowledge", map[string]any{"query": "kubernetes"})
	call(t, cs, "get_article", map[string]any{"id": "a", "project": ""})
	call(t, cs, "get_article", map[string]any{"id": "a", "project": "github.com/acme/none"})
	want := []string{"project=github.com%2Facme%2Fshop", "project=github.com%2Facme%2Fshop", "project=github.com%2Facme%2Fnone"}
	got := queries()
	if len(got) != len(want) {
		t.Fatalf("the API got %d requests, want %d: %q", len(got), len(want), got)
	}
	for i, q := range got {
		if !strings.Contains(q, want[i]) && !strings.Contains(q, strings.ReplaceAll(want[i], "%2F", "/")) {
			t.Errorf("request %d: query %q, want %q", i+1, q, want[i])
		}
	}
}

// Criterion 7: over HTTP the server never detects a Project, even from a
// checkout with a broken .goldie/config.yaml: current_context reports no
// default and no problem, and the tools give the global answer.
func TestMCPOverHTTPIgnoresABrokenConfigInTheServersWorkingDirectory(t *testing.T) {
	addr := projectServer(t)
	dir := brokenCheckout(t, "project: [unclosed leak-content-marker\n")
	t.Chdir(dir)
	cs := mustConnectHTTP(t, addr+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	text, isErr := call(t, cs, "current_context", map[string]any{})
	assertNoDefaultProject(t, "over HTTP, broken config", text, isErr)
	if strings.Contains(strings.ToLower(text), "problem") || strings.Contains(text, ".goldie") {
		t.Errorf("over HTTP: current_context reports the server's working directory:\n%s", text)
	}
	assertLeaksNoAddress(t, "over HTTP, broken config", text, append(append(pathsOf(t, dir), strings.TrimPrefix(addr, "http://")), checkoutLeaks...)...)
	assertProjectAnswer(t, "over HTTP, broken config", cs, nil, "")
}
