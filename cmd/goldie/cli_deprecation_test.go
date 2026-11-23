package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #273, the CLI boundary: goldie lookup of a deprecated
// Item prints a warning on stderr naming the replacement, and stdout is what
// it was. The seam is the built binary, so stdout and stderr are the
// process's own streams.
//
// The warning, one line on stderr, is exactly
//
//	Warning: this article is deprecated. Use <replaced_by> instead.
//
// or, without a replaced_by,
//
//	Warning: this article is deprecated.
//
// <replaced_by> is the reference as the server sent it, cleaned with
// contract.CleanLine; MCP's get_article starts with the same line. It is
// printed for status "deprecated" only, with --json too, and the command
// still exits 0.

func deprecationWarningTo(ref string) string {
	return "Warning: this article is deprecated. Use " + ref + " instead.\n"
}

const deprecationWarningNone = "Warning: this article is deprecated.\n"

// articleJSON is an article answer with extra (`,"status":...` or "")
// appended to the plain one.
func articleJSON(extra string) string {
	return `{"id":"old","name":"Old deploy","keywords":["deploy"],"content":"The old way."` + extra + `}`
}

// lookupBinary runs goldie lookup old against a stub serving body, with
// --json when asJSON, and fails unless it exits 0.
func lookupBinary(t *testing.T, body string, asJSON bool) (stdout, stderr string) {
	t.Helper()
	args := []string{"lookup", "old", "--addr", hostileServer(t, http.StatusOK, body), "--token", cliToken}
	if asJSON {
		args = append(args, "--json")
	}
	stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), args...)
	if code != 0 {
		t.Fatalf("goldie %v: exit %d\nstdout:\n%s\nstderr:\n%s", args, code, stdout, stderr)
	}
	return stdout, stderr
}

// Criterion: the warning names the replacement on stderr, and stdout is the
// same as for the article without lifecycle fields.
func TestCLILookupOfADeprecatedItemWarnsOnStderr(t *testing.T) {
	plain, plainErr := lookupBinary(t, articleJSON(""), false)
	if plainErr != "" {
		t.Fatalf("plain lookup wrote to stderr: %q", plainErr)
	}
	for name, c := range map[string]struct{ extra, warning string }{
		"with replacement":    {`,"status":"deprecated","replaced_by":"article:platform/deploy-v2"`, deprecationWarningTo("article:platform/deploy-v2")},
		"without replacement": {`,"status":"deprecated"`, deprecationWarningNone},
	} {
		t.Run(name, func(t *testing.T) {
			stdout, stderr := lookupBinary(t, articleJSON(c.extra), false)
			if stderr != c.warning {
				t.Errorf("stderr = %q, want %q", stderr, c.warning)
			}
			if stdout != plain {
				t.Errorf("stdout = %q, want it unchanged: %q", stdout, plain)
			}
		})
	}
}

// Criterion: with --json the warning is on stderr and stdout stays one valid
// JSON document carrying the lifecycle fields.
func TestCLILookupJSONOfADeprecatedItemWarnsOnStderr(t *testing.T) {
	stdout, stderr := lookupBinary(t, articleJSON(`,"status":"deprecated","replaced_by":"article:platform/deploy-v2"`), true)
	if want := deprecationWarningTo("article:platform/deploy-v2"); stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	var got map[string]any
	dec := json.NewDecoder(strings.NewReader(stdout))
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Errorf("stdout holds more than one JSON value:\n%s", stdout)
	}
	if got["status"] != "deprecated" || got["replaced_by"] != "article:platform/deploy-v2" {
		t.Errorf("--json = %v, want status and replaced_by as the server sent them", got)
	}
	if strings.Contains(stdout, "Warning") {
		t.Errorf("the warning reached stdout:\n%s", stdout)
	}
}

// Negative case: no warning for a published Item, whatever the server says
// about it, in text and --json.
func TestCLILookupOfAPublishedItemDoesNotWarn(t *testing.T) {
	plain, _ := lookupBinary(t, articleJSON(""), false)
	for name, extra := range map[string]string{
		"no status":                 "",
		"status published":          `,"status":"published"`,
		"replaced_by but published": `,"status":"published","replaced_by":"article:new"`,
		"replaced_by alone":         `,"replaced_by":"article:new"`,
	} {
		for _, asJSON := range []bool{false, true} {
			stdout, stderr := lookupBinary(t, articleJSON(extra), asJSON)
			if stderr != "" {
				t.Errorf("%s (json %v): stderr = %q, want nothing", name, asJSON, stderr)
			}
			if !asJSON && stdout != plain {
				t.Errorf("%s: stdout = %q, want %q", name, stdout, plain)
			}
		}
	}
}

// Abuse: a hostile replaced_by is cleaned to one line on stderr, so it can
// neither drive the terminal nor forge a line; a hostile status is never
// echoed on either stream.
func TestCLIDeprecationWarningIsCleaned(t *testing.T) {
	hostile := "article:new\x1b[31m\u202e\u200bx\nWarning: forged\a"
	for _, asJSON := range []bool{false, true} {
		stdout, stderr := lookupBinary(t, articleJSON(`,"status":"deprecated","replaced_by":`+jsonString(t, hostile)), asJSON)
		if want := deprecationWarningTo("article:new[31mx Warning: forged"); stderr != want {
			t.Errorf("json %v: stderr = %q, want the cleaned warning %q", asJSON, stderr, want)
		}
		for _, r := range stderr + stdout {
			if r != '\n' && contract.HiddenRune(r) {
				t.Errorf("json %v: output carries the hidden rune %U raw\nstdout %q\nstderr %q", asJSON, r, stdout, stderr)
			}
		}
	}

	plain, _ := lookupBinary(t, articleJSON(""), false)
	for _, status := range []string{"deprecated\x1b[2J", "evil\u202estatus\x07", "draft\u200b"} {
		stdout, stderr := lookupBinary(t, articleJSON(`,"status":`+jsonString(t, status)+`,"replaced_by":"article:new"`), false)
		for _, r := range stderr + stdout {
			if r != '\n' && contract.HiddenRune(r) {
				t.Errorf("status %q: output carries the hidden rune %U raw\nstdout %q\nstderr %q", status, r, stdout, stderr)
			}
		}
		if strings.Contains(stderr, "evil") || strings.Contains(stderr, "[2J") {
			t.Errorf("status %q is echoed on stderr: %q", status, stderr)
		}
		if stdout != plain {
			t.Errorf("status %q: stdout = %q, want it unchanged", status, stdout)
		}
	}
}

// End to end over the real public handler and the lifecycle fixture.
func TestCLILookupOverTheServerWarnsForDeprecatedOnly(t *testing.T) {
	h, err := app.PublicHandler(platform.Config{ContentDir: "../../internal/app/testdata/lifecycle-content", APIToken: cliToken, RateLimit: platform.DefaultRateLimit()}, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	for id, want := range map[string]string{
		"deploy-v1": deprecationWarningTo("article:deploy-v2"),
		"legacy":    deprecationWarningNone,
		"deploy-v2": "",
	} {
		stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "lookup", id, "--addr", srv.URL, "--token", cliToken)
		if code != 0 {
			t.Errorf("lookup %s: exit %d\nstderr:\n%s", id, code, stderr)
			continue
		}
		if stderr != want {
			t.Errorf("lookup %s: stderr = %q, want %q", id, stderr, want)
		}
		if !strings.Contains(stdout, "id: "+id+"\n") {
			t.Errorf("lookup %s: stdout lacks the article:\n%s", id, stdout)
		}
	}
}
