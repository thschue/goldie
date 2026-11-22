package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Minimum client version (#102) as the operator configures it:
// GOLDIE_MIN_CLIENT_VERSION or GOLDIE_MIN_CLIENT_VERSION_FILE, a semantic
// version. Clients sending X-Goldie-Client: goldie/<older> get 426.

// clientStatus GETs url with the reader token and X-Goldie-Client: client
// (no header when client is "") and returns the status.
func clientStatus(t *testing.T, url, client string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+binReaderToken)
	if client != "" {
		req.Header.Set("X-Goldie-Client", client)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test URL
	if err != nil {
		t.Fatal(err)
	}
	// Drain the body so the client reuses the connection and shutdown is not
	// held by a spare one.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// The binary applies GOLDIE_MIN_CLIENT_VERSION from the variable and from the
// file: an older client gets 426, a client at the minimum and a request
// without the header are served, and the startup report logs the minimum.
func TestBinaryGatesOlderClientsFromTheSetting(t *testing.T) {
	for name, env := range map[string]string{
		"GOLDIE_MIN_CLIENT_VERSION":      "GOLDIE_MIN_CLIENT_VERSION=1.4.0",
		"GOLDIE_MIN_CLIENT_VERSION_FILE": "GOLDIE_MIN_CLIENT_VERSION_FILE=" + tokenFile(t, "1.4.0"),
	} {
		t.Run(name, func(t *testing.T) {
			p := startServer(t, "GOLDIE_DRAIN_SECONDS=0", "GOLDIE_API_TOKENS="+binTokenLines(), env)
			url := "http://" + p.app + "/api/v1/articles/agent-loops"
			for client, want := range map[string]int{
				"goldie/1.3.9":  http.StatusUpgradeRequired,
				"goldie/1.4.0":  http.StatusOK,
				"goldie/1.10.0": http.StatusOK,
				"":              http.StatusOK,
				"curl/8.5.0":    http.StatusOK,
			} {
				if got := clientStatus(t, url, client); got != want {
					t.Errorf("X-Goldie-Client %q: status %d, want %d", client, got, want)
				}
			}
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
			if err := p.waitExit(t, 5*time.Second); err != nil {
				t.Fatalf("exit: %v", err)
			}
			if !strings.Contains(p.logs(), `"GOLDIE_MIN_CLIENT_VERSION":"1.4.0"`) {
				t.Errorf("the startup report does not log the minimum client version:\n%s", p.logs())
			}
		})
	}
}

// Unset, there is no gate: the oldest version is served.
func TestBinaryWithoutAMinimumGatesNothing(t *testing.T) {
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=0", "GOLDIE_API_TOKENS="+binTokenLines())
	if got := clientStatus(t, "http://"+p.app+"/api/v1/articles/agent-loops", "goldie/0.0.1"); got != http.StatusOK {
		t.Errorf("goldie/0.0.1 with no minimum: status %d, want 200", got)
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if err := p.waitExit(t, 5*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

// An invalid GOLDIE_MIN_CLIENT_VERSION, from the variable or the file, stops
// startup naming the variable and without echoing the value.
func TestBinaryRejectsAnInvalidMinClientVersion(t *testing.T) {
	bin := goldieBinary(t) // build before any timer starts
	const bad = "marker-version"
	for _, form := range []string{
		"GOLDIE_MIN_CLIENT_VERSION=" + bad,
		"GOLDIE_MIN_CLIENT_VERSION_FILE=" + tokenFile(t, bad),
		"GOLDIE_MIN_CLIENT_VERSION=1.2",
	} {
		// A server that wrongly starts must fail this case, not hang the run.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, bin, "server")
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"), "GOLDIE_ADDR=127.0.0.1:0", "GOLDIE_ADMIN_ADDR=127.0.0.1:0",
			"GOLDIE_CONTENT_DIR=" + contentDir(t), "GOLDIE_API_TOKEN_FILE=" + tokenFile(t, testToken), form,
		}
		out, err := cmd.CombinedOutput()
		timedOut := ctx.Err() != nil
		cancel()
		label := strings.SplitN(form, "=", 2)[0]
		if timedOut {
			t.Errorf("%s: the server started instead of failing naming GOLDIE_MIN_CLIENT_VERSION", label)
			continue
		}
		if err == nil || !strings.Contains(string(out), "GOLDIE_MIN_CLIENT_VERSION") {
			t.Errorf("%s: err = %v, want a failure naming GOLDIE_MIN_CLIENT_VERSION: %s", label, err, out)
		}
		if strings.Contains(string(out), "marker") {
			t.Errorf("%s: output echoes the value: %s", label, out)
		}
	}
}
