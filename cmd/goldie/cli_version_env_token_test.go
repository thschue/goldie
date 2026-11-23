package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Acceptance tests for #514: goldie version reads the token from the
// environment as goldie status does, and the CLI words the API's hints for a
// CLI user (by error code), without changing the API's own hints.

const (
	lookupHint = "Search for the article with: goldie search <words>, then use an id from its results."
	tokenHint  = "Set --token, GOLDIE_TOKEN or GOLDIE_TOKEN_FILE to a valid API token."
)

func TestCLIVersionReadsTheTokenFromTheEnvironment(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(versionToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(t *testing.T){
		"GOLDIE_TOKEN":      func(t *testing.T) { t.Setenv("GOLDIE_TOKEN", versionToken) },
		"GOLDIE_TOKEN_FILE": func(t *testing.T) { t.Setenv("GOLDIE_TOKEN_FILE", tokenFile) },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			srv := authedDocServer(t)
			// runCLI clears the variables, so run directly with them set.
			t.Chdir(t.TempDir())
			t.Setenv("GOLDIE_ADDR", srv.URL)
			t.Setenv("GOLDIE_TOKEN", "")
			_ = os.Unsetenv("GOLDIE_TOKEN")
			t.Setenv("GOLDIE_TOKEN_FILE", "")
			_ = os.Unsetenv("GOLDIE_TOKEN_FILE")
			set(t)
			for _, asJSON := range []bool{false, true} {
				args := []string{"version"}
				if asJSON {
					args = append(args, "--json")
				}
				var out strings.Builder
				if err := run(args, &out); err != nil {
					t.Fatalf("%v: %v", args, err)
				}
				got := out.String()
				if !strings.Contains(got, "7.8.9") {
					t.Errorf("%v = %q, want the server API version 7.8.9", args, got)
				}
				lower := strings.ToLower(got)
				for _, bad := range []string{"no token", "unknown", "hint", "rejected", "servernote"} {
					if strings.Contains(lower, bad) {
						t.Errorf("%v = %q, must not carry %q", args, got, bad)
					}
				}
				if strings.Contains(got, versionToken) {
					t.Errorf("%v leaks the token: %q", args, got)
				}
			}
		})
	}
}

func TestCLILookupUnknownIDHintIsWordedForTheCLI(t *testing.T) {
	srv := cliServer(t)
	_, err := runCLI(t, "lookup", "no/such", "--addr", srv.URL, "--token", cliToken)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Hint: "+lookupHint) {
		t.Errorf("error = %q, want the hint %q", err, lookupHint)
	}
	for _, bad := range []string{"search operation", "Authorization", "Bearer"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error = %q, must not name HTTP term %q", err, bad)
		}
	}
}

// codeServer answers every request with the status and an error envelope of
// the given code and hint.
func codeServer(t *testing.T, status int, code, hint string) *httptest.Server {
	t.Helper()
	return answerServer(t, status, jsonType,
		`{"error":{"code":"`+code+`","message":"nope","details":{"hint":"`+hint+`"}}}`)
}

func TestCLIA401HintNamesTheCLIWaysToSetATokenOnAnyCommand(t *testing.T) {
	// The server's own wording differs from the text the real API sends, so
	// the CLI must replace by error code, not by matching text.
	srv := codeServer(t, http.StatusUnauthorized, "unauthorized", "Some other server wording about headers.")
	for _, args := range [][]string{
		{"search", "agent"},
		{"lookup", "some/id"},
	} {
		_, err := runCLI(t, append(args, "--addr", srv.URL, "--token", "wrong-token-value")...)
		if err == nil {
			t.Fatalf("%v: expected an error", args)
		}
		if !strings.Contains(err.Error(), "Hint: "+tokenHint) {
			t.Errorf("%v: error = %q, want the hint %q", args, err, tokenHint)
		}
		for _, bad := range []string{"Authorization", "Bearer", "wrong-token-value", "other server wording"} {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("%v: error = %q, must not contain %q", args, err, bad)
			}
		}
	}
}

func TestCLIA401FromTheRealServerNamesTheCLIWaysToSetATokenOnSearch(t *testing.T) {
	srv := cliServer(t)
	_, err := runCLI(t, "search", "agent", "--addr", srv.URL, "--token", "wrong-token-value")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Hint: "+tokenHint) {
		t.Errorf("error = %q, want the hint %q", err, tokenHint)
	}
	if strings.Contains(err.Error(), "Authorization") || strings.Contains(err.Error(), "Bearer") {
		t.Errorf("error = %q, must not name HTTP terms", err)
	}
}

func TestCLIOtherErrorHintsAreNotReplaced(t *testing.T) {
	srv := codeServer(t, http.StatusBadRequest, "bad_request", "Fix the query.")
	_, err := runCLI(t, "search", "agent", "--addr", srv.URL, "--token", cliToken)
	if err == nil || !strings.Contains(err.Error(), "Hint: Fix the query.") {
		t.Errorf("error = %v, want the server's own hint kept", err)
	}
	if err != nil && (strings.Contains(err.Error(), tokenHint) || strings.Contains(err.Error(), lookupHint)) {
		t.Errorf("error = %q, must not carry another code's hint", err)
	}
}
