package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// goldieBinary builds the real binary once per test run, stamping version
// info the way a release build does.
func goldieBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "goldie-bin")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "goldie")
		ld := "-X github.com/thschue/goldie/internal/platform.Version=1.2.3 " +
			"-X github.com/thschue/goldie/internal/platform.Commit=abc123 " +
			"-X github.com/thschue/goldie/internal/platform.BuildDate=2026-01-02"
		out, err := exec.Command("go", "build", "-ldflags", ld, "-o", binPath, ".").CombinedOutput()
		if err != nil {
			binErr = &buildError{string(out)}
		}
	})
	if binErr != nil {
		t.Fatalf("build: %v", binErr)
	}
	return binPath
}

type buildError struct{ out string }

func (e *buildError) Error() string { return e.out }

func TestMain(m *testing.M) {
	code := m.Run()
	if binPath != "" {
		_ = os.RemoveAll(filepath.Dir(binPath))
	}
	os.Exit(code)
}

// proc is a running `goldie server`.
type proc struct {
	cmd         *exec.Cmd
	app, admin  string
	lines       chan string
	exited      chan error
	allLogsLock sync.Mutex
	allLogs     []string
}

func (p *proc) logs() string {
	p.allLogsLock.Lock()
	defer p.allLogsLock.Unlock()
	return strings.Join(p.allLogs, "\n")
}

// testToken is the API token startServer passes through GOLDIE_API_TOKEN_FILE.
const testToken = "binary-test-token-0123456789abcdef"

// contentDir is the example content, a valid dir Source.
func contentDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../../examples/knowledge")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// tokenFile writes value, with a trailing newline as editors and Secrets
// leave it, to a file and returns its path.
func tokenFile(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// startServer starts the binary with env added to a working configuration.
// The API token is testToken from GOLDIE_API_TOKEN_FILE unless env sets
// GOLDIE_API_TOKEN or GOLDIE_API_TOKEN_FILE itself.
func startServer(t *testing.T, env ...string) *proc {
	t.Helper()
	cmd := exec.Command(goldieBinary(t), "server")
	base := []string{
		"PATH=" + os.Getenv("PATH"),
		"GOLDIE_ADDR=127.0.0.1:0",
		"GOLDIE_ADMIN_ADDR=127.0.0.1:0",
		"GOLDIE_CONTENT_DIR=" + contentDir(t),
	}
	if !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "GOLDIE_API_TOKEN") }) {
		base = append(base, "GOLDIE_API_TOKEN_FILE="+tokenFile(t, testToken))
	}
	cmd.Env = append(base, env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &proc{cmd: cmd, lines: make(chan string, 256), exited: make(chan error, 1)}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			p.allLogsLock.Lock()
			p.allLogs = append(p.allLogs, sc.Text())
			p.allLogsLock.Unlock()
			select {
			case p.lines <- sc.Text():
			default:
			}
		}
		if err := sc.Err(); err != nil {
			p.allLogsLock.Lock()
			p.allLogs = append(p.allLogs, "log capture stopped: "+err.Error())
			p.allLogsLock.Unlock()
		}
		p.exited <- cmd.Wait()
	}()
	for p.app == "" || p.admin == "" {
		select {
		case l := <-p.lines:
			var m map[string]any
			if json.Unmarshal([]byte(l), &m) == nil && m["msg"] == "listening" {
				if m["listener"] == "app" {
					p.app, _ = m["addr"].(string)
				} else {
					p.admin, _ = m["addr"].(string)
				}
			}
		case err := <-p.exited:
			t.Fatalf("server exited early: %v\n%s", err, p.logs())
		case <-time.After(10 * time.Second):
			t.Fatalf("server did not start: %s", p.logs())
		}
	}
	return p
}

func (p *proc) waitExit(t *testing.T, within time.Duration) error {
	t.Helper()
	select {
	case err := <-p.exited:
		return err
	case <-time.After(within):
		t.Fatalf("process still running after %v\n%s", within, p.logs())
		return nil
	}
}

func httpGet(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // loopback test URL
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestPC01And03BinaryServesProbesAndVersion(t *testing.T) {
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=0")
	defer func() { _ = p.cmd.Process.Signal(syscall.SIGTERM); _ = p.waitExit(t, 5*time.Second) }()
	for _, path := range []string{"/livez", "/readyz", "/version"} {
		if code, _ := httpGet(t, "http://"+p.admin+path); code != 200 {
			t.Errorf("admin %s = %d", path, code)
		}
		if code, _ := httpGet(t, "http://"+p.app+path); code != 404 {
			t.Errorf("app %s = %d, want 404", path, code)
		}
	}
	_, body := httpGet(t, "http://"+p.admin+"/version")
	want := `{"version":"1.2.3","commit":"abc123","date":"2026-01-02"}`
	if strings.TrimSpace(body) != want {
		t.Errorf("%s/version = %s, want %s", p.admin, body, want)
	}
}

// The OpenAPI document is served on the public listener only; the admin port
// keeps to probes and version (PC-01).
func TestBinaryServesOpenAPISpecOnPublicListener(t *testing.T) {
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=0")
	defer func() { _ = p.cmd.Process.Signal(syscall.SIGTERM); _ = p.waitExit(t, 5*time.Second) }()
	for path, want := range map[string]string{
		"/openapi.yaml": "application/yaml",
		"/openapi.json": "application/json",
	} {
		resp, err := http.Get("http://" + p.app + path) //nolint:gosec // loopback test URL
		if err != nil {
			t.Fatalf("GET app %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), want) {
			t.Errorf("app %s = %d %q, want 200 %q", path, resp.StatusCode, resp.Header.Get("Content-Type"), want)
		}
		if code, _ := httpGet(t, "http://"+p.admin+path); code != 404 {
			t.Errorf("admin %s = %d, want 404", path, code)
		}
	}
}

func TestPC04InvalidConfigFailsFast(t *testing.T) {
	for _, tc := range []struct{ env, name string }{
		{"GOLDIE_LOG_LEVEL=loud", "GOLDIE_LOG_LEVEL"},
		{"GOLDIE_DRAIN_SECONDS=abc", "GOLDIE_DRAIN_SECONDS"},
		{"GOLDIE_ADMIN_ADDR=oops", "GOLDIE_ADMIN_ADDR"},
	} {
		cmd := exec.Command(goldieBinary(t), "server")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), tc.env}
		done := make(chan struct{})
		var out []byte
		var err error
		go func() { out, err = cmd.CombinedOutput(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatalf("%s: server did not fail fast", tc.name)
		}
		if err == nil {
			t.Errorf("%s: exit 0, want non-zero", tc.name)
		}
		if !strings.Contains(string(out), tc.name) {
			t.Errorf("%s: output does not name the variable: %s", tc.name, out)
		}
	}
}

// The binary serves the API from GOLDIE_CONTENT_DIR to callers with the
// token from GOLDIE_API_TOKEN_FILE, and never logs the token (PC-05, PC-06).
func TestBinaryServesAPIWithTokenFromFile(t *testing.T) {
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=0")
	url := "http://" + p.app + "/api/v1/articles/agent-loops"
	for auth, want := range map[string]int{"": 401, "Bearer wrong": 401, "Bearer " + testToken: 200} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test URL
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Authorization %q: status %d, want %d: %s", auth, resp.StatusCode, want, body)
		}
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if err := p.waitExit(t, 5*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
	if strings.Contains(p.logs(), testToken) {
		t.Errorf("the logs contain the API token:\n%s", p.logs())
	}
	if !strings.Contains(p.logs(), `"GOLDIE_API_TOKEN":"(redacted)"`) {
		t.Errorf("the startup report does not show the token as redacted:\n%s", p.logs())
	}
}

func TestPC04ServerNeedsTokenAndContent(t *testing.T) {
	// marker is long enough that its space is what fails; short is one byte
	// under the minimum.
	const marker = "s3cret marker 0123456789abcdef0123"
	short := testToken[:31]
	content := "GOLDIE_CONTENT_DIR=" + contentDir(t)
	token := "GOLDIE_API_TOKEN_FILE=" + tokenFile(t, testToken)
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{"GOLDIE_API_TOKEN", []string{content}},
		{"GOLDIE_API_TOKEN", []string{content, "GOLDIE_API_TOKEN="}},
		{"GOLDIE_API_TOKEN", []string{content, "GOLDIE_API_TOKEN_FILE=" + tokenFile(t, marker)}},
		{"GOLDIE_API_TOKEN", []string{content, token, "GOLDIE_API_TOKEN=" + testToken}},
		{"GOLDIE_API_TOKEN", []string{content, "GOLDIE_API_TOKEN=" + short}},
		{"GOLDIE_API_TOKEN", []string{content, "GOLDIE_API_TOKEN_FILE=" + tokenFile(t, short)}},
		{"GOLDIE_CONTENT_DIR", []string{token}},
		{"GOLDIE_CONTENT_DIR", []string{token, "GOLDIE_CONTENT_DIR=" + filepath.Join(t.TempDir(), "missing")}},
	} {
		// A server that wrongly starts must fail this case, not hang the run.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, goldieBinary(t), "server")
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "GOLDIE_ADDR=127.0.0.1:0", "GOLDIE_ADMIN_ADDR=127.0.0.1:0"}, tc.env...)
		out, err := cmd.CombinedOutput()
		timedOut := ctx.Err() != nil
		cancel()
		if timedOut {
			t.Errorf("%v: the server started instead of failing naming %s", tc.env, tc.name)
			continue
		}
		if err == nil || !strings.Contains(string(out), tc.name) {
			t.Errorf("%v: err = %v, want a failure naming %s: %s", tc.env, err, tc.name, out)
		}
		if strings.Contains(string(out), marker) || strings.Contains(string(out), short) {
			t.Errorf("%v: output echoes the token: %s", tc.env, out)
		}
		if strings.Contains(string(out), "31 byte") {
			t.Errorf("%v: output gives the token's length: %s", tc.env, out)
		}
	}
}

// A token of exactly the minimum length starts the server, from the plain
// variable and from a file with a trailing newline.
func TestBinaryAcceptsMinimumLengthToken(t *testing.T) {
	atMin := testToken[:32]
	for name, env := range map[string]string{
		"GOLDIE_API_TOKEN":      "GOLDIE_API_TOKEN=" + atMin,
		"GOLDIE_API_TOKEN_FILE": "GOLDIE_API_TOKEN_FILE=" + tokenFile(t, atMin),
	} {
		t.Run(name, func(t *testing.T) {
			p := startServer(t, "GOLDIE_DRAIN_SECONDS=0", env)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+p.app+"/api/v1/articles/agent-loops", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+atMin)
			resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test URL
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status %d, want 200", resp.StatusCode)
			}
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
			if err := p.waitExit(t, 5*time.Second); err != nil {
				t.Fatalf("exit: %v", err)
			}
		})
	}
}

func TestPC05BothSecretFormsRejected(t *testing.T) {
	cmd := exec.Command(goldieBinary(t), "server")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GOLDIE_LOG_LEVEL=info", "GOLDIE_LOG_LEVEL_FILE=/dev/null"}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "GOLDIE_LOG_LEVEL") {
		t.Fatalf("both forms must fail naming the variable: %v %s", err, out)
	}
}

func TestPC06And07StartupReportAndJSONLogs(t *testing.T) {
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=0")
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if err := p.waitExit(t, 5*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
	reports := 0
	for _, l := range strings.Split(p.logs(), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %q", l)
		}
		for _, k := range []string{"ts", "level", "msg", "component", "version"} {
			if _, ok := m[k]; !ok {
				t.Errorf("line lacks %q: %s", k, l)
			}
		}
		if m["version"] != "1.2.3" {
			t.Errorf("version = %v", m["version"])
		}
		if m["msg"] == "config" {
			reports++
			if m["GOLDIE_DRAIN_SECONDS"] != float64(0) {
				t.Errorf("report misses effective config: %s", l)
			}
		}
	}
	if reports != 1 {
		t.Errorf("config reported %d times, want 1", reports)
	}
}

func TestPC08SigtermDrainsThenExitsZero(t *testing.T) {
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=2")
	began := time.Now()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// Readiness fails first while the listeners keep answering.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for {
		code, _ := httpGet(t, "http://"+p.admin+"/readyz")
		if code == 503 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/readyz stayed %d after SIGTERM", code)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, _ := httpGet(t, "http://"+p.admin+"/livez"); code != 200 {
		t.Errorf("/livez = %d during drain", code)
	}
	if err := p.waitExit(t, 10*time.Second); err != nil {
		t.Fatalf("exit: %v, want 0", err)
	}
	if time.Since(began) < 2*time.Second {
		t.Errorf("exited after %v, before the 2s drain elapsed", time.Since(began))
	}
}

func TestPC08SecondSignalExitsImmediately(t *testing.T) {
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=60")
	_ = p.cmd.Process.Signal(syscall.SIGINT)
	time.Sleep(200 * time.Millisecond)
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	err := p.waitExit(t, 5*time.Second)
	if err == nil {
		t.Fatal("forced exit should be non-zero")
	}
}

func TestPC09HealthNeverEchoesAddress(t *testing.T) {
	cmd := exec.Command(goldieBinary(t), "health")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GOLDIE_ADMIN_ADDR=s3cret:mark:er"}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "GOLDIE_ADMIN_ADDR") {
		t.Fatalf("health with a bad address: %v %s, want non-zero naming the variable", err, out)
	}
	if strings.Contains(string(out), "s3cret") {
		t.Fatalf("health echoes the address: %s", out)
	}
}

func TestPC09HealthSubcommand(t *testing.T) {
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=30")
	run := func(args ...string) error {
		cmd := exec.Command(goldieBinary(t), append([]string{"health"}, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GOLDIE_ADMIN_ADDR=" + p.admin}
		return cmd.Run()
	}
	if err := run(); err != nil {
		t.Fatalf("health on a live server: %v", err)
	}
	if err := run("--ready"); err != nil {
		t.Fatalf("health --ready on a ready server: %v", err)
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(2 * time.Second)
	for run("--ready") == nil {
		if time.Now().After(deadline) {
			t.Fatal("health --ready kept passing while draining")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := run(); err != nil {
		t.Errorf("plain health must stay green while draining (liveness): %v", err)
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	_ = p.waitExit(t, 5*time.Second)
	if err := run(); err == nil {
		t.Fatal("health must exit non-zero when nothing listens")
	}
}
