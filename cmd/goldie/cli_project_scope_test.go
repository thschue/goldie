package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #121: search and lookup send the resolved Project as
// project=, and an unregistered Project gets global guidance only plus the
// server's hint, which status shows too. The server is the real handler over
// the example content, which registers github.com/acme/shop and holds a shop
// article platform/deploy that overrides the global one.

const (
	registeredProject   = "github.com/acme/shop"
	unregisteredProject = "github.com/acme/nothere"
)

// scopedServer is the real server behind a recorder of every request's path
// and project parameter. A request without the parameter records "<none>".
type scopedServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string // "<path> <project>"
}

func newScopedServer(t *testing.T) *scopedServer {
	t.Helper()
	h, err := app.PublicHandler(platform.Config{ContentDir: "../../internal/app/testdata/content", APIToken: cliToken, RateLimit: platform.DefaultRateLimit()}, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	s := &scopedServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := "<none>"
		if r.URL.Query().Has("project") {
			p = r.URL.Query().Get("project")
		}
		s.mu.Lock()
		s.seen = append(s.seen, r.URL.Path+" "+p)
		s.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// projects returns the project values the server received on search and
// article requests, in order. Status's own probe search is not counted: the
// callers that use it run search and lookup only.
func (s *scopedServer) projects() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.seen {
		if strings.HasPrefix(e, "/api/v1/search ") || strings.HasPrefix(e, "/api/v1/articles/") {
			out = append(out, e[strings.LastIndex(e, " ")+1:])
		}
	}
	return out
}

func (s *scopedServer) reset() {
	s.mu.Lock()
	s.seen = nil
	s.mu.Unlock()
}

// runScoped runs the CLI in dir with GOLDIE_PROJECT set to env (unset when
// empty) and returns its output.
func runScoped(t *testing.T, dir, env string, args ...string) (string, error) {
	t.Helper()
	for _, name := range []string{"GOLDIE_ADDR", "GOLDIE_TOKEN", "GOLDIE_TOKEN_FILE", "GOLDIE_PROJECT"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	if env != "" {
		t.Setenv("GOLDIE_PROJECT", env)
	}
	t.Chdir(dir)
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), err
}

func scopedArgs(srv *scopedServer, args ...string) []string {
	return append(args, "--addr", srv.URL, "--token", cliToken)
}

func onlyProject(t *testing.T, srv *scopedServer, want string) {
	t.Helper()
	got := srv.projects()
	if len(got) == 0 {
		t.Fatalf("the server received no search or lookup request")
	}
	for _, p := range got {
		if p != want {
			t.Errorf("server received project=%q, want %q (all: %q)", p, want, got)
		}
	}
}

func TestCLISearchSendsTheDetectedProject(t *testing.T) {
	srv := newScopedServer(t)
	dir := gitRepo(t, "[remote \"origin\"]\n\turl = git@github.com:Acme/Shop.git\n")
	if _, err := runScoped(t, dir, "", scopedArgs(srv, "search", "kubernetes")...); err != nil {
		t.Fatal(err)
	}
	onlyProject(t, srv, registeredProject) // lower-cased, as ADR 0020 has clients send it
}

func TestCLILookupSendsTheDetectedProject(t *testing.T) {
	srv := newScopedServer(t)
	dir := gitRepo(t, "[remote \"origin\"]\n\turl = https://github.com/acme/shop.git\n")
	if _, err := runScoped(t, dir, "", scopedArgs(srv, "lookup", "platform/deploy")...); err != nil {
		t.Fatal(err)
	}
	onlyProject(t, srv, registeredProject)
}

func TestCLISendsNoProjectWhenNoneIsFound(t *testing.T) {
	srv := newScopedServer(t)
	for _, args := range [][]string{{"search", "kubernetes"}, {"lookup", "platform/deploy"}} {
		srv.reset()
		out, err := runScoped(t, t.TempDir(), "", scopedArgs(srv, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		onlyProject(t, srv, "<none>")
		if strings.Contains(strings.ToLower(out), "not registered") {
			t.Errorf("%v with no Project shows the unregistered hint:\n%s", args, out)
		}
	}
}

func TestCLISearchAndLookupUseTheDetectionOverrideOrder(t *testing.T) {
	srv := newScopedServer(t)
	dir := gitRepo(t, "[remote \"origin\"]\n\turl = git@github.com:me/detected.git\n[remote \"upstream\"]\n\turl = git@github.com:acme/forked.git\n")
	cfgDir := filepath.Join(dir, ".goldie")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(cfgDir, "config.yaml")
	for _, cmd := range [][]string{{"search", "kubernetes"}, {"lookup", "platform/deploy"}} {
		steps := []struct {
			label, env string
			file       bool
			extra      []string
			want       string
		}{
			{"flag beats env, file and remote", "ex.com/env/p", true, []string{"--project", "ex.com/flag/p"}, "ex.com/flag/p"},
			{"env beats file and remote", "ex.com/env/p", true, nil, "ex.com/env/p"},
			{"file beats remote", "", true, nil, "ex.com/file/p"},
			{"upstream beats origin", "", false, nil, "github.com/acme/forked"},
		}
		for _, st := range steps {
			_ = os.Remove(cfg)
			if st.file {
				if err := os.WriteFile(cfg, []byte("project: ex.com/file/p\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			srv.reset()
			args := append(append([]string{}, cmd...), st.extra...)
			if _, err := runScoped(t, dir, st.env, scopedArgs(srv, args...)...); err != nil {
				t.Fatalf("%s %s: %v", cmd[0], st.label, err)
			}
			t.Log(cmd[0], st.label)
			onlyProject(t, srv, st.want)
		}
	}
}

func TestCLISearchAcceptsProjectFlagAndLowerCasesIt(t *testing.T) {
	srv := newScopedServer(t)
	if _, err := runScoped(t, t.TempDir(), "", scopedArgs(srv, "search", "kubernetes", "--project", "git@GitHub.com:Acme/Shop.git")...); err != nil {
		t.Fatal(err)
	}
	onlyProject(t, srv, registeredProject)
}

// hit is the part of a search result these tests read.
type hit struct{ ID, Name, Scope string }

func searchHits(t *testing.T, out string) (hits []hit, hint string) {
	t.Helper()
	var m struct {
		Results []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Scope string `json:"scope"`
		} `json:"results"`
		Hint *string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("search --json is not JSON: %v\n%s", err, out)
	}
	for _, r := range m.Results {
		hits = append(hits, hit{r.ID, r.Name, r.Scope})
	}
	if m.Hint != nil {
		hint = *m.Hint
	}
	return hits, hint
}

func TestCLISearchGivesProjectScopedResultsForARegisteredProject(t *testing.T) {
	srv := newScopedServer(t)
	out, err := runScoped(t, t.TempDir(), "", scopedArgs(srv, "search", "kubernetes", "--json", "--project", registeredProject)...)
	if err != nil {
		t.Fatal(err)
	}
	hits, hint := searchHits(t, out)
	if hint != "" {
		t.Errorf("registered Project got hint %q", hint)
	}
	if len(hits) == 0 || hits[0].Scope != registeredProject || hits[0].Name != "Shop deploy" {
		t.Errorf("first hit = %+v, want the shop article with the Project as its scope", hits)
	}
	text, err := runScoped(t, t.TempDir(), "", scopedArgs(srv, "search", "kubernetes", "--project", registeredProject)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Shop deploy") {
		t.Errorf("text search lacks the project article:\n%s", text)
	}
	if strings.Contains(strings.ToLower(text), "not registered") {
		t.Errorf("registered Project shows the unregistered hint:\n%s", text)
	}
}

func TestCLILookupGivesTheProjectArticleForARegisteredProject(t *testing.T) {
	srv := newScopedServer(t)
	out, err := runScoped(t, t.TempDir(), "", scopedArgs(srv, "lookup", "platform/deploy", "--project", registeredProject)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Shop-specific deploy.") || strings.Contains(out, "Use the platform chart") {
		t.Errorf("lookup did not give the project's article:\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), "not registered") {
		t.Errorf("registered Project shows the unregistered hint:\n%s", out)
	}
	// Without a Project the global article answers, so the difference above is
	// the Project and nothing else.
	out, err = runScoped(t, t.TempDir(), "", scopedArgs(srv, "lookup", "platform/deploy")...)
	if err != nil || !strings.Contains(out, "Use the platform chart") {
		t.Errorf("global lookup = %q, %v", out, err)
	}
}

func TestCLISearchUnregisteredProjectGetsGlobalItemsAndTheHint(t *testing.T) {
	srv := newScopedServer(t)
	args := scopedArgs(srv, "search", "kubernetes", "--project", unregisteredProject)

	text, err := runScoped(t, t.TempDir(), "", args...)
	if err != nil {
		t.Fatalf("an unregistered Project is not an error: %v", err)
	}
	onlyProject(t, srv, unregisteredProject)
	if !strings.Contains(text, "Deploying to Kubernetes") || strings.Contains(text, "Shop deploy") {
		t.Errorf("want the global Items only:\n%s", text)
	}
	if !strings.Contains(strings.ToLower(text), "not registered") {
		t.Errorf("output lacks the unregistered hint:\n%s", text)
	}

	out, err := runScoped(t, t.TempDir(), "", append(args, "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	hits, hint := searchHits(t, out)
	if !strings.Contains(strings.ToLower(hint), "not registered") {
		t.Errorf("--json hint = %q", hint)
	}
	if len(hits) == 0 {
		t.Fatal("no global hits")
	}
	for _, h := range hits {
		if h.Scope != "global" {
			t.Errorf("hit %+v is not global", h)
		}
	}
}

func TestCLILookupUnregisteredProjectGetsTheGlobalArticleAndTheHint(t *testing.T) {
	srv := newScopedServer(t)
	args := scopedArgs(srv, "lookup", "platform/deploy", "--project", unregisteredProject)
	out, err := runScoped(t, t.TempDir(), "", args...)
	if err != nil {
		t.Fatalf("an unregistered Project is not an error: %v", err)
	}
	onlyProject(t, srv, unregisteredProject)
	if !strings.Contains(out, "Use the platform chart") || strings.Contains(out, "Shop-specific") {
		t.Errorf("want the global article:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "not registered") {
		t.Errorf("output lacks the unregistered hint:\n%s", out)
	}
	js, err := runScoped(t, t.TempDir(), "", append(args, "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Hint *string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(js), &m); err != nil || m.Hint == nil || !strings.Contains(strings.ToLower(*m.Hint), "not registered") {
		t.Errorf("--json hint = %v, %v\n%s", m.Hint, err, js)
	}
}

func TestCLIStatusShowsTheUnregisteredHint(t *testing.T) {
	srv := newScopedServer(t)
	out, err := runScoped(t, t.TempDir(), "", scopedArgs(srv, "status", "--project", unregisteredProject)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Project: "+unregisteredProject) || !strings.Contains(strings.ToLower(out), "not registered") {
		t.Errorf("status lacks the Project or its unregistered hint:\n%s", out)
	}
	out, err = runScoped(t, t.TempDir(), "", scopedArgs(srv, "status", "--project", registeredProject)...)
	if err != nil || !strings.Contains(out, "Project: "+registeredProject) || strings.Contains(strings.ToLower(out), "not registered") {
		t.Errorf("registered Project: %q, %v", out, err)
	}
	out, err = runScoped(t, t.TempDir(), "", scopedArgs(srv, "status")...)
	if err != nil || strings.Contains(strings.ToLower(out), "not registered") {
		t.Errorf("no Project: %q, %v", out, err)
	}
	// --json stays one JSON document and still names the Project.
	m := statusJSON(t, t.TempDir(), "--addr", srv.URL, "--token", cliToken, "--project", unregisteredProject)
	if m["project"] != unregisteredProject {
		t.Errorf("status --json project = %v", m["project"])
	}
}

func TestCLIInvalidProjectIsRejectedBeforeAnyRequestAndNeverEchoesACredential(t *testing.T) {
	const secret = "ghp_s3cretTokenValue"
	srv := newScopedServer(t)
	for _, args := range [][]string{{"search", "kubernetes"}, {"lookup", "platform/deploy"}} {
		srv.reset()
		_, err := runScoped(t, t.TempDir(), "", scopedArgs(srv, append(args, "--project", "user:"+secret+"@host")...)...)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Errorf("%v: err = %v, want an error without the credential", args, err)
		}
		if got := srv.projects(); len(got) != 0 {
			t.Errorf("%v sent %q to the server despite an unusable Project", args, got)
		}
	}
	// A hostile value cannot add parameters or change the search words.
	srv.reset()
	_, err := runScoped(t, t.TempDir(), "", scopedArgs(srv, "search", "kubernetes", "--project", "github.com/acme/shop&q=zzz")...)
	if err == nil {
		t.Error("a Project carrying a query separator was accepted")
	}
	if got := srv.projects(); len(got) != 0 {
		t.Errorf("hostile Project reached the server: %q", got)
	}
}

func TestCLIProjectFlagIsStillUnknownToVersion(t *testing.T) {
	if _, err := runCLI(t, "version", "--project", "ex.com/a/b"); err == nil || !strings.Contains(err.Error(), "-project") {
		t.Errorf("version --project: err = %v, want an unknown-flag error", err)
	}
}
