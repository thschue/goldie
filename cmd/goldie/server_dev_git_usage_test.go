package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// A credential without GOLDIE_GIT_URL is a usage error: exit 2 and the
// one-line next step, like every other misuse of goldie server.
func TestServerDevGitCredentialRefusalIsMisuse(t *testing.T) {
	stdout, stderr, err := runDevToExit(t, gitDevEnv("GOLDIE_GIT_TOKEN=s3cret-token"), "server", "--dev", "--git")
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Fatalf("err = %v, want exit code 2 for misuse", err)
	}
	if !strings.Contains(stderr, "Run 'goldie server --help'.") {
		t.Errorf("stderr = %q, want the next step", stderr)
	}
	if !strings.Contains(stderr, "set GOLDIE_GIT_URL to use your own repository") {
		t.Errorf("stderr = %q, want the way out", stderr)
	}
	if strings.Contains(stdout+stderr, "s3cret-token") || strings.Contains(stdout, "GOLDIE_TOKEN=") {
		t.Errorf("secret or export line printed:\n%s%s", stdout, stderr)
	}
}
