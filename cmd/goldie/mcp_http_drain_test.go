package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

// listenBody asks to be told of tool-list changes: a request a server that
// advertised listChanged would hold open until the client leaves.
const listenBody = `{"jsonrpc":"2.0","id":1,"method":"subscriptions/listen","params":{` +
	`"notifications":{"toolsListChanged":true},"_meta":{` +
	`"io.modelcontextprotocol/protocolVersion":"2025-11-25",` +
	`"io.modelcontextprotocol/clientInfo":{"name":"parked","version":"v0"},` +
	`"io.modelcontextprotocol/clientCapabilities":{}}}}`

// Token holders who park a request on /mcp, one waiting for tool-list changes
// and one dripping its body, do not hold up shutdown: the listen is answered
// at once, and on SIGTERM the server drains and exits 0 well inside the grace
// (PC-08).
func TestMCPHTTPParkedRequestsDoNotHoldUpShutdown(t *testing.T) {
	p := mcpServer(t)

	type answer struct {
		status int
		err    error
	}
	listened := make(chan answer, 1)
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+p.app+"/mcp",
			strings.NewReader(listenBody))
		if err != nil {
			listened <- answer{err: err}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test URL
		if err != nil {
			listened <- answer{err: err}
			return
		}
		_, err = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		listened <- answer{resp.StatusCode, err}
	}()

	conn, err := net.Dial("tcp", p.app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := fmt.Fprintf(conn, "POST /mcp HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n"+
		"Content-Type: application/json\r\nAccept: application/json, text/event-stream\r\n"+
		"Content-Length: 4096\r\n\r\n{\"jsonrpc\":", p.app, testToken); err != nil {
		t.Fatal(err)
	}

	select {
	case a := <-listened:
		if a.err != nil || a.status != http.StatusOK {
			t.Errorf("subscriptions/listen: status %d, err %v, want an answer at once", a.status, a.err)
		}
	case <-time.After(5 * time.Second):
		t.Error("subscriptions/listen was held open")
	}

	time.Sleep(300 * time.Millisecond) // let the server start reading the slow body
	start := time.Now()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := p.waitExit(t, 20*time.Second); err != nil {
		t.Fatalf("exit with parked /mcp requests: %v, want 0\n%s", err, p.logs())
	}
	t.Logf("exited %v after SIGTERM", time.Since(start).Round(time.Millisecond))
}
