package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// gitRepo writes a repository whose .git/config is cfg and returns its path.
func gitRepo(t *testing.T, cfg string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCLIStatusDetectsProject(t *testing.T) {
	srv := cliServer(t)
	fork := gitRepo(t, "[remote \"origin\"]\n\turl = git@github.com:me/widgets.git\n[remote \"upstream\"]\n\turl = https://GitHub.com/Acme/Widgets.git\n")
	m := statusJSON(t, fork, "--addr", srv.URL)
	if m["project"] != "github.com/acme/widgets" || m["projectSource"] != "git remote upstream" {
		t.Errorf("fork: project=%v from %v", m["project"], m["projectSource"])
	}
	out, err := runCLIIn(t, fork, "status", "--addr", srv.URL)
	if err != nil || !strings.Contains(out, "Project: github.com/acme/widgets (from git remote upstream)") {
		t.Errorf("status = %q, %v", out, err)
	}

	m = statusJSON(t, t.TempDir(), "--addr", srv.URL)
	if m["project"] != nil || m["projectSource"] != nil {
		t.Errorf("not a repository: project=%v from %v, want null", m["project"], m["projectSource"])
	}
	out, _ = runCLIIn(t, t.TempDir(), "status", "--addr", srv.URL)
	if !strings.Contains(out, "Project: none found") {
		t.Errorf("not a repository: %q", out)
	}
}

// runCLIWithProjectEnv is runCLIIn with GOLDIE_PROJECT set.
func runCLIWithProjectEnv(t *testing.T, dir, project string, args ...string) map[string]any {
	t.Helper()
	for _, name := range []string{"GOLDIE_ADDR", "GOLDIE_TOKEN", "GOLDIE_TOKEN_FILE"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	t.Setenv("GOLDIE_PROJECT", project)
	t.Chdir(dir)
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCLIProjectOverrideOrder(t *testing.T) {
	srv := cliServer(t)
	dir := gitRepo(t, "[remote \"origin\"]\n\turl = git@github.com:me/detected.git\n")
	cfgDir := filepath.Join(dir, ".goldie")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("project: ex.com/file/p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := []string{"status", "--json", "--addr", srv.URL}
	m := runCLIWithProjectEnv(t, dir, "ex.com/env/p", append(status, "--project", "ex.com/flag/p")...)
	if m["project"] != "ex.com/flag/p" || m["projectSource"] != "--project" {
		t.Errorf("flag: %v from %v", m["project"], m["projectSource"])
	}
	m = runCLIWithProjectEnv(t, dir, "ex.com/env/p", status...)
	if m["project"] != "ex.com/env/p" || m["projectSource"] != "GOLDIE_PROJECT" {
		t.Errorf("env: %v from %v", m["project"], m["projectSource"])
	}
	m = statusJSON(t, dir, "--addr", srv.URL)
	if m["project"] != "ex.com/file/p" || m["projectSource"] != ".goldie/config.yaml" {
		t.Errorf("config file: %v from %v", m["project"], m["projectSource"])
	}
	if err := os.Remove(filepath.Join(cfgDir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	m = statusJSON(t, dir, "--addr", srv.URL)
	if m["project"] != "github.com/me/detected" || m["projectSource"] != "git remote origin" {
		t.Errorf("detection: %v from %v", m["project"], m["projectSource"])
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
