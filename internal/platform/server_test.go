package platform

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// running is a real server on ephemeral ports.
type running struct {
	app, admin string
	cancel     context.CancelFunc
	force      chan struct{}
	done       chan error
	finished   chan struct{}
	logs       *syncBuffer
	ready      *Registry
}

func start(t *testing.T, drain time.Duration, appHandler http.Handler) *running {
	t.Helper()
	r := &running{
		force:    make(chan struct{}),
		done:     make(chan error, 1),
		finished: make(chan struct{}),
		logs:     &syncBuffer{},
		ready:    NewRegistry(100 * time.Millisecond),
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	addrs := make(chan [2]string, 2)
	srv := &Server{
		Config: Config{Addr: "127.0.0.1:0", AdminAddr: "127.0.0.1:0", DrainTimeout: drain},
		Logger: NewLogger(r.logs, 0, "server"),
		Ready:  r.ready,
		App:    appHandler,
		OnListen: func(l, a string) {
			addrs <- [2]string{l, a}
		},
	}
	go func() {
		r.done <- srv.Serve(ctx, r.force)
		close(r.finished)
	}()
	for range 2 {
		select {
		case a := <-addrs:
			if a[0] == "app" {
				r.app = a[1]
			} else {
				r.admin = a[1]
			}
		case <-time.After(5 * time.Second):
			t.Fatal("server did not start listening")
		}
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.finished:
		case <-time.After(5 * time.Second):
			close(r.force)
		}
	})
	return r
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // test URL on loopback
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestPC01AdminPortIsSeparate(t *testing.T) {
	s := start(t, 0, nil)
	for _, p := range []string{"/livez", "/readyz", "/version"} {
		if code, _ := get(t, "http://"+s.admin+p); code != 200 {
			t.Errorf("admin %s = %d, want 200", p, code)
		}
		code, body := get(t, "http://"+s.app+p)
		if code != 404 || !strings.Contains(body, `"not_found"`) {
			t.Errorf("app %s = %d %s, want the 404 not_found envelope: probes and version must not be public", p, code, body)
		}
	}
}

// Admin-port and fallback 404s carry a static next-step hint that echoes
// nothing from the request (testing checklist: every error has a hint).
func TestNotFoundCarriesStaticHint(t *testing.T) {
	s := start(t, 0, nil) // no App wired: the app mux falls back to notFound
	for name, base := range map[string]string{"admin": s.admin, "app fallback": s.app} {
		code, body := get(t, "http://"+base+"/secret-path-xyz?token=abc123")
		if code != 404 {
			t.Fatalf("%s = %d, want 404", name, code)
		}
		var env struct {
			Error struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("%s: bad envelope %q: %v", name, body, err)
		}
		hint, _ := env.Error.Details["hint"].(string)
		if env.Error.Code != "not_found" || hint == "" {
			t.Errorf("%s: want not_found with details.hint, got %s", name, body)
		}
		for _, leak := range []string{"secret-path-xyz", "abc123", "127.0.0.1"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s: body echoes %q: %s", name, leak, body)
			}
		}
	}
}

func TestPC02LivenessAndReadiness(t *testing.T) {
	s := start(t, 0, nil)
	if code, _ := get(t, "http://"+s.admin+"/livez"); code != 200 {
		t.Fatalf("/livez = %d", code)
	}
	code, body := get(t, "http://"+s.admin+"/readyz")
	if code != 200 || !strings.Contains(body, `"ready"`) {
		t.Fatalf("empty registry: /readyz = %d %s, want 200 ready", code, body)
	}

	// A check added while running that fails makes the server unready; a
	// passing one keeps it ready.
	s.ready.Add("source-a", func(context.Context) error { return nil })
	if code, _ := get(t, "http://"+s.admin+"/readyz"); code != 200 {
		t.Fatalf("passing check: /readyz = %d", code)
	}
	s.ready.Add("source-b", func(context.Context) error { return errors.New("secret detail at /etc/x") })
	code, body = get(t, "http://"+s.admin+"/readyz")
	if code != 503 {
		t.Fatalf("failing check: /readyz = %d, want 503", code)
	}
	var parsed struct {
		Status string `json:"status"`
		Checks []struct{ Name, Status string }
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("readyz body is not JSON: %v: %s", err, body)
	}
	if len(parsed.Checks) != 2 || parsed.Checks[1].Name != "source-b" || parsed.Checks[1].Status != "failed" {
		t.Fatalf("unexpected checks: %+v", parsed.Checks)
	}
	if strings.Contains(body, "secret detail") || strings.Contains(body, "/etc/x") {
		t.Fatalf("check error leaked into the body: %s", body)
	}
	// /livez is unaffected by readiness.
	if code, _ := get(t, "http://"+s.admin+"/livez"); code != 200 {
		t.Fatalf("/livez = %d while unready", code)
	}
}

func TestPC02CheckTimeout(t *testing.T) {
	s := start(t, 0, nil)
	s.ready.Add("slow", func(ctx context.Context) error {
		time.Sleep(2 * time.Second) // ignores ctx on purpose
		return nil
	})
	began := time.Now()
	code, body := get(t, "http://"+s.admin+"/readyz")
	if code != 503 || !strings.Contains(body, `"timeout"`) {
		t.Fatalf("/readyz = %d %s, want 503 with a timeout check", code, body)
	}
	if time.Since(began) > time.Second {
		t.Fatalf("readiness waited %v for a hung check", time.Since(began))
	}
}

func TestPC01IdleConnectionsAreClosed(t *testing.T) {
	old := idleTimeout
	idleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { idleTimeout = old })
	s := start(t, 0, nil)
	for addr, path := range map[string]string{s.admin: "/version", s.app: "/nothing"} {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if _, err := io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := br.ReadByte(); !errors.Is(err, io.EOF) {
			t.Errorf("%s: idle keep-alive connection not closed by the server: %v", addr, err)
		}
	}
}

func TestPC03Version(t *testing.T) {
	s := start(t, 0, nil)
	for _, base := range []string{s.admin} {
		code, body := get(t, "http://"+base+"/version")
		if code != 200 {
			t.Fatalf("%s/version = %d", base, code)
		}
		var v map[string]string
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("not JSON: %s", body)
		}
		if v["version"] != Version || v["commit"] != Commit || v["date"] != BuildDate || len(v) != 3 {
			t.Fatalf("version body = %v", v)
		}
	}
}

func TestPC05SecretsByFile(t *testing.T) {
	read := func(p string) ([]byte, error) {
		if p == "/run/secret" {
			return []byte("s3cret\n"), nil
		}
		return nil, errors.New("open /nope: no such file")
	}
	env := func(m map[string]string) Lookup {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	if v, err := Setting(env(map[string]string{"GOLDIE_X_FILE": "/run/secret"}), read, "GOLDIE_X"); err != nil || v != "s3cret" {
		t.Fatalf("file form = %q, %v", v, err)
	}
	if v, err := Setting(env(map[string]string{"GOLDIE_X": "plain"}), read, "GOLDIE_X"); err != nil || v != "plain" {
		t.Fatalf("plain form = %q, %v", v, err)
	}
	_, err := Setting(env(map[string]string{"GOLDIE_X": "plain", "GOLDIE_X_FILE": "/run/secret"}), read, "GOLDIE_X")
	if err == nil || !strings.Contains(err.Error(), "GOLDIE_X") || strings.Contains(err.Error(), "plain") {
		t.Fatalf("both forms: err = %v", err)
	}
	_, err = Setting(env(map[string]string{"GOLDIE_X_FILE": "/nope"}), read, "GOLDIE_X")
	if err == nil || strings.Contains(err.Error(), "/nope") {
		t.Fatalf("unreadable file: err = %v (must name the variable, not the path)", err)
	}
}

func TestPC05LoadConfigAcceptsFileForm(t *testing.T) {
	// Every setting goes through the same resolver, so the file form and the
	// both-set error apply to config loaded by the server.
	env := map[string]string{"GOLDIE_LOG_LEVEL": "debug", "GOLDIE_LOG_LEVEL_FILE": "/x"}
	_, err := LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err == nil || !strings.Contains(err.Error(), "GOLDIE_LOG_LEVEL") {
		t.Fatalf("err = %v", err)
	}
}

func TestPC05ConfigErrorsNeverEchoValues(t *testing.T) {
	// A value read through NAME_FILE may be secret, so a validation error names
	// the variable and never repeats what it read, in either form.
	const marker = "s3cret-marker"
	path := t.TempDir() + "/value"
	if err := os.WriteFile(path, []byte(marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{EnvAddr, EnvAdminAddr, EnvLogLevel, EnvDrainSeconds, EnvSwaggerUIEnabled} {
		for _, env := range []map[string]string{{name + "_FILE": path}, {name: marker}} {
			_, err := LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("%v: err = %v, want it to name %s", env, err, name)
				continue
			}
			if strings.Contains(err.Error(), marker) {
				t.Errorf("%v: error echoes the value: %v", env, err)
			}
		}
	}
}

func TestPC05ListenErrorsNeverEchoAddress(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	for _, tc := range []struct {
		name string
		cfg  Config
		addr string
	}{
		{EnvAddr, Config{Addr: "127.0.0.1:99999", AdminAddr: "127.0.0.1:0"}, "99999"},
		{EnvAdminAddr, Config{Addr: "127.0.0.1:0", AdminAddr: busy.Addr().String()}, busy.Addr().String()},
	} {
		srv := &Server{Config: tc.cfg, Logger: NewLogger(io.Discard, 0, "server")}
		err := srv.Serve(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), tc.name) {
			t.Errorf("%s: err = %v, want it to name the variable", tc.name, err)
			continue
		}
		if strings.Contains(err.Error(), tc.addr) {
			t.Errorf("%s: error echoes the address: %v", tc.name, err)
		}
	}
}

func TestPC04ConfigFailsFastNamingVariable(t *testing.T) {
	cases := map[string]map[string]string{
		"GOLDIE_LOG_LEVEL":     {"GOLDIE_LOG_LEVEL": "loud"},
		"GOLDIE_DRAIN_SECONDS": {"GOLDIE_DRAIN_SECONDS": "-1"},
		"GOLDIE_ADMIN_ADDR":    {"GOLDIE_ADMIN_ADDR": "nonsense"},
	}
	for name, env := range cases {
		_, err := LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v, want it to name the variable", name, err)
		}
	}
	cfg, err := LoadConfig(func(string) (string, bool) { return "", false })
	if err != nil || cfg.AdminAddr == "" || cfg.AdminAddr == cfg.Addr {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}
	_, err = LoadConfig(func(k string) (string, bool) {
		if k == "GOLDIE_ADMIN_ADDR" || k == "GOLDIE_ADDR" {
			return ":9000", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("the admin port must differ from the app port")
	}
}

func TestPC06StartupReportRedactsAndLogsOnce(t *testing.T) {
	var buf syncBuffer
	Config{Addr: ":1", AdminAddr: ":2", LogLevel: "info", DrainTimeout: 3 * time.Second}.Report(NewLogger(&buf, 0, "server"))
	if n := strings.Count(buf.String(), `"msg":"config"`); n != 1 {
		t.Fatalf("config reported %d times: %s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "GOLDIE_DRAIN_SECONDS") {
		t.Fatalf("report lacks effective values: %s", buf.String())
	}
}

func TestPC07LogsAreStructuredJSON(t *testing.T) {
	s := start(t, 0, nil)
	_ = s
	time.Sleep(10 * time.Millisecond)
	lines := strings.Split(strings.TrimSpace(s.logs.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("no log lines")
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", l)
		}
		for _, k := range []string{"ts", "level", "msg", "component", "version"} {
			if _, ok := m[k]; !ok {
				t.Errorf("log line lacks %q: %s", k, l)
			}
		}
		if m["level"] != strings.ToLower(m["level"].(string)) {
			t.Errorf("level not lower case: %s", l)
		}
	}
}

func TestPC08DrainFailsReadinessThenFinishesInFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "done")
	})
	s := start(t, 300*time.Millisecond, app)

	type result struct {
		body string
		err  error
	}
	res := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + s.app + "/slow") //nolint:gosec // loopback
		if err != nil {
			res <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		res <- result{body: string(b)}
	}()
	<-started

	s.cancel() // SIGTERM equivalent
	deadline := time.Now().Add(2 * time.Second)
	for {
		code, body := get(t, "http://"+s.admin+"/readyz")
		if code == 503 && strings.Contains(body, "draining") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("readiness never failed during drain: %d %s", code, body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, _ := get(t, "http://"+s.admin+"/livez"); code != 200 {
		t.Fatalf("/livez = %d while draining, want 200", code)
	}
	select {
	case err := <-s.done:
		t.Fatalf("server stopped with a request in flight: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	close(release)
	if r := <-res; r.err != nil || r.body != "done" {
		t.Fatalf("in-flight request = %q, %v", r.body, r.err)
	}
	select {
	case err := <-s.done:
		if err != nil {
			t.Fatalf("graceful shutdown returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
	if _, err := http.Get("http://" + s.app + "/livez"); err == nil { //nolint:gosec // loopback
		t.Fatal("app port still accepting after shutdown")
	}
}

func TestPC08SecondSignalForcesExit(t *testing.T) {
	started := make(chan struct{})
	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(time.Minute)
	})
	s := start(t, 0, app)
	go func() { _, _ = http.Get("http://" + s.app + "/hang") }() //nolint:gosec,errcheck // loopback
	<-started
	s.cancel()
	time.Sleep(100 * time.Millisecond)
	close(s.force)
	select {
	case err := <-s.done:
		if !errors.Is(err, ErrForced) {
			t.Fatalf("err = %v, want ErrForced", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("forced shutdown did not return")
	}
}

func TestPC07LogLevelIsHonoured(t *testing.T) {
	var buf syncBuffer
	lvl, err := ParseLevel("WARN")
	if err != nil {
		t.Fatal(err)
	}
	l := NewLogger(&buf, lvl, "server")
	l.Info("hidden")
	l.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), `"level":"warn"`) {
		t.Fatalf("level filtering wrong: %s", buf.String())
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Fatal("invalid level accepted")
	}
}
