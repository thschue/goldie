package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Named tokens (#99) as the operator configures them: GOLDIE_API_TOKENS or
// GOLDIE_API_TOKENS_FILE with name:role:token and name:role:sha256:<hex>
// lines.
const (
	binReaderToken   = "binary-reader-token-0123456789abcdef0123"
	binOperatorToken = "binary-operator-token-0123456789abcdef01"
	binHashedSecret  = "binary-hashed-secret-0123456789abcdef012"
)

func binSHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func binTokenLines() string {
	return "ci-reader:reader:" + binReaderToken + "\n" +
		"ci-operator:operator:" + binOperatorToken + "\n" +
		"ci-hashed:reader:sha256:" + binSHA256Hex(binHashedSecret)
}

// bearerStatus is the status of an authenticated GET; token "" sends none.
func bearerStatus(t *testing.T, url, token string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test URL
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// The binary serves the API to every configured token, plaintext and hashed,
// from GOLDIE_API_TOKENS and from GOLDIE_API_TOKENS_FILE, without
// GOLDIE_API_TOKEN set. The digest of a hashed entry is not a credential, and
// neither secrets nor digests reach the logs (PC-05, PC-06).
func TestBinaryServesNamedTokensFromEnvAndFile(t *testing.T) {
	for name, env := range map[string]string{
		"GOLDIE_API_TOKENS":      "GOLDIE_API_TOKENS=" + binTokenLines(),
		"GOLDIE_API_TOKENS_FILE": "GOLDIE_API_TOKENS_FILE=" + tokenFile(t, binTokenLines()),
	} {
		t.Run(name, func(t *testing.T) {
			p := startServer(t, "GOLDIE_DRAIN_SECONDS=0", env)
			for _, path := range []string{"/api/v1/articles/agent-loops", "/api/v1/search?q=agent"} {
				url := "http://" + p.app + path
				for token, want := range map[string]int{
					binReaderToken:                http.StatusOK,
					binOperatorToken:              http.StatusOK,
					binHashedSecret:               http.StatusOK,
					"":                            http.StatusUnauthorized,
					testToken:                     http.StatusUnauthorized,
					binSHA256Hex(binHashedSecret): http.StatusUnauthorized,
					"sha256:" + binSHA256Hex(binHashedSecret): http.StatusUnauthorized,
				} {
					if got := bearerStatus(t, url, token); got != want {
						t.Errorf("%s with token %.12s…: status %d, want %d", path, token, got, want)
					}
				}
			}
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
			if err := p.waitExit(t, 5*time.Second); err != nil {
				t.Fatalf("exit: %v", err)
			}
			for _, secret := range []string{binReaderToken, binOperatorToken, binHashedSecret, binSHA256Hex(binHashedSecret)} {
				if strings.Contains(p.logs(), secret) {
					t.Errorf("the logs contain a token or digest:\n%s", p.logs())
				}
			}
			if !strings.Contains(p.logs(), `"GOLDIE_API_TOKENS":"(redacted)"`) {
				t.Errorf("the startup report does not show GOLDIE_API_TOKENS as redacted:\n%s", p.logs())
			}
		})
	}
}

// A plaintext line under the 32-byte minimum stops startup, from the variable
// and from the file, on the first or a later line. So do malformed lines and
// unknown Roles. The output names GOLDIE_API_TOKENS but never the token, any
// part of it, or its length.
func TestBinaryRejectsShortOrMalformedTokenLines(t *testing.T) {
	short := binReaderToken[:31]
	good := "ok:operator:" + binOperatorToken
	for _, tc := range []struct {
		name  string
		lines string
	}{
		{"short plaintext line", "ci:reader:" + short},
		{"short line after a good one", good + "\nci:reader:" + short},
		{"unknown role", "ci:admin:" + binReaderToken},
		{"no role", "ci:" + binReaderToken},
		{"empty name", ":reader:" + binReaderToken},
		{"hash that is not hex", "ci:reader:sha256:" + strings.Repeat("zz", 32)},
	} {
		for _, form := range []string{"GOLDIE_API_TOKENS=" + tc.lines, "GOLDIE_API_TOKENS_FILE=" + tokenFile(t, tc.lines)} {
			// A server that wrongly starts must fail this case, not hang the run.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cmd := exec.CommandContext(ctx, goldieBinary(t), "server")
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"), "GOLDIE_ADDR=127.0.0.1:0", "GOLDIE_ADMIN_ADDR=127.0.0.1:0",
				"GOLDIE_CONTENT_DIR=" + contentDir(t), form,
			}
			out, err := cmd.CombinedOutput()
			timedOut := ctx.Err() != nil
			cancel()
			label := tc.name + " via " + strings.SplitN(form, "=", 2)[0]
			if timedOut {
				t.Errorf("%s: the server started instead of failing naming GOLDIE_API_TOKENS", label)
				continue
			}
			if err == nil || !strings.Contains(string(out), "GOLDIE_API_TOKENS") {
				t.Errorf("%s: err = %v, want a failure naming GOLDIE_API_TOKENS: %s", label, err, out)
			}
			for _, secret := range []string{short, binReaderToken, binOperatorToken, short[:8], short[len(short)-8:]} {
				if strings.Contains(string(out), secret) {
					t.Errorf("%s: output echoes a token: %s", label, out)
				}
			}
			for _, length := range []string{"31 byte", "31-byte", "(31)"} {
				if strings.Contains(string(out), length) {
					t.Errorf("%s: output gives the token's length: %s", label, out)
				}
			}
		}
	}
}

// A plaintext line of exactly 32 bytes starts the server and authenticates.
func TestBinaryAcceptsMinimumLengthTokenLine(t *testing.T) {
	atMin := binReaderToken[:32]
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=0", "GOLDIE_API_TOKENS=ci:reader:"+atMin)
	if got := bearerStatus(t, "http://"+p.app+"/api/v1/articles/agent-loops", atMin); got != http.StatusOK {
		t.Errorf("status %d, want 200", got)
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if err := p.waitExit(t, 5*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}
