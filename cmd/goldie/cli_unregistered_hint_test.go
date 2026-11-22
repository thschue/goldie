package main

import (
	"strings"
	"testing"
)

// Acceptance tests for #187: search, lookup and status show the unregistered
// hint that says how to register a Project, in text and in --json, and the
// hint names no HTTP route and no client command.

func wantRegisterHint(id string) string {
	return "The Project " + id + " is not registered on this server, so this is the global answer. " +
		"To register it, add a projects/" + id + "/ directory to one of goldie's Sources, usually through the platform team."
}

func TestCLIUnregisteredHintSaysHowToRegister(t *testing.T) {
	srv := newScopedServer(t)
	want := wantRegisterHint(unregisteredProject)
	for _, args := range [][]string{
		{"search", "kubernetes"},
		{"lookup", "platform/deploy"},
		{"status"},
	} {
		out, err := runScoped(t, t.TempDir(), "", scopedArgs(srv, append(args, "--project", unregisteredProject)...)...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("%v output lacks the hint %q:\n%s", args, want, out)
		}
		for _, bad := range []string{"/api/v1", "GET /", "List the registered Projects", "goldie projects"} {
			if strings.Contains(out, bad) {
				t.Errorf("%v output still contains %q:\n%s", args, bad, out)
			}
		}
	}
}

func TestCLIUnregisteredHintInJSON(t *testing.T) {
	srv := newScopedServer(t)
	want := wantRegisterHint(unregisteredProject)
	out, err := runScoped(t, t.TempDir(), "", scopedArgs(srv, "search", "kubernetes", "--project", unregisteredProject, "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	if _, hint := searchHits(t, out); hint != want {
		t.Errorf("search --json hint = %q, want %q", hint, want)
	}
	m := statusJSON(t, t.TempDir(), "--addr", srv.URL, "--token", cliToken, "--project", unregisteredProject)
	if m["hint"] != want {
		t.Errorf("status --json hint = %v, want %q", m["hint"], want)
	}
}

// A detected Project (from the git remote) gets the hint with its own id, and
// a registered Project or none gets no hint.
func TestCLIUnregisteredHintNamesTheDetectedProject(t *testing.T) {
	srv := newScopedServer(t)
	dir := gitRepo(t, "[remote \"origin\"]\n\turl = https://github.com/acme/nothere.git\n")
	out, err := runScoped(t, dir, "", scopedArgs(srv, "search", "kubernetes")...)
	if err != nil || !strings.Contains(out, wantRegisterHint(unregisteredProject)) {
		t.Errorf("detected unregistered Project: %v\n%s", err, out)
	}
	for _, args := range [][]string{{"search", "kubernetes", "--project", registeredProject}, {"search", "kubernetes"}} {
		out, err := runScoped(t, t.TempDir(), "", scopedArgs(srv, args...)...)
		if err != nil || strings.Contains(out, "register") {
			t.Errorf("%v shows a registration hint: %v\n%s", args, err, out)
		}
	}
}
