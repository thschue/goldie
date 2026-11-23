package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Every config error in one run (#186). The operator sees each problem the
// server finds at startup in one failure, each naming its variable, never a
// value, and fixes them in one restart.

// A bad token and a missing content dir stop goldie server in one run that
// names both variables, labelled invalid configuration.
func TestBinaryReportsEveryServerSettingProblemInOneRun(t *testing.T) {
	const marker = "s3cret marker 0123456789abcdef0123" // long enough that the space is what fails
	missing := filepath.Join(t.TempDir(), "missing-marker-dir")
	for name, env := range map[string][]string{
		"bad token, missing dir": {"GOLDIE_API_TOKEN_FILE=" + tokenFile(t, marker), "GOLDIE_CONTENT_DIR=" + missing},
		"bad token line, no dir": {"GOLDIE_API_TOKENS=ci:reader:" + marker},
		"no token, missing dir":  {"GOLDIE_CONTENT_DIR=" + missing},
	} {
		// A server that wrongly starts must fail this case, not hang the run.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, goldieBinary(t), "server")
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "GOLDIE_ADDR=127.0.0.1:0", "GOLDIE_ADMIN_ADDR=127.0.0.1:0"}, env...)
		out, err := cmd.CombinedOutput()
		timedOut := ctx.Err() != nil
		cancel()
		if timedOut {
			t.Errorf("%s: the server started instead of failing", name)
			continue
		}
		if err == nil {
			t.Errorf("%s: exit 0, want a failure: %s", name, out)
			continue
		}
		// The startup report before the failure logs every setting name, and
		// the content dir as written (PC-06); the failure is what follows
		// "goldie: ".
		_, failure, ok := strings.Cut(string(out), "goldie: ")
		if !ok || !strings.HasPrefix(failure, "invalid configuration") {
			t.Errorf("%s: no failure labelled invalid configuration: %s", name, out)
		}
		for _, v := range []string{"GOLDIE_API_TOKEN", "GOLDIE_CONTENT_DIR"} {
			if !strings.Contains(failure, v) {
				t.Errorf("%s: the failure does not name %s, want every problem in one run: %s", name, v, failure)
			}
		}
		for _, hidden := range []string{missing, "missing-marker-dir"} {
			if strings.Contains(failure, hidden) {
				t.Errorf("%s: the failure echoes the content dir: %s", name, failure)
			}
		}
		for _, secret := range []string{marker, "s3cret"} {
			if strings.Contains(string(out), secret) {
				t.Errorf("%s: output echoes the token: %s", name, out)
			}
		}
	}
}

// LoadConfig refuses GOLDIE_API_TOKEN and GOLDIE_API_TOKENS together, so
// goldie health, which loads the configuration but needs no token, stops on
// them too, naming both variables and echoing neither token, before it probes
// anything.
func TestBinaryHealthRejectsBothTokenVariables(t *testing.T) {
	for name, env := range map[string][]string{
		"both variables": {"GOLDIE_API_TOKEN=" + testToken, "GOLDIE_API_TOKENS=" + binTokenLines()},
		"token file":     {"GOLDIE_API_TOKEN_FILE=" + tokenFile(t, testToken), "GOLDIE_API_TOKENS=" + binTokenLines()},
	} {
		cmd := exec.Command(goldieBinary(t), "health")
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "GOLDIE_ADMIN_ADDR=127.0.0.1:1"}, env...)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "GOLDIE_API_TOKEN and GOLDIE_API_TOKENS") {
			t.Errorf("health with %s: err = %v, want a failure naming GOLDIE_API_TOKEN and GOLDIE_API_TOKENS: %s", name, err, out)
		}
		if strings.Contains(string(out), "unreachable") {
			t.Errorf("health with %s: it probed the admin port instead of refusing the configuration: %s", name, out)
		}
		for _, secret := range []string{testToken, binReaderToken, binOperatorToken} {
			if strings.Contains(string(out), secret) {
				t.Errorf("health with %s: output echoes a token: %s", name, out)
			}
		}
	}
}

// #258: LoadConfig refuses GOLDIE_CONTENT_DIR and GOLDIE_GIT_URL together, so
// goldie health stops on them too, naming both variables before it probes
// anything, and echoing neither the directory nor the URL's password.
func TestBinaryHealthRejectsContentDirAndGitURL(t *testing.T) {
	const password = "git-url-password-0123456789abcdef"
	dir := filepath.Join(t.TempDir(), "content-marker-dir")
	url := "https://deploy:" + password + "@git.example.com/org/content.git"
	urlFile := filepath.Join(t.TempDir(), "git-url")
	if err := os.WriteFile(urlFile, []byte(url+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][]string{
		"both variables": {"GOLDIE_CONTENT_DIR=" + dir, "GOLDIE_GIT_URL=" + url},
		"url file":       {"GOLDIE_CONTENT_DIR=" + dir, "GOLDIE_GIT_URL_FILE=" + urlFile},
	} {
		cmd := exec.Command(goldieBinary(t), "health")
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "GOLDIE_ADMIN_ADDR=127.0.0.1:1"}, env...)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "GOLDIE_CONTENT_DIR") || !strings.Contains(string(out), "GOLDIE_GIT_URL") {
			t.Errorf("health with %s: err = %v, want a failure naming GOLDIE_CONTENT_DIR and GOLDIE_GIT_URL: %s", name, err, out)
		}
		if strings.Contains(string(out), "unreachable") {
			t.Errorf("health with %s: it probed the admin port instead of refusing the configuration: %s", name, out)
		}
		for _, hidden := range []string{password, "content-marker-dir", urlFile} {
			if strings.Contains(string(out), hidden) {
				t.Errorf("health with %s: output echoes %q: %s", name, hidden, out)
			}
		}
	}
}

// #438: a first run with nothing set ends at the command that works, one line
// naming goldie server -dev. An operator who set one of the two, or set a bad
// value, knows what they are doing and gets the error alone.

// serverFailure runs goldie server with env on top of an otherwise empty
// environment and returns its stderr, failing the test if it starts or hangs.
func serverFailure(t *testing.T, env ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goldieBinary(t), "server")
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "GOLDIE_ADDR=127.0.0.1:0", "GOLDIE_ADMIN_ADDR=127.0.0.1:0"}, env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("the server started instead of failing: %s", stderr.String())
	}
	if err == nil {
		t.Fatalf("exit 0, want a failure: %s", stderr.String())
	}
	return stderr.String()
}

var devHint = regexp.MustCompile(`goldie server --?dev`)

func TestBinaryServerWithNothingSetEndsWithTheDevHint(t *testing.T) {
	out := serverFailure(t)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	last := lines[len(lines)-1]
	if !devHint.MatchString(last) {
		t.Errorf("last stderr line = %q, want a hint naming goldie server -dev: %s", last, out)
	}
	// The error wording stays: both operator errors come before the hint.
	for _, want := range []string{"invalid configuration", "GOLDIE_API_TOKENS or GOLDIE_API_TOKEN is not set", "GOLDIE_CONTENT_DIR or GOLDIE_GIT_URL is not set"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr lost %q: %s", want, out)
		}
	}
	if len(devHint.FindAllString(out, -1)) != 1 {
		t.Errorf("want the hint exactly once: %s", out)
	}
}

func TestBinaryServerWithOnlyOneOfSourceAndTokenHasNoDevHint(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-marker-dir")
	const gitURL = "https://git.example.com/org/content.git"
	for name, env := range map[string][]string{
		"token only":                {"GOLDIE_API_TOKEN=" + testToken},
		"tokens only":               {"GOLDIE_API_TOKENS=" + binTokenLines()},
		"token file only":           {"GOLDIE_API_TOKEN_FILE=" + tokenFile(t, testToken)},
		"content dir, no token":     {"GOLDIE_CONTENT_DIR=" + t.TempDir()},
		"git url, no token":         {"GOLDIE_GIT_URL=" + gitURL},
		"bad token, no source":      {"GOLDIE_API_TOKENS=ci:reader:short"},
		"missing dir, no token":     {"GOLDIE_CONTENT_DIR=" + missing},
		"token, bad dir":            {"GOLDIE_API_TOKEN=" + testToken, "GOLDIE_CONTENT_DIR=" + missing},
		"token, both sources (bad)": {"GOLDIE_API_TOKEN=" + testToken, "GOLDIE_CONTENT_DIR=" + t.TempDir(), "GOLDIE_GIT_URL=" + gitURL},
	} {
		out := serverFailure(t, env...)
		if devHint.MatchString(out) {
			t.Errorf("%s: stderr has the dev hint, want the error alone: %s", name, out)
		}
	}
}
