package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Per-token rate limiting (#100) as the operator configures it:
// GOLDIE_RATE_LIMIT or GOLDIE_RATE_LIMIT_FILE, "<n>/s" or "<n>/m" with an
// optional ",burst=<b>".

// The binary applies GOLDIE_RATE_LIMIT to each token on its own: the reader
// gets 429 with Retry-After once its burst is spent while the operator is
// still served, and the startup report logs the effective limit.
func TestBinaryRateLimitsEachTokenFromTheSetting(t *testing.T) {
	for name, env := range map[string]string{
		"GOLDIE_RATE_LIMIT":      "GOLDIE_RATE_LIMIT=2/m",
		"GOLDIE_RATE_LIMIT_FILE": "GOLDIE_RATE_LIMIT_FILE=" + tokenFile(t, "2/m"),
	} {
		t.Run(name, func(t *testing.T) {
			p := startServer(t, "GOLDIE_DRAIN_SECONDS=0", "GOLDIE_API_TOKENS="+binTokenLines(), env)
			url := "http://" + p.app + "/api/v1/articles/agent-loops"
			for i := range 2 {
				if got := bearerStatus(t, url, binReaderToken); got != http.StatusOK {
					t.Fatalf("reader request %d: status %d, want 200", i+1, got)
				}
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+binReaderToken)
			resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test URL
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "30" {
				t.Errorf("reader over its limit: status %d Retry-After %q, want 429 and 30", resp.StatusCode, resp.Header.Get("Retry-After"))
			}
			if got := bearerStatus(t, url, binOperatorToken); got != http.StatusOK {
				t.Errorf("operator while the reader is limited: status %d, want 200", got)
			}
			if got := bearerStatus(t, url, ""); got != http.StatusUnauthorized {
				t.Errorf("no token: status %d, want 401", got)
			}
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
			if err := p.waitExit(t, 5*time.Second); err != nil {
				t.Fatalf("exit: %v", err)
			}
			if !strings.Contains(p.logs(), `"GOLDIE_RATE_LIMIT":"2/m"`) {
				t.Errorf("the startup report does not log the effective rate limit:\n%s", p.logs())
			}
		})
	}
}

// Unset, the startup report logs the default limit.
func TestBinaryReportsTheDefaultRateLimit(t *testing.T) {
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=0")
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if err := p.waitExit(t, 5*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
	if !strings.Contains(p.logs(), `"GOLDIE_RATE_LIMIT":"20/s,burst=40"`) {
		t.Errorf("the startup report does not log the default rate limit:\n%s", p.logs())
	}
}

// An invalid GOLDIE_RATE_LIMIT, from the variable or the file, stops startup
// naming the variable and without echoing the value.
func TestBinaryRejectsAnInvalidRateLimit(t *testing.T) {
	const bad = "fast-marker/s"
	for _, form := range []string{"GOLDIE_RATE_LIMIT=" + bad, "GOLDIE_RATE_LIMIT_FILE=" + tokenFile(t, bad), "GOLDIE_RATE_LIMIT=0/s"} {
		// A server that wrongly starts must fail this case, not hang the run.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, goldieBinary(t), "server")
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"), "GOLDIE_ADDR=127.0.0.1:0", "GOLDIE_ADMIN_ADDR=127.0.0.1:0",
			"GOLDIE_CONTENT_DIR=" + contentDir(t), "GOLDIE_API_TOKEN_FILE=" + tokenFile(t, testToken), form,
		}
		out, err := cmd.CombinedOutput()
		timedOut := ctx.Err() != nil
		cancel()
		label := strings.SplitN(form, "=", 2)[0]
		if timedOut {
			t.Errorf("%s: the server started instead of failing naming GOLDIE_RATE_LIMIT", label)
			continue
		}
		if err == nil || !strings.Contains(string(out), "GOLDIE_RATE_LIMIT") {
			t.Errorf("%s: err = %v, want a failure naming GOLDIE_RATE_LIMIT: %s", label, err, out)
		}
		if strings.Contains(string(out), "fast-marker") {
			t.Errorf("%s: output echoes the value: %s", label, out)
		}
	}
}
