package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// GitHub sign-in settings (#642) at the binary: a partial configuration stops
// goldie server non-zero, labelled invalid configuration, naming the variable
// and never echoing the client secret; a complete one starts, and the startup
// report shows the secret as (redacted).

const binGitHubSecret = "gh-oauth-secret-0123456789abcdef0123456789abcdef"

// githubBinEnv is a complete sign-in configuration without the variable drop
// and with extra appended (a later entry wins).
func githubBinEnv(drop string, extra ...string) []string {
	env := map[string]string{
		"GOLDIE_GITHUB_CLIENT_ID":      "Iv1.0123456789abcdef",
		"GOLDIE_GITHUB_CLIENT_SECRET":  binGitHubSecret,
		"GOLDIE_GITHUB_ORGS":           "acme",
		"GOLDIE_GITHUB_OPERATOR_TEAMS": "acme/platform",
		"GOLDIE_PUBLIC_URL":            "https://goldie.example.com",
	}
	delete(env, drop)
	var out []string
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return append(out, extra...)
}

func TestBinaryRefusesAPartialGitHubConfigurationNamingTheVariable(t *testing.T) {
	for name, tc := range map[string]struct {
		env  []string
		want string
	}{
		"client id without orgs":    {githubBinEnv("GOLDIE_GITHUB_ORGS", "GOLDIE_GITHUB_OPERATOR_TEAMS="), "GOLDIE_GITHUB_ORGS"},
		"team whose org not listed": {githubBinEnv("", "GOLDIE_GITHUB_OPERATOR_TEAMS=stranger/ops"), "GOLDIE_GITHUB_OPERATOR_TEAMS"},
		"plain http public url":     {githubBinEnv("", "GOLDIE_PUBLIC_URL=http://goldie.example.com"), "GOLDIE_PUBLIC_URL"},
	} {
		// A server that wrongly starts must fail this case, not hang the run.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, goldieBinary(t), "server")
		cmd.Env = append([]string{
			"PATH=" + os.Getenv("PATH"), "GOLDIE_ADDR=127.0.0.1:0", "GOLDIE_ADMIN_ADDR=127.0.0.1:0",
			"GOLDIE_CONTENT_DIR=" + contentDir(t), "GOLDIE_API_TOKEN_FILE=" + tokenFile(t, testToken),
		}, tc.env...)
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
		_, failure, ok := strings.Cut(string(out), "goldie: ")
		if !ok || !strings.HasPrefix(failure, "invalid configuration") || !strings.Contains(failure, tc.want) {
			t.Errorf("%s: want a failure labelled invalid configuration naming %s: %s", name, tc.want, out)
		}
		if strings.Contains(string(out), binGitHubSecret) {
			t.Errorf("%s: output echoes the client secret: %s", name, out)
		}
	}
}

func TestBinaryStartsWithAGitHubConfigurationAndNeverLogsTheSecret(t *testing.T) {
	env := append(githubBinEnv("GOLDIE_GITHUB_CLIENT_SECRET"), "GOLDIE_GITHUB_CLIENT_SECRET_FILE="+tokenFile(t, binGitHubSecret), "GOLDIE_DRAIN_SECONDS=0")
	p := startServer(t, env...)
	if !strings.Contains(p.logs(), `"GOLDIE_GITHUB_CLIENT_SECRET":"(redacted)"`) {
		t.Errorf("the startup report does not show the client secret as redacted:\n%s", p.logs())
	}
	if strings.Contains(p.logs(), binGitHubSecret) {
		t.Errorf("the logs contain the client secret:\n%s", p.logs())
	}
	if got := bearerStatus(t, "http://"+p.app+"/api/v1/articles/agent-loops", testToken); got != http.StatusOK {
		t.Errorf("API with a GitHub configuration: status %d, want 200", got)
	}
}

func TestBinaryWithoutGitHubSettingsReportsTheSecretUnsetAndServesAsBefore(t *testing.T) {
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=0")
	if !strings.Contains(p.logs(), `"GOLDIE_GITHUB_CLIENT_SECRET":"(unset)"`) {
		t.Errorf("the startup report does not show the client secret as unset:\n%s", p.logs())
	}
	if got := bearerStatus(t, "http://"+p.app+"/api/v1/articles/agent-loops", testToken); got != http.StatusOK {
		t.Errorf("API without GitHub settings: status %d, want 200", got)
	}
}
