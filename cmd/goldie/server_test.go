package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

func startServer(t *testing.T, env ...string) *proc {
	t.Helper()
	cmd := exec.Command(goldieBinary(t), "server")
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"GOLDIE_ADDR=127.0.0.1:0",
		"GOLDIE_ADMIN_ADDR=127.0.0.1:0",
	}, env...)
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
	for _, path := range []string{"/livez", "/readyz"} {
		if code, _ := httpGet(t, "http://"+p.admin+path); code != 200 {
			t.Errorf("admin %s = %d", path, code)
		}
		if code, _ := httpGet(t, "http://"+p.app+path); code != 404 {
			t.Errorf("app %s = %d, want 404", path, code)
		}
	}
	for _, addr := range []string{p.admin, p.app} {
		_, body := httpGet(t, "http://"+addr+"/version")
		want := `{"version":"1.2.3","commit":"abc123","date":"2026-01-02"}`
		if strings.TrimSpace(body) != want {
			t.Errorf("%s/version = %s, want %s", addr, body, want)
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
