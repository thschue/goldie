package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
