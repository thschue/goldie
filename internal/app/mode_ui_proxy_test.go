package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// #636 fix round: what the ui-mode proxy forwards, how it drains and what it
// tells the operator.

// noRedirect is a client that shows a redirect instead of following it.
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestModeUIAnswers404ForEncodedDotSegments(t *testing.T) {
	var mu sync.Mutex
	var reached []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reached = append(reached, r.URL.EscapedPath())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(upstream.Close)
	srv := uiModeServer(t, upstream.URL)

	for _, p := range []string{"/api/%2e%2e/x", "/api/..%2fx", "/api/%2E%2E/x", "/api/v1/%2e/x", "/swagger-ui/%2e%2e/api/v1/x"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+p, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := noRedirect().Do(req) //nolint:gosec // test server URL
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want the 404 envelope", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "")
	}
	snapshot := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), reached...) }
	if got := snapshot(); len(got) != 0 {
		t.Errorf("the upstream was reached with %v, want nothing", got)
	}

	// An encoded slash inside a segment is a Project ID and still goes through.
	resp, _ := uiDo(t, http.MethodGet, srv.URL+"/api/v1/projects/github.com%2Facme%2Fapp/items", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET with an encoded Project ID: status %d, want 200 from the upstream", resp.StatusCode)
	}
	if got := snapshot(); len(got) != 1 || !strings.Contains(got[0], "github.com%2Facme%2Fapp") {
		t.Errorf("the upstream saw %v, want the Project ID with its encoded slashes", got)
	}
}

// The first stop signal ends held streams only: a request that is not a
// stream finishes during the drain, and a new one is still served (PC-08).
func TestModeUIDrainEndsStreamsOnlyAndFinishesOtherRequests(t *testing.T) {
	release := make(chan struct{})
	slowIn := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/slow":
			close(slowIn)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		case "/api/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: 1\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(upstream.Close)
	var releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	pub := uiModeHandler(t, upstream.URL)
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)

	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	startReturns(t, pub, ctx, platform.NewRegistry(0))

	type result struct {
		status int
		err    error
	}
	slow := make(chan result, 1)
	go func() {
		resp, err := http.Get(srv.URL + "/api/slow") //nolint:gosec,noctx // test server URL
		if err != nil {
			slow <- result{err: err}
			return
		}
		_ = resp.Body.Close()
		slow <- result{status: resp.StatusCode}
	}()
	<-slowIn

	streamResp, err := http.Get(srv.URL + "/api/stream") //nolint:gosec,noctx // test server URL
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = streamResp.Body.Close() })
	streamEnded := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, streamResp.Body); close(streamEnded) }()

	stop() // the first stop signal
	select {
	case <-streamEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("the held stream is still open 5s after the stop signal")
	}

	resp, body := uiDo(t, http.MethodGet, srv.URL+"/api/other", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a new request during the drain: status %d %s, want 200", resp.StatusCode, body)
	}
	open()
	select {
	case r := <-slow:
		if r.err != nil || r.status != http.StatusOK {
			t.Errorf("the request in flight at the stop signal: status %d, err %v, want 200", r.status, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight request never finished")
	}
}

// A failing upstream is one rate-limited warn line for the operator, and the
// client still gets the envelope without the address.
func TestModeUILogsUpstreamFailuresRateLimited(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	logger := slog.New(slog.NewTextHandler(lockedWriter{&mu, &buf}, nil))
	cfg := loadedConfig(t, map[string]string{envMode: "ui", envUpstreamAddr: deadAddr(t)})
	pub, err := PublicHandler(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	for range 5 {
		resp, _ := uiDo(t, http.MethodGet, srv.URL+"/api/v1/search?q=x", nil)
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("status %d, want 502", resp.StatusCode)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	lines := strings.Count(buf.String(), "upstream request failed")
	if lines != 1 {
		t.Errorf("%d warn lines for 5 failures, want exactly 1:\n%s", lines, buf.String())
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
