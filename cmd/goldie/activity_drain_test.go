package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Acceptance test for #243: an open GET /api/v1/activity stream does not hold
// up shutdown. On SIGTERM the server ends the stream cleanly (the client reads
// to a clean end, not a cut connection), drains and exits 0 well inside the
// 30s grace (PC-08). GOLDIE_API_TOKEN is an operator token.
func TestActivityStreamEndsCleanlyOnShutdown(t *testing.T) {
	p := mcpServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+p.app+"/api/v1/activity", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	type answer struct {
		resp *http.Response
		err  error
	}
	got := make(chan answer, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test URL
		got <- answer{resp, err}
	}()
	var a answer
	select {
	case a = <-got:
	case <-time.After(10 * time.Second):
		t.Fatalf("GET /api/v1/activity: no response headers within 10s\n%s", p.logs())
	}
	if a.err != nil {
		t.Fatalf("GET /api/v1/activity: %v", a.err)
	}
	defer func() { _ = a.resp.Body.Close() }()
	if a.resp.StatusCode != http.StatusOK || !strings.HasPrefix(a.resp.Header.Get("Content-Type"), "text/event-stream") {
		b, _ := io.ReadAll(io.LimitReader(a.resp.Body, 4096))
		t.Fatalf("GET /api/v1/activity as the operator: status %d type %q, want a 200 event stream: %s",
			a.resp.StatusCode, a.resp.Header.Get("Content-Type"), b)
	}
	ended := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, a.resp.Body)
		ended <- err
	}()

	time.Sleep(200 * time.Millisecond) // the stream is open and idle
	start := time.Now()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Errorf("the stream ended with %v on shutdown, want a clean end", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the stream was still open 10s after SIGTERM\n%s", p.logs())
	}
	if err := p.waitExit(t, 10*time.Second); err != nil {
		t.Fatalf("exit with an open activity stream: %v, want 0\n%s", err, p.logs())
	}
	t.Logf("exited %v after SIGTERM", time.Since(start).Round(time.Millisecond))
	if strings.Contains(p.logs(), testToken) {
		t.Error("the logs contain the token")
	}
}
