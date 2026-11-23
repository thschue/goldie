package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// #257: the Syncer lives in the composition root. PublicHandler returns a
// *Public, an http.Handler with Start(ctx, *platform.Registry), which starts
// the dir Source's poll loop and registers its readiness check "source:dir"
// (PC-02). The first load still happens inside PublicHandler, so content that
// will not load stops startup. Start returns without blocking; the poll loop
// ends when ctx ends.
//
// Existing callers (`h, err := PublicHandler(...)`, then used as an
// http.Handler) compile unchanged, so no existing test is migrated.
// PublicHandler's return type cannot be stubbed in a test file: these tests do
// not compile until the implementation lands.

// publicConfig is a loaded Config for the test content on ephemeral ports.
func publicConfig(t *testing.T) platform.Config {
	t.Helper()
	return loadedConfig(t, map[string]string{
		platform.EnvContentDir:       testContent,
		platform.EnvAPIToken:         testToken,
		platform.EnvAddr:             "127.0.0.1:0",
		platform.EnvAdminAddr:        "127.0.0.1:0",
		platform.EnvDrainSeconds:     "0",
		platform.EnvSwaggerUIEnabled: "false",
	})
}

// adminServer serves pub on a real platform.Server with reg and returns the
// admin address. The server stops with the test.
func adminServer(t *testing.T, cfg platform.Config, pub http.Handler, reg *platform.Registry) string {
	t.Helper()
	addrs := make(chan string, 2)
	srv := &platform.Server{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Ready: reg, App: pub,
		OnListen: func(l, a string) {
			if l == "admin" {
				addrs <- a
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, make(chan struct{})) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	select {
	case a := <-addrs:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("admin listener did not start")
		return ""
	}
}

type readyzCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func readyz(t *testing.T, admin string) (int, string, []readyzCheck) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+admin+"/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var body struct {
		Checks []readyzCheck `json:"checks"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatalf("/readyz body is not JSON: %v: %s", err, b)
	}
	return resp.StatusCode, string(b), body.Checks
}

// startPublic returns the Public for publicConfig, failing the test on error.
func startPublic(t *testing.T) *Public {
	t.Helper()
	pub, err := PublicHandler(publicConfig(t), nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	if pub == nil {
		t.Fatal("PublicHandler returned a nil *Public without an error")
	}
	return pub
}

// startReturns runs pub.Start and fails if it blocks.
func startReturns(t *testing.T, pub *Public, ctx context.Context, reg *platform.Registry) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); pub.Start(ctx, reg) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start blocked: it must start the poll loop and return")
	}
}

// pollLoops counts goroutines running the Syncer's poll loop.
func pollLoops() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "(*Syncer).Run")
}

// waitLoops waits for pollLoops to equal want.
func waitLoops(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := pollLoops()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("poll loops = %d, want %d", got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Admin /readyz lists source:dir as ok once Start ran (PC-02), answers 200,
// and shows only name and status: no path, no error text.
func TestStartListsSourceDirReadyOnAdminReadyz(t *testing.T) {
	pub := startPublic(t)
	reg := platform.NewRegistry(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startReturns(t, pub, ctx, reg)
	admin := adminServer(t, publicConfig(t), pub, reg)

	code, raw, checks := readyz(t, admin)
	if code != http.StatusOK {
		t.Fatalf("/readyz = %d, want 200: %s", code, raw)
	}
	if len(checks) != 1 || checks[0] != (readyzCheck{Name: "source:dir", Status: "ok"}) {
		t.Fatalf("checks = %+v, want exactly [source:dir ok]: %s", checks, raw)
	}
	abs, _ := filepath.Abs(testContent)
	for _, leak := range []string{testContent, abs, "rror"} {
		if strings.Contains(raw, leak) {
			t.Errorf("/readyz body contains %q: %s", leak, raw)
		}
	}
}

// PublicHandler alone registers nothing: the Source is listed only once a
// server has started it.
func TestSourceDirIsNotListedBeforeStart(t *testing.T) {
	pub := startPublic(t)
	reg := platform.NewRegistry(0)
	admin := adminServer(t, publicConfig(t), pub, reg)
	if _, raw, checks := readyz(t, admin); len(checks) != 0 {
		t.Fatalf("checks before Start = %+v, want none: %s", checks, raw)
	}
}

// A second Start never starts a second poll loop, never panics, and leaves
// one source:dir check.
func TestStartTwiceStartsOnePollLoop(t *testing.T) {
	waitLoops(t, 0)
	pub := startPublic(t)
	reg := platform.NewRegistry(0)
	ctx, cancel := context.WithCancel(context.Background())
	startReturns(t, pub, ctx, reg)
	waitLoops(t, 1)
	startReturns(t, pub, ctx, reg)
	startReturns(t, pub, ctx, platform.NewRegistry(0))
	time.Sleep(100 * time.Millisecond)
	if got := pollLoops(); got != 1 {
		t.Fatalf("poll loops after Start x3 = %d, want 1", got)
	}
	admin := adminServer(t, publicConfig(t), pub, reg)
	if _, raw, checks := readyz(t, admin); len(checks) != 1 || checks[0].Name != "source:dir" {
		t.Fatalf("checks = %+v, want exactly one source:dir: %s", checks, raw)
	}
	cancel()
	waitLoops(t, 0)
}

// Concurrent Starts are safe under -race and still run one loop.
func TestStartConcurrentCallsRunOnePollLoop(t *testing.T) {
	waitLoops(t, 0)
	pub := startPublic(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := platform.NewRegistry(0)
	done := make(chan struct{})
	for range 8 {
		go func() { pub.Start(ctx, reg); done <- struct{}{} }()
	}
	for range 8 {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Start blocked")
		}
	}
	time.Sleep(100 * time.Millisecond)
	if got := pollLoops(); got != 1 {
		t.Fatalf("poll loops after 8 concurrent Starts = %d, want 1", got)
	}
}

// Cancelling the server context (a drain) ends the poll loop. The Source
// stays ready, because a stale Source stays ready (PC-02), and the last good
// Snapshot is still served.
func TestCancelStopsPollLoopAndKeepsSourceReady(t *testing.T) {
	waitLoops(t, 0)
	pub := startPublic(t)
	reg := platform.NewRegistry(0)
	ctx, cancel := context.WithCancel(context.Background())
	startReturns(t, pub, ctx, reg)
	admin := adminServer(t, publicConfig(t), pub, reg)
	waitLoops(t, 1)
	cancel()
	waitLoops(t, 0)
	code, raw, checks := readyz(t, admin)
	if code != http.StatusOK || len(checks) != 1 || checks[0].Status != "ok" {
		t.Fatalf("after cancel /readyz = %d %s, want 200 with source:dir ok", code, raw)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, lookupPath, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	pub.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("after cancel the API answers %d, want 200: %s", rec.Code, rec.Body)
	}
}

// Start with a context that is already cancelled, and a cancel racing Start,
// neither panic nor hang nor leave a loop running.
func TestStartWithCancelledAndRacingContext(t *testing.T) {
	waitLoops(t, 0)
	pub := startPublic(t)
	dead, cancelDead := context.WithCancel(context.Background())
	cancelDead()
	startReturns(t, pub, dead, platform.NewRegistry(0))
	waitLoops(t, 0)

	pub2 := startPublic(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); pub2.Start(ctx, platform.NewRegistry(0)) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start hung while its context was cancelled")
	}
	waitLoops(t, 0)
}

// A dir Source that will not load still stops startup, in PublicHandler,
// before any Start, naming the variable and never the path. The result is a
// nil *Public.
func TestUnloadableContentStillFailsInPublicHandler(t *testing.T) {
	cfg := publicConfig(t)
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	cfg.ContentDir = missing
	pub, err := PublicHandler(cfg, nil)
	if err == nil {
		t.Fatal("PublicHandler accepted content that cannot load")
	}
	if pub != nil {
		t.Errorf("PublicHandler returned a non-nil *Public with error %v", err)
	}
	if !strings.Contains(err.Error(), platform.EnvContentDir) || strings.Contains(err.Error(), missing) {
		t.Errorf("error = %q, want it to name %s and not the path", err, platform.EnvContentDir)
	}
}

// #476: only GOLDIE_SOURCES set and valid. The server cannot serve a list yet
// and says so, rather than asking for a variable the operator may not want.
func TestPublicHandlerWithOnlyValidSourcesSaysListsAreNotServedYet(t *testing.T) {
	cfg := publicConfig(t)
	cfg.ContentDir = ""
	cfg.GitURL = ""
	cfg.Sources = []platform.SourceSpec{{Name: "local", Type: "dir", Dir: t.TempDir()}}
	pub, err := PublicHandler(cfg, nil)
	if err == nil {
		t.Fatal("PublicHandler accepted a list of Sources it cannot serve")
	}
	if pub != nil {
		t.Errorf("PublicHandler returned a non-nil *Public with error %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, platform.EnvSources) || !strings.Contains(msg, "cannot serve a list of Sources yet") ||
		strings.Contains(msg, "needs one Source") {
		t.Errorf("error = %q, want it to say %s is valid but a list is not served yet", msg, platform.EnvSources)
	}
}
