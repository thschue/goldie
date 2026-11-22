package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
