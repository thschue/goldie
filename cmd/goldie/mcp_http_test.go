package main

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These tests run the real binary as `goldie server` and speak MCP to its
// /mcp endpoint over streamable HTTP, the way a remote agent does (#106).

// bearerTransport sends token (none when empty) on every request.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// mcpServer starts `goldie server` over the content the stdio tests use, so
// assertBothToolsAnswer applies.
func mcpServer(t *testing.T, env ...string) *proc {
	t.Helper()
	dir, err := filepath.Abs("../../internal/app/testdata/content")
	if err != nil {
		t.Fatal(err)
	}
	return startServer(t, append([]string{"GOLDIE_DRAIN_SECONDS=0", "GOLDIE_CONTENT_DIR=" + dir}, env...)...)
}

// connectMCPHTTP connects an MCP client to the server's /mcp over streamable
// HTTP with token, including the standalone SSE stream a real client opens.
func connectMCPHTTP(t *testing.T, p *proc, token string) (*mcp.ClientSession, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{
			Endpoint:   "http://" + p.app + "/mcp",
			HTTPClient: &http.Client{Transport: bearerTransport{token}},
			MaxRetries: -1,
		}, nil)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, nil
}

// Criterion: an MCP client over HTTP against `goldie server` calls both tools.
func TestMCPHTTPServerCallsBothTools(t *testing.T) {
	p := mcpServer(t)
	cs, err := connectMCPHTTP(t, p, testToken)
	if err != nil {
		t.Fatalf("connect to goldie server /mcp: %v\n%s", err, p.logs())
	}
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools over HTTP: %v", err)
	}
	got := map[string]bool{}
	for _, tl := range res.Tools {
		got[tl.Name] = true
	}
	if len(got) != 2 || !got["search_knowledge"] || !got["get_article"] {
		t.Errorf("tools over HTTP = %v, want exactly search_knowledge and get_article", got)
	}
	assertBothToolsAnswer(t, cs)
}

// Criterion: the same client is rejected without a token, and with a wrong
// one: it cannot connect, and a raw MCP request is the 401 that names the
// Bearer scheme without echoing the token.
func TestMCPHTTPServerRejectsCallersWithoutTheToken(t *testing.T) {
	p := mcpServer(t)
	const wrong = "wrong-token-0123456789abcdef0123456789"
	for name, token := range map[string]string{"no token": "", "wrong token": wrong} {
		t.Run(name, func(t *testing.T) {
			if cs, err := connectMCPHTTP(t, p, token); err == nil {
				_ = cs.Close()
				t.Fatal("the MCP client connected, want it rejected")
			}
			const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
				`"capabilities":{},"clientInfo":{"name":"hostile","version":"v0"}}}`
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+p.app+"/mcp",
				strings.NewReader(initialize))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test URL
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("POST /mcp: status %d, want 401: %s", resp.StatusCode, body)
			}
			if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("WWW-Authenticate %q, want Bearer", got)
			}
			if !strings.Contains(string(body), `"unauthorized"`) {
				t.Errorf("body is not the unauthorized envelope: %s", body)
			}
			for _, secret := range []string{wrong, testToken} {
				if strings.Contains(string(body), secret) {
					t.Errorf("the body echoes a token: %s", body)
				}
			}
		})
	}
}

// Neither the caller's token nor a wrong one shows in the server's logs after
// MCP traffic, accepted or rejected (PC-05, PC-06).
func TestMCPHTTPServerNeverLogsTheToken(t *testing.T) {
	p := mcpServer(t)
	const wrong = "wrong-token-0123456789abcdef0123456789"
	if cs, err := connectMCPHTTP(t, p, wrong); err == nil {
		_ = cs.Close()
		t.Error("connected with a wrong token")
	}
	cs, err := connectMCPHTTP(t, p, testToken)
	if err != nil {
		t.Fatalf("connect: %v\n%s", err, p.logs())
	}
	assertBothToolsAnswer(t, cs)
	_ = cs.Close()
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if err := p.waitExit(t, 10*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
	for _, secret := range []string{testToken, wrong} {
		if strings.Contains(p.logs(), secret) {
			t.Errorf("the logs contain a token:\n%s", p.logs())
		}
	}
}

// An agent that stays connected, with its standalone SSE stream open, does
// not hold up shutdown: on SIGTERM the server still drains and exits 0
// within the bound (PC-08).
func TestMCPHTTPOpenSessionDoesNotHoldUpShutdown(t *testing.T) {
	p := mcpServer(t)
	cs, err := connectMCPHTTP(t, p, testToken)
	if err != nil {
		t.Fatalf("connect: %v\n%s", err, p.logs())
	}
	assertBothToolsAnswer(t, cs)
	time.Sleep(300 * time.Millisecond) // let the client open its standalone SSE stream
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := p.waitExit(t, 10*time.Second); err != nil {
		t.Fatalf("exit with an open MCP session: %v, want 0\n%s", err, p.logs())
	}
}
