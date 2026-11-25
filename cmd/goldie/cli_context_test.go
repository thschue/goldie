package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Acceptance tests for #668 at the dispatcher: goldie context --hook is a
// registered command, exits 2 on misuse and 0 on every failure, and works over
// GOLDIE_DIR (local mode). The cases over a stub API are in
// internal/cli/context_test.go.

const ctxShop = "[remote \"origin\"]\n\turl = git@github.com:acme/shop.git\n"

func TestCLIContextHookInLocalModePrintsTheCatalog(t *testing.T) {
	t.Setenv("GOLDIE_DIR", dirContent(t))
	stdout, err := runCLIIn(t, gitRepo(t, ctxShop), "context", "--hook")
	var errBuf bytes.Buffer
	if code := exitCode(err, &errBuf); code != 0 || errBuf.Len() != 0 {
		t.Fatalf("exit %d, stderr %q (err %v), want 0 and empty", code, errBuf.String(), err)
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not the hook JSON: %v\n%q", err, stdout)
	}
	h := got.HookSpecificOutput
	if h.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q, want SessionStart", h.HookEventName)
	}
	for _, want := range []string{"Goldie holds this team's golden paths.", "reviewer: Reviews code globally", "nightly: Keep the nightly build green"} {
		if !strings.Contains(h.AdditionalContext, want) {
			t.Errorf("additionalContext lacks %q:\n%s", want, h.AdditionalContext)
		}
	}
}

func TestCLIContextMisuseExitsTwoAndFailureExitsZero(t *testing.T) {
	misuse := [][]string{{"context"}, {"context", "--hook", "--no-such-flag"}}
	for _, args := range misuse {
		stdout, err := runCLI(t, args...)
		var errBuf bytes.Buffer
		if code := exitCode(err, &errBuf); code != 2 || stdout != "" {
			t.Errorf("%v: exit %d, stdout %q, want 2 and nothing", args, code, stdout)
		}
	}
	// No server and no token: a failure, so the session start shows no error.
	stdout, err := runCLI(t, "context", "--hook")
	var errBuf bytes.Buffer
	if code := exitCode(err, &errBuf); code != 0 || stdout != "" {
		t.Errorf("failure: exit %d, stdout %q, want 0 and nothing", code, stdout)
	}
}
