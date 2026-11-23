package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Acceptance tests for #282: goldie init writes .goldie/config.yaml with
// `project: <id>` at the repository root and says whether the Project is
// registered, through GET /api/v1/projects on a fake API on loopback. Its
// output is three lines, plus a hint when the Project is not registered:
//
//	Project: <id> (from <how it was found>)
//	Wrote: <root>/.goldie/config.yaml
//	Registered: yes | no | not checked (<why>)
//	Hint: To register it, add a projects/<id>/ directory to one of goldie's Sources, usually through the platform team.
//
// A refusal writes nothing, prints nothing and returns one of the init errors
// below.

const (
	initErrExists    = ".goldie/config.yaml already exists at the repository root; goldie init never overwrites it"
	initErrSymlink   = ".goldie at the repository root is a symlink; goldie init never follows it"
	initErrNoRepo    = "not inside a git repository: goldie init writes .goldie/config.yaml at the repository root"
	initErrSource    = "this is a Source repository (global/articles or projects/ at its root): goldie init is for a project repository"
	initErrNoProject = "no Project found: set --project or GOLDIE_PROJECT, or add an upstream or origin remote"
)

// projectsAPI is a fake API that serves only GET /api/v1/projects, to the CLI
// token, and records every request as "<method> <path> <authorization>".
type projectsAPI struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newProjectsAPI(t *testing.T, projectsJSON string) *projectsAPI {
	t.Helper()
	a := &projectsAPI{}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.seen = append(a.seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+cliToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"missing or wrong token","details":{}}}`))
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/projects" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"not found","details":{}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"projects":` + projectsJSON + `}`))
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *projectsAPI) requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seen...)
}

// runInit runs goldie init in dir with only the given GOLDIE_ variables set.
func runInit(t *testing.T, dir string, env map[string]string, args ...string) (string, error) {
	t.Helper()
	for _, name := range []string{"GOLDIE_ADDR", "GOLDIE_TOKEN", "GOLDIE_TOKEN_FILE", "GOLDIE_PROJECT"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	t.Chdir(dir)
	var out bytes.Buffer
	err := run(append([]string{"init"}, args...), &out)
	return out.String(), err
}

// atRoot replaces the repository root in out with <root>, in both its given
// and its symlink-resolved spelling.
func atRoot(t *testing.T, out, root string) string {
	t.Helper()
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		out = strings.ReplaceAll(out, resolved, "<root>")
	}
	return strings.ReplaceAll(out, root, "<root>")
}

func subdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileContent(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// noGoldie fails when dir/.goldie exists in any form.
func noGoldie(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, ".goldie")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s/.goldie exists (%v), want nothing written there", dir, err)
	}
}

const (
	widgetsOrigin = "[remote \"origin\"]\n\turl = git@github.com:Acme/Widgets.git\n"
	widgetsConfig = "project: github.com/acme/widgets\n"
	widgetsHint   = "Hint: To register it, add a projects/github.com/acme/widgets/ directory to one of goldie's Sources, usually through the platform team.\n"
)

func TestCLIInitWritesConfigAtRepositoryRoot(t *testing.T) {
	api := newProjectsAPI(t, `["github.com/Acme/Widgets","github.com/other/thing"]`)
	root := gitRepo(t, widgetsOrigin)
	deep := subdir(t, root, "pkg", "deep")

	out, err := runInit(t, deep, nil, "--addr", api.URL, "--token", cliToken)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	want := "Project: github.com/acme/widgets (from git remote origin)\n" +
		"Wrote: <root>/.goldie/config.yaml\n" +
		"Registered: yes\n"
	if got := atRoot(t, out, root); got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
	if got := fileContent(t, filepath.Join(root, ".goldie", "config.yaml")); got != widgetsConfig {
		t.Errorf("config.yaml = %q, want %q", got, widgetsConfig)
	}
	noGoldie(t, deep)
	noGoldie(t, filepath.Join(root, "pkg"))

	// The check is one GET /api/v1/projects with the token, and nothing else.
	if got := api.requests(); len(got) != 1 || got[0] != "GET /api/v1/projects Bearer "+cliToken {
		t.Errorf("API requests = %q, want one GET /api/v1/projects with the token", got)
	}

	// Run again: the config the first run wrote is never overwritten.
	out, err = runInit(t, deep, nil, "--addr", api.URL, "--token", cliToken)
	if err == nil || err.Error() != initErrExists {
		t.Errorf("second init: err = %v, want %q", err, initErrExists)
	}
	if out != "" {
		t.Errorf("second init printed %q, want nothing", out)
	}
	if got := fileContent(t, filepath.Join(root, ".goldie", "config.yaml")); got != widgetsConfig {
		t.Errorf("config.yaml after the second init = %q", got)
	}
}

// TestCLIInitRegisteredIgnoresCase checks the registered check against the
// stored spelling of a Project, ignoring case (ADR 0020), and that it matches
// the whole id, not a prefix or a part of it.
func TestCLIInitRegisteredIgnoresCase(t *testing.T) {
	cases := []struct {
		name, list string
		registered bool
	}{
		{"same case", `["github.com/acme/widgets"]`, true},
		{"stored in another case", `["github.com/ACME/WIDGETS"]`, true},
		{"among others", `["a.example/x/y","github.com/Acme/Widgets","z.example/x/y"]`, true},
		{"empty list", `[]`, false},
		{"other project", `["github.com/acme/other"]`, false},
		{"longer repo", `["github.com/acme/widgets-two"]`, false},
		{"shorter repo", `["github.com/acme/widget"]`, false},
		{"other host", `["gitlab.com/acme/widgets"]`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := newProjectsAPI(t, c.list)
			root := gitRepo(t, widgetsOrigin)
			out, err := runInit(t, root, nil, "--addr", api.URL, "--token", cliToken)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			want := "Project: github.com/acme/widgets (from git remote origin)\n" +
				"Wrote: <root>/.goldie/config.yaml\n"
			if c.registered {
				want += "Registered: yes\n"
			} else {
				want += "Registered: no\n" + widgetsHint
			}
			if got := atRoot(t, out, root); got != want {
				t.Errorf("output:\n%s\nwant:\n%s", got, want)
			}
			if got := fileContent(t, filepath.Join(root, ".goldie", "config.yaml")); got != widgetsConfig {
				t.Errorf("config.yaml = %q", got)
			}
		})
	}
}

// TestCLIInitNotChecked checks that init still writes the config when it
// cannot check the Project, and says why.
func TestCLIInitNotChecked(t *testing.T) {
	api := newProjectsAPI(t, `["github.com/acme/widgets"]`)
	cases := []struct {
		name     string
		env      map[string]string
		args     []string
		line     string
		requests int
	}{
		{"no address", nil, []string{"--token", cliToken},
			"Registered: not checked (no server address: set --addr or GOLDIE_ADDR)\n", 0},
		{"no token", nil, []string{"--addr", api.URL},
			"Registered: not checked (no token: set --token, GOLDIE_TOKEN or GOLDIE_TOKEN_FILE)\n", 0},
		{"token rejected", nil, []string{"--addr", api.URL, "--token", "wrong-token-value"},
			"Registered: not checked (token rejected)\n", 1},
		{"token rejected, from the environment", map[string]string{"GOLDIE_ADDR": api.URL, "GOLDIE_TOKEN": "wrong-token-value"}, nil,
			"Registered: not checked (token rejected)\n", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := len(api.requests())
			root := gitRepo(t, widgetsOrigin)
			out, err := runInit(t, root, c.env, c.args...)
			if err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			want := "Project: github.com/acme/widgets (from git remote origin)\n" +
				"Wrote: <root>/.goldie/config.yaml\n" + c.line
			if got := atRoot(t, out, root); got != want {
				t.Errorf("output:\n%s\nwant:\n%s", got, want)
			}
			if got := fileContent(t, filepath.Join(root, ".goldie", "config.yaml")); got != widgetsConfig {
				t.Errorf("config.yaml = %q", got)
			}
			if n := len(api.requests()) - before; n != c.requests {
				t.Errorf("API requests = %d, want %d", n, c.requests)
			}
			if strings.Contains(out, "wrong-token-value") || strings.Contains(out, cliToken) {
				t.Errorf("output shows a token: %q", out)
			}
		})
	}
}

// TestCLIInitSaysHowTheProjectWasFound checks the Project line for a Project
// given by flag or variable, normalised before it is written.
func TestCLIInitSaysHowTheProjectWasFound(t *testing.T) {
	api := newProjectsAPI(t, `["ex.com/org/repo"]`)
	cases := []struct {
		name string
		env  map[string]string
		args []string
		from string
	}{
		{"--project", nil, []string{"--project", "Ex.com/Org/Repo.git"}, "--project"},
		{"GOLDIE_PROJECT", map[string]string{"GOLDIE_PROJECT": "git@Ex.com:Org/Repo.git"}, nil, "GOLDIE_PROJECT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := gitRepo(t, widgetsOrigin)
			out, err := runInit(t, root, c.env, append(c.args, "--addr", api.URL, "--token", cliToken)...)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			want := "Project: ex.com/org/repo (from " + c.from + ")\n" +
				"Wrote: <root>/.goldie/config.yaml\n" +
				"Registered: yes\n"
			if got := atRoot(t, out, root); got != want {
				t.Errorf("output:\n%s\nwant:\n%s", got, want)
			}
			if got := fileContent(t, filepath.Join(root, ".goldie", "config.yaml")); got != "project: ex.com/org/repo\n" {
				t.Errorf("config.yaml = %q", got)
			}
		})
	}

	// A fork: upstream wins over origin, as for status.
	fork := gitRepo(t, "[remote \"origin\"]\n\turl = git@github.com:me/widgets.git\n[remote \"upstream\"]\n\turl = https://GitHub.com/Acme/Widgets.git\n")
	out, err := runInit(t, fork, nil)
	if err != nil {
		t.Fatalf("init in a fork: %v", err)
	}
	if !strings.HasPrefix(out, "Project: github.com/acme/widgets (from git remote upstream)\n") {
		t.Errorf("init in a fork: %q", out)
	}
	if got := fileContent(t, filepath.Join(fork, ".goldie", "config.yaml")); got != widgetsConfig {
		t.Errorf("fork config.yaml = %q", got)
	}
}

func TestCLIInitNeverOverwritesExistingConfig(t *testing.T) {
	api := newProjectsAPI(t, `["github.com/acme/widgets"]`)
	for _, content := range []string{"project: github.com/me/mine\n", "", "other: 1\n"} {
		root := gitRepo(t, widgetsOrigin)
		path := filepath.Join(root, ".goldie", "config.yaml")
		writeFile(t, path, content)
		for _, dir := range []string{root, subdir(t, root, "pkg")} {
			out, err := runInit(t, dir, nil, "--addr", api.URL, "--token", cliToken)
			if err == nil || err.Error() != initErrExists {
				t.Errorf("config %q, from %s: err = %v, want %q", content, dir, err, initErrExists)
			}
			if out != "" {
				t.Errorf("config %q: printed %q, want nothing", content, out)
			}
			if got := fileContent(t, path); got != content {
				t.Errorf("config.yaml = %q, want it untouched: %q", got, content)
			}
		}
	}

	// A broken config is an error too, and is left as it is.
	root := gitRepo(t, widgetsOrigin)
	path := filepath.Join(root, ".goldie", "config.yaml")
	writeFile(t, path, "project: [unclosed\n")
	if _, err := runInit(t, root, nil); err == nil || strings.HasPrefix(err.Error(), "usage:") {
		t.Errorf("broken config: err = %v, want the config error", err)
	}
	if got := fileContent(t, path); got != "project: [unclosed\n" {
		t.Errorf("broken config.yaml = %q, want it untouched", got)
	}
}

func TestCLIInitRefusesOutsideRepository(t *testing.T) {
	for _, args := range [][]string{nil, {"--project", "github.com/acme/widgets"}} {
		dir := t.TempDir()
		out, err := runInit(t, dir, nil, args...)
		if err == nil || err.Error() != initErrNoRepo {
			t.Errorf("init %q outside a repository: err = %v, want %q", args, err, initErrNoRepo)
		}
		if out != "" {
			t.Errorf("printed %q, want nothing", out)
		}
		noGoldie(t, dir)
		noGoldie(t, filepath.Dir(dir))
	}
}

func TestCLIInitRefusesWithoutProject(t *testing.T) {
	root := gitRepo(t, "[core]\n\tbare = false\n")
	out, err := runInit(t, root, nil)
	if err == nil || err.Error() != initErrNoProject {
		t.Errorf("no remotes: err = %v, want %q", err, initErrNoProject)
	}
	if out != "" {
		t.Errorf("printed %q, want nothing", out)
	}
	noGoldie(t, root)

	// A remote that is not host/org/repo gives no Project: refused, and the
	// URL, which may hold a credential or a private path, is not echoed.
	for _, url := range []string{"/srv/private/widgets.git", "https://user:s3cret-token@", "https://gitlab.com/group/sub/app.git"} {
		root := gitRepo(t, "[remote \"origin\"]\n\turl = "+url+"\n")
		out, err := runInit(t, root, nil)
		if err == nil || strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("origin %q: err = %v, want no Project found", url, err)
		} else if strings.Contains(err.Error(), url) || strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "private") {
			t.Errorf("origin %q: error %q echoes the URL", url, err)
		}
		if out != "" {
			t.Errorf("origin %q: printed %q, want nothing", url, out)
		}
		noGoldie(t, root)
	}
}

func TestCLIInitRefusesSourceRepository(t *testing.T) {
	for name, marker := range map[string]string{
		"global/articles": filepath.Join("global", "articles", "deploy.md"),
		"projects/":       filepath.Join("projects", "github.com", "acme", "shop", "articles", "deploy.md"),
	} {
		t.Run(name, func(t *testing.T) {
			root := gitRepo(t, widgetsOrigin)
			writeFile(t, filepath.Join(root, marker), "---\nname: x\n---\n")
			for _, dir := range []string{root, subdir(t, root, "docs")} {
				for _, args := range [][]string{nil, {"--project", "github.com/acme/widgets"}} {
					out, err := runInit(t, dir, nil, args...)
					if err == nil || err.Error() != initErrSource {
						t.Errorf("from %s %q: err = %v, want %q", dir, args, err, initErrSource)
					}
					if out != "" {
						t.Errorf("printed %q, want nothing", out)
					}
					noGoldie(t, root)
					noGoldie(t, dir)
				}
			}
		})
	}
}

// TestCLIInitRefusesHostileProject checks that an unusable Project from the
// flag or the variable writes nothing, and the error never echoes it.
func TestCLIInitRefusesHostileProject(t *testing.T) {
	//nolint:staticcheck // ST1018: the hostile values hold format characters on purpose.
	hostile := []string{
		"../../etc/passwd",
		"github.com/acme/widgets\nproject: evil.example/x/y",
		"github.com/acme/wid​gets",
		"github.com/acme/widgets‮",
		"github.com/acme/wid\x1b[31mgets",
		"user:s3cret-token@host",
		"https://user:s3cret-token@",
		"gitlab.com/group/sub/app",
	}
	//nolint:staticcheck // ST1018: the hostile values hold format characters on purpose.
	for _, v := range hostile {
		for _, via := range []string{"flag", "env"} {
			root := gitRepo(t, widgetsOrigin)
			var env map[string]string
			var args []string
			if via == "flag" {
				args = []string{"--project", v}
			} else {
				env = map[string]string{"GOLDIE_PROJECT": v}
			}
			out, err := runInit(t, root, env, args...)
			if err == nil || strings.HasPrefix(err.Error(), "usage:") {
				t.Errorf("%s %q: err = %v, want the Project refused", via, v, err)
			} else if msg := err.Error(); strings.Contains(msg, v) || strings.Contains(msg, "s3cret") || strings.Contains(msg, "evil") ||
				strings.ContainsAny(msg, "​‮\x1b") {
				t.Errorf("%s %q: error %q echoes it", via, v, msg)
			}
			if out != "" {
				t.Errorf("%s %q: printed %q, want nothing", via, v, out)
			}
			noGoldie(t, root)
		}
	}

	// A remote URL with a credential that reads as host/org/repo is written
	// without the credential.
	root := gitRepo(t, "[remote \"origin\"]\n\turl = https://user:s3cret-token@GitHub.com/Acme/Widgets.git\n")
	out, err := runInit(t, root, nil)
	if err != nil {
		t.Fatalf("credential-bearing origin: %v", err)
	}
	if strings.Contains(out, "s3cret") {
		t.Errorf("output shows the credential: %q", out)
	}
	if got := fileContent(t, filepath.Join(root, ".goldie", "config.yaml")); got != widgetsConfig {
		t.Errorf("config.yaml = %q, want %q", got, widgetsConfig)
	}
}

// TestCLIInitNeverWritesAboveRepositoryRoot checks that a repository nested in
// another, as a plain clone, a submodule or a worktree, gets its own config,
// and that the parent's .goldie/ is never used or changed.
func TestCLIInitNeverWritesAboveRepositoryRoot(t *testing.T) {
	const parentConfig = "project: evil.example/other/repo\n"
	newOuter := func(t *testing.T) string {
		outer := gitRepo(t, "[remote \"origin\"]\n\turl = https://evil.example/other/outer\n")
		writeFile(t, filepath.Join(outer, ".goldie", "config.yaml"), parentConfig)
		return outer
	}
	assertOuter := func(t *testing.T, outer string) {
		t.Helper()
		if got := fileContent(t, filepath.Join(outer, ".goldie", "config.yaml")); got != parentConfig {
			t.Errorf("parent config.yaml = %q, want it untouched", got)
		}
		entries, err := os.ReadDir(filepath.Join(outer, ".goldie"))
		if err != nil || len(entries) != 1 {
			t.Errorf("parent .goldie/ holds %d entries (%v), want only its config.yaml", len(entries), err)
		}
	}
	check := func(t *testing.T, inner, from, wantFrom string) {
		t.Helper()
		out, err := runInit(t, from, nil)
		if err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		want := "Project: github.com/acme/inner (from " + wantFrom + ")\n" +
			"Wrote: <root>/.goldie/config.yaml\n" +
			"Registered: not checked (no server address: set --addr or GOLDIE_ADDR)\n"
		if got := atRoot(t, out, inner); got != want {
			t.Errorf("output:\n%s\nwant:\n%s", got, want)
		}
		if got := fileContent(t, filepath.Join(inner, ".goldie", "config.yaml")); got != "project: github.com/acme/inner\n" {
			t.Errorf("inner config.yaml = %q", got)
		}
		if from != inner {
			noGoldie(t, from)
		}
	}

	t.Run("nested clone", func(t *testing.T) {
		outer := newOuter(t)
		inner := filepath.Join(outer, "vendor", "inner")
		writeFile(t, filepath.Join(inner, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/inner.git\n")
		check(t, inner, subdir(t, inner, "pkg", "deep"), "git remote origin")
		assertOuter(t, outer)
		noGoldie(t, filepath.Join(outer, "vendor"))
	})

	t.Run("submodule", func(t *testing.T) {
		outer := newOuter(t)
		writeFile(t, filepath.Join(outer, ".git", "modules", "inner", "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/inner.git\n")
		inner := filepath.Join(outer, "vendor", "inner")
		writeFile(t, filepath.Join(inner, ".git"), "gitdir: ../../.git/modules/inner\n")
		check(t, inner, subdir(t, inner, "pkg"), "git remote origin")
		assertOuter(t, outer)
		noGoldie(t, filepath.Join(outer, ".git", "modules", "inner"))
	})

	t.Run("worktree", func(t *testing.T) {
		main := gitRepo(t, "[remote \"origin\"]\n\turl = git@github.com:acme/inner.git\n")
		wtGit := filepath.Join(main, ".git", "worktrees", "feature")
		writeFile(t, filepath.Join(wtGit, "commondir"), "../..\n")
		outer := newOuter(t)
		wt := filepath.Join(outer, "wt")
		writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+wtGit+"\n")
		check(t, wt, subdir(t, wt, "pkg"), "git remote origin")
		assertOuter(t, outer)
		noGoldie(t, main)
		noGoldie(t, wtGit)
	})
}

func TestCLIUsageListsInit(t *testing.T) {
	var help bytes.Buffer
	if err := run(nil, &help); err != nil || !hasCommandLine(help.String(), "init") {
		t.Errorf("root help = %q (err %v), want it to list init", help.String(), err)
	}
	root := gitRepo(t, widgetsOrigin)
	out, err := runInit(t, root, nil, "extra")
	if err == nil || !strings.Contains(err.Error(), "init takes no arguments") {
		t.Errorf("init extra: err = %v, want the misuse reason", err)
	}
	if out != "" {
		t.Errorf("init extra printed %q, want nothing", out)
	}
	noGoldie(t, root)
}
