package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/platform"
)

// statusJSON runs status --json in dir and decodes the answer.
func statusJSON(t *testing.T, dir string, args ...string) map[string]any {
	t.Helper()
	out, err := runCLIIn(t, dir, append([]string{"status", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("status --json is not JSON: %v\n%s", err, out)
	}
	return m
}

// On a first run, with no address set and no server to ask, status points to
// goldie server --dev, as the server's first-run hint does (#459).
func TestCLIStatusWithNoServerPointsToServerDev(t *testing.T) {
	stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "status")
	if code == 0 {
		t.Errorf("exit code = 0, want a failure with no server (stdout %q)", stdout)
	}
	if !strings.Contains(stderr, "goldie server --dev") {
		t.Errorf("stderr = %q, want it to point to goldie server --dev", stderr)
	}
	if !strings.Contains(stderr, "GOLDIE_ADDR") {
		t.Errorf("stderr = %q, want it to keep naming the missing address setting", stderr)
	}
}

func TestCLIStatusJSONTokenGiven(t *testing.T) {
	srv := cliServer(t)
	cases := []struct {
		name            string
		args            []string
		given, accepted bool
	}{
		{"no token", nil, false, false},
		{"rejected token", []string{"--token", "wrong-token-value"}, true, false},
		{"accepted token", []string{"--token", cliToken}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := statusJSON(t, t.TempDir(), append([]string{"--addr", srv.URL}, c.args...)...)
			if m["tokenGiven"] != c.given || m["tokenAccepted"] != c.accepted {
				t.Errorf("tokenGiven=%v tokenAccepted=%v, want %v and %v", m["tokenGiven"], m["tokenAccepted"], c.given, c.accepted)
			}
		})
	}
}

func TestCLIStatusNeverPrintsRemoteCredentials(t *testing.T) {
	srv := cliServer(t)
	const secret = "ghp_s3cretTokenValue"
	dir := gitRepo(t, "[remote \"origin\"]\n\turl = https://user:"+secret+"@github.com/acme/widgets.git\n")
	for _, args := range [][]string{{"status"}, {"status", "--json"}} {
		out, err := runCLIIn(t, dir, append(args, "--addr", srv.URL)...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, secret) || strings.Contains(out, "user:") {
			t.Errorf("%v leaks the remote's userinfo:\n%s", args, out)
		}
		if !strings.Contains(out, "github.com/acme/widgets") {
			t.Errorf("%v lacks the Project:\n%s", args, out)
		}
	}
	// A credential in an unreadable value never reaches the output. status
	// reports the problem on its Project: line and exits 0 (#145).
	for _, args := range [][]string{{"status"}, {"status", "--json"}} {
		out, err := runCLIIn(t, t.TempDir(), append(args, "--addr", srv.URL, "--project", "user:"+secret+"@host")...)
		if err != nil {
			t.Errorf("%v: err = %v, want exit 0 with the problem reported", args, err)
			continue
		}
		for _, leak := range []string{secret, "user:", "@host"} {
			if strings.Contains(out, leak) {
				t.Errorf("%v leaks %q:\n%s", args, leak, out)
			}
		}
		if args[len(args)-1] == "status" && !strings.HasPrefix(projectLine(t, out), "Project:") {
			t.Errorf("%v: no Project: line in:\n%s", args, out)
		}
		if len(args) == 2 && !strings.Contains(out, "projectError") {
			t.Errorf("%v lacks projectError:\n%s", args, out)
		}
	}
	// Commands that still fail on a bad Project never echo the credential in the error.
	_, err := runCLIIn(t, t.TempDir(), "search", "kubernetes", "--addr", srv.URL, "--project", "user:"+secret+"@host")
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("search err = %v, want an error without the credential", err)
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

// statusIn runs the CLI in dir with GOLDIE_PROJECT set to env (empty means
// unset) and the other GOLDIE_* variables cleared.
func statusIn(t *testing.T, dir, env string, args ...string) (string, error) {
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

// projectLine returns the single "Project:" line of a status text.
func projectLine(t *testing.T, out string) string {
	t.Helper()
	var found []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Project:") {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one Project: line, got %d in:\n%s", len(found), out)
	}
	return found[0]
}

// statusEnvJSON is status --json with GOLDIE_PROJECT set to env.
func statusEnvJSON(t *testing.T, dir, env string, args ...string) map[string]any {
	t.Helper()
	out, err := statusIn(t, dir, env, append([]string{"status", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return m
}

// absent reports whether an additive key is missing or null.
func absent(m map[string]any, key string) bool {
	v, ok := m[key]
	return !ok || v == nil
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".goldie"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".goldie", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func remoteCfg(name, url string) string {
	return "[remote \"" + name + "\"]\n\turl = " + url + "\n"
}

// badProject is a way to give the CLI a Project it must refuse.
type badProject struct {
	name string
	dir  func(t *testing.T) string
	env  string
	args []string
}

func badProjects() []badProject {
	plain := func(t *testing.T) string { return t.TempDir() }
	return []badProject{
		{"bad --project", plain, "", []string{"--project", "just-a-name"}},
		{"bad GOLDIE_PROJECT", plain, "just-a-name", nil},
		{"malformed config yaml", func(t *testing.T) string {
			d := gitRepo(t, "")
			writeConfig(t, d, "project: [unclosed\n")
			return d
		}, "", nil},
		{"bad project in config", func(t *testing.T) string {
			d := gitRepo(t, "")
			writeConfig(t, d, "project: just-a-name\n")
			return d
		}, "", nil},
	}
}

func TestCLIStatusReportsProjectProblemOnItsOwnLine(t *testing.T) {
	srv := cliServer(t)
	for _, c := range badProjects() {
		t.Run(c.name, func(t *testing.T) {
			dir := c.dir(t)
			base := append([]string{"--addr", srv.URL, "--token", cliToken}, c.args...)
			out, err := statusIn(t, dir, c.env, append([]string{"status"}, base...)...)
			if err != nil {
				t.Fatalf("status must exit 0, got %v\n%s", err, out)
			}
			for _, want := range []string{"Server: reachable", "API: ", "Auth: token accepted"} {
				if !strings.Contains(out, want) {
					t.Errorf("status lacks %q:\n%s", want, out)
				}
			}
			line := projectLine(t, out)
			if strings.TrimSpace(strings.TrimPrefix(line, "Project:")) == "" || strings.Contains(line, "none found") {
				t.Errorf("Project line does not report the problem: %q", line)
			}
			for _, l := range strings.Split(out, "\n") {
				if strings.HasPrefix(l, "Server:") || strings.HasPrefix(l, "API:") || strings.HasPrefix(l, "Auth:") {
					if strings.Contains(l, "Project") {
						t.Errorf("problem leaked onto another line: %q", l)
					}
				}
			}

			m := statusEnvJSON(t, dir, c.env, base...)
			msg, ok := m["projectError"].(string)
			if !ok || strings.TrimSpace(msg) == "" {
				t.Errorf("projectError = %v, want a non-empty string", m["projectError"])
			}
			if m["reachable"] != true || m["tokenAccepted"] != true || m["api"] == nil || m["apiVersion"] == nil {
				t.Errorf("the existing fields must stay: %v", m)
			}
			if m["project"] != nil || m["projectSource"] != nil {
				t.Errorf("project=%v from %v, want null", m["project"], m["projectSource"])
			}
			if !absent(m, "projectHint") {
				t.Errorf("projectHint = %v on a Project error", m["projectHint"])
			}
		})
	}
}

func TestCLIStatusProjectFieldsAreAdditiveOnly(t *testing.T) {
	srv := cliServer(t)
	m := statusJSON(t, t.TempDir(), "--addr", srv.URL, "--token", cliToken, "--project", "github.com/acme/widgets")
	for _, k := range []string{"projectError", "projectHint"} {
		if !absent(m, k) {
			t.Errorf("%s = %v with a good Project", k, m[k])
		}
	}
	// The unregistered-Project hint of #183 stays its own field.
	hint, _ := m["hint"].(string)
	if hint == "" || strings.Contains(hint, "host/org/repo") {
		t.Errorf("hint = %q, want the unregistered-Project hint, unchanged", hint)
	}
}

func TestCLIOtherCommandsStillFailOnProjectErrors(t *testing.T) {
	srv := cliServer(t)
	for _, c := range badProjects() {
		for _, cmd := range [][]string{{"search", "kubernetes"}, {"lookup", "platform/deploy"}} {
			t.Run(c.name+" "+cmd[0], func(t *testing.T) {
				args := append(append([]string{}, cmd...), "--addr", srv.URL, "--token", cliToken)
				args = append(args, c.args...)
				out, err := statusIn(t, c.dir(t), c.env, args...)
				if err == nil {
					t.Errorf("%s must fail on a Project error, printed:\n%s", cmd[0], out)
				}
			})
		}
	}
}

func TestCLIStatusUnreachableServerStillFailsBeforePrinting(t *testing.T) {
	for _, c := range badProjects() {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"status", "--addr", "http://127.0.0.1:1"}, c.args...)
			out, err := statusIn(t, c.dir(t), c.env, args...)
			if err == nil || !strings.Contains(err.Error(), "cannot reach") || strings.TrimSpace(out) != "" {
				t.Errorf("err = %v, out = %q, want a cannot-reach error and no output", err, out)
			}
		})
	}
}

// rejectedRemotes are remotes that exist but are not host/org/repo.
var rejectedRemotes = []struct{ name, remote, url string }{
	{"origin gitlab subgroup", "origin", "https://git.internal.example/grp/sub/repo.git"},
	{"origin local path", "origin", "/srv/internal-secret/widgets.git"},
	{"origin file url", "origin", "file:///srv/internal-secret/widgets.git"},
	{"upstream gitlab subgroup", "upstream", "git@git.internal.example:grp/sub/repo.git"},
	{"upstream local path", "upstream", "../internal-secret/widgets"},
}

func TestCLIStatusHintsWhenARemoteWasRejected(t *testing.T) {
	srv := cliServer(t)
	leaks := []string{"git.internal.example", "internal-secret", "grp/sub"}
	for _, c := range rejectedRemotes {
		t.Run(c.name, func(t *testing.T) {
			dir := gitRepo(t, remoteCfg(c.remote, c.url))
			out, err := runCLIIn(t, dir, "status", "--addr", srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			line := projectLine(t, out)
			for _, want := range []string{"found", "not in the accepted host/org/repo form", c.remote} {
				if !strings.Contains(line, want) {
					t.Errorf("Project line lacks %q: %q", want, line)
				}
			}
			if strings.Contains(line, "none found") {
				t.Errorf("a rejected remote reads like no remote: %q", line)
			}
			for _, leak := range leaks {
				if strings.Contains(out, leak) {
					t.Errorf("text output leaks the remote URL (%q):\n%s", leak, out)
				}
			}

			raw, err := runCLIIn(t, dir, "status", "--json", "--addr", srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(raw), &m); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, raw)
			}
			hint, _ := m["projectHint"].(string)
			if !strings.Contains(hint, c.remote) || !strings.Contains(hint, "host/org/repo") {
				t.Errorf("projectHint = %q, want it to name %s and host/org/repo", hint, c.remote)
			}
			if m["project"] != nil || m["projectSource"] != nil || !absent(m, "projectError") {
				t.Errorf("project=%v from %v error=%v", m["project"], m["projectSource"], m["projectError"])
			}
			for _, leak := range leaks {
				if strings.Contains(raw, leak) {
					t.Errorf("JSON output leaks the remote URL (%q):\n%s", leak, raw)
				}
			}
		})
	}
}

func TestCLIStatusHintNamesARejectedRemoteWhenBothAreRejected(t *testing.T) {
	srv := cliServer(t)
	dir := gitRepo(t, remoteCfg("upstream", "/srv/aaa")+remoteCfg("origin", "/srv/bbb"))
	m := statusJSON(t, dir, "--addr", srv.URL)
	hint, _ := m["projectHint"].(string)
	if !strings.Contains(hint, "upstream") && !strings.Contains(hint, "origin") {
		t.Errorf("projectHint = %q, want it to name upstream or origin", hint)
	}
	if strings.Contains(hint, "/srv/") {
		t.Errorf("projectHint leaks a remote URL: %q", hint)
	}
}

func TestCLIStatusRemoteCredentialsNeverReachTheOutput(t *testing.T) {
	srv := cliServer(t)
	const secret = "ghp_s3cretTokenValue"
	cases := []struct {
		name string
		dir  func(t *testing.T) string
		args []string
	}{
		{"rejected origin", func(t *testing.T) string {
			return gitRepo(t, remoteCfg("origin", "https://alice:"+secret+"@secret-host.example/a/b/c"))
		}, nil},
		{"rejected upstream", func(t *testing.T) string {
			return gitRepo(t, remoteCfg("upstream", "ssh://alice:"+secret+"@secret-host.example/a/b/c"))
		}, nil},
		{"credential in --project", func(t *testing.T) string { return t.TempDir() },
			[]string{"--project", "alice:" + secret + "@secret-host.example"}},
		{"credential in config", func(t *testing.T) string {
			d := gitRepo(t, "")
			writeConfig(t, d, "project: alice:"+secret+"@secret-host.example\n")
			return d
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := c.dir(t)
			for _, mode := range [][]string{{"status"}, {"status", "--json"}} {
				out, err := runCLIIn(t, dir, append(append(mode, "--addr", srv.URL), c.args...)...)
				if err != nil {
					t.Fatalf("%v: %v", mode, err)
				}
				for _, leak := range []string{secret, "alice", "secret-host"} {
					if strings.Contains(out, leak) {
						t.Errorf("%v leaks %q:\n%s", mode, leak, out)
					}
				}
			}
		})
	}
}

func TestCLIStatusProjectErrorHasNoControlCharacters(t *testing.T) {
	srv := cliServer(t)
	dir := gitRepo(t, "")
	writeConfig(t, dir, "project: \"bad\\x1b[31mred\\u202Ename\"\n")
	for _, mode := range [][]string{{"status"}, {"status", "--json"}} {
		out, err := runCLIIn(t, dir, append(mode, "--addr", srv.URL)...)
		if err != nil {
			t.Fatalf("%v must exit 0: %v", mode, err)
		}
		if strings.ContainsAny(out, "\x1b\u202e") {
			t.Errorf("%v prints a control or bidi character: %q", mode, out)
		}
	}
}

func TestCLIStatusHintOnlyWhenNothingElseGivesAProject(t *testing.T) {
	srv := cliServer(t)
	rejectedUpstream := remoteCfg("upstream", "/srv/internal-secret/widgets.git")
	rejectedOrigin := remoteCfg("origin", "/srv/internal-secret/widgets.git")
	const validOrigin = "[remote \"origin\"]\n\turl = git@github.com:me/widgets.git\n"
	const validUpstream = "[remote \"upstream\"]\n\turl = git@github.com:acme/widgets.git\n"
	cases := []struct {
		name, cfg, env, config string
		args                   []string
		project, source        any
	}{
		{"upstream rejected, origin valid", rejectedUpstream + validOrigin, "", "", nil, "github.com/me/widgets", "git remote origin"},
		{"upstream valid, origin rejected", validUpstream + rejectedOrigin, "", "", nil, "github.com/acme/widgets", "git remote upstream"},
		{"project from flag", rejectedOrigin, "", "", []string{"--project", "ex.com/flag/p"}, "ex.com/flag/p", "--project"},
		{"project from env", rejectedOrigin, "ex.com/env/p", "", nil, "ex.com/env/p", "GOLDIE_PROJECT"},
		{"project from config", rejectedOrigin, "", "project: ex.com/file/p\n", nil, "ex.com/file/p", ".goldie/config.yaml"},
		{"no remotes", "", "", "", nil, nil, nil},
		{"remote section without url", "[remote \"origin\"]\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n", "", "", nil, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := gitRepo(t, c.cfg)
			if c.config != "" {
				writeConfig(t, dir, c.config)
			}
			args := append([]string{"--addr", srv.URL}, c.args...)
			m := statusEnvJSON(t, dir, c.env, args...)
			if m["project"] != c.project || m["projectSource"] != c.source {
				t.Errorf("project=%v from %v, want %v from %v", m["project"], m["projectSource"], c.project, c.source)
			}
			for _, k := range []string{"projectHint", "projectError"} {
				if !absent(m, k) {
					t.Errorf("%s = %v, want none", k, m[k])
				}
			}
			out, err := statusIn(t, dir, c.env, append([]string{"status"}, args...)...)
			if err != nil {
				t.Fatal(err)
			}
			line := projectLine(t, out)
			if strings.Contains(line, "accepted host/org/repo") {
				t.Errorf("unexpected hint: %q", line)
			}
			if c.project == nil && !strings.Contains(line, "none found") {
				t.Errorf("no Project and no rejected remote must keep the none found text: %q", line)
			}
		})
	}
}

// TestCLIStatusProjectProblemGolden pins the Project line and the additive JSON
// fields for a bad Project and for a rejected remote, next to status_project.
func TestCLIStatusProjectProblemGolden(t *testing.T) {
	srv := cliServer(t)
	base := []string{"--addr", srv.URL, "--token", cliToken}
	rejected := func(t *testing.T) string {
		return gitRepo(t, remoteCfg("origin", "https://git.internal.example/grp/sub/repo.git"))
	}
	plain := func(t *testing.T) string { return t.TempDir() }
	cases := []struct {
		name string
		dir  func(t *testing.T) string
		args []string
	}{
		{"status_project_problem", plain, []string{"status", "--project", "just-a-name"}},
		{"status_project_problem_json", plain, []string{"status", "--json", "--project", "just-a-name"}},
		{"status_project_rejected_remote", rejected, []string{"status"}},
		{"status_project_rejected_remote_json", rejected, []string{"status", "--json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := runCLIIn(t, c.dir(t), append(c.args, base...)...)
			if err != nil {
				t.Fatalf("run %v: %v", c.args, err)
			}
			for _, leak := range []string{"git.internal.example", "grp/sub", "just-a-name"} {
				if strings.Contains(out, leak) {
					t.Errorf("output leaks %q:\n%s", leak, out)
				}
			}
			golden(t, c.name, out, srv.URL)
		})
	}
}

// statusLimitedServer serves the real API document, and answers every /api/
// request with the 429 envelope and the given Retry-After value, so the
// status command's token probe is the request over the limit.
func statusLimitedServer(t *testing.T, retryAfter string) string {
	t.Helper()
	h, err := app.PublicHandler(platform.Config{ContentDir: "../../internal/app/testdata/content", APIToken: cliToken, RateLimit: platform.DefaultRateLimit()}, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Retry-After", retryAfter)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"too many requests for this API token",` +
			`"details":{"hint":"Wait the number of seconds in the Retry-After header, then retry."}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The status token probe over the limit is the 429, with the wait when the
// header gives one, and not a failure to reach the server when it does not:
// the generated parser rejects a Retry-After that is not an integer.
func TestCLIStatusProbeOverTheLimitIsThe429(t *testing.T) {
	cases := map[string]string{"7": "7 seconds", "soon-marker": ""}
	// Every server first: runCLI changes the working directory, and the
	// content directory is relative to this one.
	urls := map[string]string{}
	for value := range cases {
		urls[value] = statusLimitedServer(t, value)
	}
	for value, want := range cases {
		_, err := runCLI(t, "status", "--addr", urls[value], "--token", cliToken)
		if err == nil {
			t.Errorf("Retry-After %q: expected an error", value)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "429") || strings.Contains(msg, "cannot reach") {
			t.Errorf("Retry-After %q: error %q is not the 429", value, msg)
		}
		if want != "" && !strings.Contains(msg, want) {
			t.Errorf("Retry-After %q: error %q lacks %q", value, msg, want)
		}
		if strings.Contains(msg, "marker") || strings.Contains(msg, "0 seconds") {
			t.Errorf("Retry-After %q: error %q echoes the value or claims a wait", value, msg)
		}
	}
}

func TestCLIStatusRejectedToken(t *testing.T) {
	srv := cliServer(t)
	out, err := runCLI(t, "status", "--addr", srv.URL, "--token", "wrong-token-value")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "token rejected") {
		t.Errorf("status = %q, want it to say the token was rejected", out)
	}
}

func TestCLIStatusWithoutToken(t *testing.T) {
	srv := cliServer(t)
	out, err := runCLI(t, "status", "--addr", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no token given") {
		t.Errorf("status = %q, want it to say no token was given", out)
	}
	out, err = runCLI(t, "status", "--addr", srv.URL, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"tokenAccepted": false`) && !strings.Contains(out, `"tokenAccepted":false`) {
		t.Errorf("status --json = %q, want tokenAccepted false", out)
	}
}
