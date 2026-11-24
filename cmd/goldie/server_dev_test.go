package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// #283: goldie server --dev [dir] runs the server on loopback over the dir
// Source at dir, through the same content load as goldie server, with a
// random token, and prints how to reach it:
//
//	export GOLDIE_ADDR=http://127.0.0.1:<port> GOLDIE_TOKEN=<64 lower-case hex>
//
// That line is printed once, to stdout, and is the only place the token ever
// appears: never in a log line (stdout, stderr or the OTLP log bridge), never
// in the startup report. The public and admin listeners bind to
// 127.0.0.1:0. --dev ignores GOLDIE_ADDR, GOLDIE_ADMIN_ADDR, GOLDIE_API_TOKEN
// and GOLDIE_API_TOKENS (and their _FILE forms) from the environment, so a
// shell that ran the export line can run it again, and logs devIgnoredMsg
// once when one of them is set. Other settings (GOLDIE_DRAIN_SECONDS, the
// OTEL_ variables) apply as for goldie server. Content that does not load
// stops it with devLoadError and no path.

// devExportRE is the whole export line; group 1 is the address, group 2 the
// token.
var devExportRE = regexp.MustCompile(`^export GOLDIE_ADDR=(http://127\.0\.0\.1:[1-9][0-9]{0,4}) GOLDIE_TOKEN=([0-9a-f]{64})$`)

// devIgnoredMsg is the msg of the one log line saying the variables are
// ignored.
const devIgnoredMsg = "dev mode ignores GOLDIE_ADDR, GOLDIE_ADMIN_ADDR, GOLDIE_API_TOKEN and GOLDIE_API_TOKENS from the environment"

// devLoadError is goldie's last stderr line for a content directory that is
// missing or not a directory.
const devLoadError = "goldie: content does not load: content directory is not readable"

// devProc is a running `goldie server --dev`, with stdout and stderr kept
// apart.
type devProc struct {
	cmd         *exec.Cmd
	addr, token string // from the export line
	app, admin  string // from the "listening" log lines
	exited      chan error

	mu             sync.Mutex
	stdout, stderr []string
	changed        chan struct{}
}

func (p *devProc) lines() (stdout, stderr []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.stdout...), append([]string(nil), p.stderr...)
}

func (p *devProc) dump() string {
	out, errs := p.lines()
	return "stdout:\n" + strings.Join(out, "\n") + "\nstderr:\n" + strings.Join(errs, "\n")
}

// logRecords are every JSON line on stdout and stderr, decoded.
func (p *devProc) logRecords() []map[string]any {
	out, errs := p.lines()
	var recs []map[string]any
	for _, l := range append(out, errs...) {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil {
			recs = append(recs, m)
		}
	}
	return recs
}

func (p *devProc) capture(r io.Reader, dst *[]string, done *sync.WaitGroup) {
	defer done.Done()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		p.mu.Lock()
		*dst = append(*dst, sc.Text())
		p.mu.Unlock()
		select {
		case p.changed <- struct{}{}:
		default:
		}
	}
	if err := sc.Err(); err != nil {
		p.mu.Lock()
		*dst = append(*dst, "capture stopped: "+err.Error())
		p.mu.Unlock()
	}
}

// devEnv is a minimal environment for --dev: nothing goldie reads but the
// drain, so tests stop fast.
func devEnv(extra ...string) []string {
	return append([]string{"PATH=" + os.Getenv("PATH"), "GOLDIE_DRAIN_SECONDS=0"}, extra...)
}

// launchDev starts `goldie <args>` with env and captures its output. It does
// not wait for anything.
func launchDev(t *testing.T, dir string, env []string, args ...string) *devProc {
	t.Helper()
	return launchDevBin(t, goldieBinary(t), dir, env, args...)
}

// launchDevBin is launchDev for the binary bin.
func launchDevBin(t *testing.T, bin, dir string, env []string, args ...string) *devProc {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &devProc{cmd: cmd, exited: make(chan error, 1), changed: make(chan struct{}, 1)}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	})
	var wg sync.WaitGroup
	wg.Add(2)
	go p.capture(stdout, &p.stdout, &wg)
	go p.capture(stderr, &p.stderr, &wg)
	go func() { wg.Wait(); p.exited <- cmd.Wait() }()
	return p
}

// startDev runs `goldie server --dev args...` with env and waits for the
// export line and both "listening" lines.
func startDev(t *testing.T, env []string, args ...string) *devProc {
	t.Helper()
	return startDevIn(t, "", env, args...)
}

func startDevIn(t *testing.T, dir string, env []string, args ...string) *devProc {
	t.Helper()
	return startDevBin(t, goldieBinary(t), dir, env, args...)
}

// startDevBin is startDevIn for the binary bin.
func startDevBin(t *testing.T, bin, dir string, env []string, args ...string) *devProc {
	t.Helper()
	p := launchDevBin(t, bin, dir, env, append([]string{"server", "--dev"}, args...)...)
	deadline := time.After(10 * time.Second)
	for {
		p.scan()
		if p.token != "" && p.app != "" && p.admin != "" {
			return p
		}
		select {
		case <-p.changed:
		case err := <-p.exited:
			p.exited <- err
			t.Fatalf("goldie server --dev exited early: %v\n%s", err, p.dump())
		case <-deadline:
			t.Fatalf("goldie server --dev did not print the export line and start both listeners:\n%s", p.dump())
		}
	}
}

// scan fills addr and token from the export line and app and admin from the
// "listening" log lines.
func (p *devProc) scan() {
	out, _ := p.lines()
	for _, l := range out {
		if m := devExportRE.FindStringSubmatch(l); m != nil {
			p.addr, p.token = m[1], m[2]
		}
	}
	for _, m := range p.logRecords() {
		if m["msg"] != "listening" {
			continue
		}
		a, _ := m["addr"].(string)
		switch m["listener"] {
		case "app":
			p.app = a
		case "admin":
			p.admin = a
		}
	}
}

// stop sends SIGTERM and waits for a clean exit.
func (p *devProc) stop(t *testing.T) {
	t.Helper()
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-p.exited:
		p.exited <- err
		if err != nil {
			t.Errorf("goldie server --dev exit: %v, want 0\n%s", err, p.dump())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("goldie server --dev did not exit after SIGTERM\n%s", p.dump())
	}
}

// devStatus is the status of GET path on the --dev server with the bearer
// token (none when empty).
func devStatus(t *testing.T, base, path, token string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test URL
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

const devArticle = "/api/v1/articles/agent-loops"

// assertTokenOnlyInExportLine checks that the export line is on stdout
// exactly once and that the token appears nowhere else: no other stdout line,
// no stderr line, so no log line and not the startup report.
func assertTokenOnlyInExportLine(t *testing.T, p *devProc) {
	t.Helper()
	out, errs := p.lines()
	exports := 0
	for _, l := range out {
		switch {
		case devExportRE.MatchString(l):
			exports++
		case strings.Contains(l, "GOLDIE_TOKEN="):
			t.Errorf("stdout has a second, malformed export line: %q", l)
		case strings.Contains(l, p.token):
			t.Errorf("the token is on stdout outside the export line: %q", l)
		}
	}
	if exports != 1 {
		t.Errorf("the export line is on stdout %d times, want exactly 1\n%s", exports, p.dump())
	}
	for _, l := range errs {
		if strings.Contains(l, p.token) || strings.Contains(l, "export GOLDIE_ADDR=") {
			t.Errorf("stderr carries the token or the export line: %q", l)
		}
	}
}

// goldie server --dev prints the export line once, to stdout; the address and
// token in it reach the running server, which answers 200 with the token and
// 401 without or with another one. Both listeners are on 127.0.0.1 with a
// real port, the admin port serves the probes, and SIGTERM exits 0.
func TestServerDevPrintsAWorkingExportLineOnce(t *testing.T) {
	p := startDev(t, devEnv(), contentDir(t))
	if p.addr != "http://"+p.app {
		t.Errorf("export GOLDIE_ADDR=%s, want the public listener http://%s", p.addr, p.app)
	}
	for name, a := range map[string]string{"app": p.app, "admin": p.admin} {
		host, port, err := net.SplitHostPort(a)
		if err != nil || host != "127.0.0.1" || port == "0" {
			t.Errorf("%s listener on %q, want 127.0.0.1:<port>", name, a)
		}
	}
	if got := devStatus(t, p.addr, devArticle, p.token); got != http.StatusOK {
		t.Errorf("GET with the printed token = %d, want 200", got)
	}
	for name, tok := range map[string]string{"no token": "", "another token": strings.Repeat("0", 64), "the test token": testToken} {
		if got := devStatus(t, p.addr, devArticle, tok); got != http.StatusUnauthorized {
			t.Errorf("GET with %s = %d, want 401", name, got)
		}
	}
	for _, path := range []string{"/livez", "/readyz"} {
		if code, _ := httpGet(t, "http://"+p.admin+path); code != http.StatusOK {
			t.Errorf("admin %s = %d, want 200", path, code)
		}
	}
	p.stop(t)
	assertTokenOnlyInExportLine(t, p)
}

// The token never goes through the logger: not into a log line on stdout or
// stderr, not into the startup report, including after requests with and
// without it and the shutdown.
func TestServerDevNeverLogsTheToken(t *testing.T) {
	p := startDev(t, devEnv("GOLDIE_LOG_LEVEL=debug"), contentDir(t))
	devStatus(t, p.addr, devArticle, p.token)
	devStatus(t, p.addr, devArticle, "")
	devStatus(t, p.addr, "/mcp", p.token)
	p.stop(t)
	assertTokenOnlyInExportLine(t, p)
	reports := 0
	for _, m := range p.logRecords() {
		if m["msg"] == "config" {
			reports++
		}
	}
	if reports != 1 {
		t.Errorf("startup report logged %d times, want 1\n%s", reports, p.dump())
	}
}

// What feeds the OTLP log bridge never carries the token either: a collector
// receiving every signal sees the server's logs ("listening") but not the
// token.
func TestServerDevNeverSendsTheTokenToTelemetry(t *testing.T) {
	var mu sync.Mutex
	bodies := map[string][][]byte{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body = zr
		}
		b, _ := io.ReadAll(body)
		mu.Lock()
		bodies[r.URL.Path] = append(bodies[r.URL.Path], b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	p := startDev(t, devEnv("GOLDIE_LOG_LEVEL=debug", "OTEL_EXPORTER_OTLP_ENDPOINT="+collector.URL), contentDir(t))
	devStatus(t, p.addr, devArticle, p.token)
	devStatus(t, p.addr, devArticle, "")
	p.stop(t)
	assertTokenOnlyInExportLine(t, p)

	mu.Lock()
	defer mu.Unlock()
	logs := bytes.Join(bodies["/v1/logs"], nil)
	if !bytes.Contains(logs, []byte("listening")) {
		t.Fatalf("the collector got no server logs on /v1/logs (paths %v): the bridge was not exercised", keys(bodies))
	}
	for path, bs := range bodies {
		for _, b := range bs {
			if bytes.Contains(b, []byte(p.token)) {
				t.Errorf("the token reached the collector on %s", path)
			}
		}
	}
}

func keys(m map[string][][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Two runs make two different tokens; one run's token opens nothing on the
// other.
func TestServerDevTokensDifferPerRun(t *testing.T) {
	a := startDev(t, devEnv(), contentDir(t))
	b := startDev(t, devEnv(), contentDir(t))
	if a.token == b.token {
		t.Fatal("two runs of goldie server --dev printed the same token")
	}
	if got := devStatus(t, b.addr, devArticle, a.token); got != http.StatusUnauthorized {
		t.Errorf("the first run's token on the second = %d, want 401", got)
	}
	a.stop(t)
	b.stop(t)
}

// Without a dir argument, --dev serves the content in the working directory
// or at GOLDIE_CONTENT_DIR (both point at the same content here: the issue
// does not choose between them).
func TestServerDevWithoutADirArgument(t *testing.T) {
	p := startDevIn(t, contentDir(t), devEnv("GOLDIE_CONTENT_DIR="+contentDir(t)))
	if got := devStatus(t, p.addr, devArticle, p.token); got != http.StatusOK {
		t.Errorf("GET with the printed token = %d, want 200", got)
	}
	p.stop(t)
	assertTokenOnlyInExportLine(t, p)
}

// A dir argument wins over GOLDIE_CONTENT_DIR, even a missing one.
func TestServerDevDirArgumentWinsOverContentDir(t *testing.T) {
	p := startDev(t, devEnv("GOLDIE_CONTENT_DIR="+filepath.Join(t.TempDir(), "missing")), contentDir(t))
	if got := devStatus(t, p.addr, devArticle, p.token); got != http.StatusOK {
		t.Errorf("GET with the printed token = %d, want 200", got)
	}
	p.stop(t)
}

// Re-running --dev in a shell that ran the export line works: GOLDIE_ADDR is a
// URL there, not a listen address, and the old token is not this run's.
func TestServerDevRunsAgainAfterTheExportLine(t *testing.T) {
	first := startDev(t, devEnv(), contentDir(t))
	first.stop(t)
	second := startDev(t, devEnv("GOLDIE_ADDR="+first.addr, "GOLDIE_TOKEN="+first.token), contentDir(t))
	if second.token == first.token {
		t.Fatal("the re-run reused the token from the environment")
	}
	if got := devStatus(t, second.addr, devArticle, second.token); got != http.StatusOK {
		t.Errorf("GET with the new token = %d, want 200", got)
	}
	if got := devStatus(t, second.addr, devArticle, first.token); got != http.StatusUnauthorized {
		t.Errorf("GET with the old token = %d, want 401", got)
	}
	second.stop(t)
	assertTokenOnlyInExportLine(t, second)
	assertIgnoredOnce(t, second)
}

// assertIgnoredOnce checks for exactly one devIgnoredMsg log line.
func assertIgnoredOnce(t *testing.T, p *devProc) {
	t.Helper()
	n := 0
	for _, m := range p.logRecords() {
		if m["msg"] == devIgnoredMsg {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%q logged %d times, want once\n%s", devIgnoredMsg, n, p.dump())
	}
}

// A shell set up for a real server, or a broken one, does not stop --dev:
// bad, conflicting and unreadable token variables and non-loopback or
// malformed addresses are all ignored, one line says so, the listeners stay
// on loopback, the printed token works, and no token from the environment is
// accepted or echoed.
func TestServerDevIgnoresAddressAndTokenVariables(t *testing.T) {
	envToken := "env-token-0123456789abcdef0123456789abcdef"
	envToken2 := "env-token-two-0123456789abcdef0123456789ab"
	fileToken := "file-token-0123456789abcdef0123456789abcdef"
	missing := filepath.Join(t.TempDir(), "missing")
	for _, tc := range []struct {
		name   string
		env    []string
		secret []string // values that must be neither accepted nor echoed
	}{
		{"public addresses", []string{"GOLDIE_ADDR=0.0.0.0:0", "GOLDIE_ADMIN_ADDR=0.0.0.0:0"}, nil},
		{"malformed addresses", []string{"GOLDIE_ADDR=not an address", "GOLDIE_ADMIN_ADDR=:x"}, nil},
		{"same address twice", []string{"GOLDIE_ADDR=127.0.0.1:1", "GOLDIE_ADMIN_ADDR=127.0.0.1:1"}, nil},
		{"a valid token", []string{"GOLDIE_API_TOKEN=" + envToken}, []string{envToken}},
		{"a short token", []string{"GOLDIE_API_TOKEN=short"}, nil},
		{"token and tokens together", []string{"GOLDIE_API_TOKEN=" + envToken, "GOLDIE_API_TOKENS=ci:reader:" + envToken2}, []string{envToken, envToken2}},
		{"a token and its _FILE together", []string{"GOLDIE_API_TOKEN=" + envToken, "GOLDIE_API_TOKEN_FILE=" + tokenFile(t, fileToken)}, []string{envToken, fileToken}},
		{"a token _FILE", []string{"GOLDIE_API_TOKEN_FILE=" + tokenFile(t, fileToken)}, []string{fileToken}},
		{"a missing tokens _FILE", []string{"GOLDIE_API_TOKENS_FILE=" + missing}, nil},
		{"a malformed tokens line", []string{"GOLDIE_API_TOKENS=no-role-here " + envToken2}, []string{envToken2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := startDev(t, devEnv(tc.env...), contentDir(t))
			for name, a := range map[string]string{"app": p.app, "admin": p.admin} {
				if host, _, err := net.SplitHostPort(a); err != nil || host != "127.0.0.1" {
					t.Errorf("%s listener on %q, want 127.0.0.1", name, a)
				}
			}
			if got := devStatus(t, p.addr, devArticle, p.token); got != http.StatusOK {
				t.Errorf("GET with the printed token = %d, want 200", got)
			}
			for _, s := range tc.secret {
				if len(s) >= 32 {
					if got := devStatus(t, p.addr, devArticle, s); got != http.StatusUnauthorized {
						t.Errorf("GET with a token from the environment = %d, want 401", got)
					}
				}
			}
			p.stop(t)
			assertTokenOnlyInExportLine(t, p)
			assertIgnoredOnce(t, p)
			out, errs := p.lines()
			all := strings.Join(append(out, errs...), "\n")
			for _, s := range tc.secret {
				if len(s) >= 32 && strings.Contains(all, s) {
					t.Errorf("the output echoes a token from the environment:\n%s", all)
				}
			}
		})
	}
}

// Even with GOLDIE_ADDR and GOLDIE_ADMIN_ADDR asking for every interface,
// neither port answers on any of the host's other addresses.
func TestServerDevIsNotReachableOnOtherAddresses(t *testing.T) {
	p := startDev(t, devEnv("GOLDIE_ADDR=0.0.0.0:0", "GOLDIE_ADMIN_ADDR=0.0.0.0:0"), contentDir(t))
	defer p.stop(t)
	ips := devNonLoopbackIPs(t)
	if len(ips) == 0 {
		t.Log("this host has no non-loopback address; the listener addresses are the whole test")
	}
	for name, a := range map[string]string{"app": p.app, "admin": p.admin} {
		_, port, err := net.SplitHostPort(a)
		if err != nil {
			t.Fatalf("%s listener address %q: %v", name, a, err)
		}
		for _, ip := range ips {
			c, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), port), time.Second)
			if err == nil {
				_ = c.Close()
				t.Errorf("the %s port answers on %s: --dev must listen on 127.0.0.1 only", name, ip)
			}
		}
	}
}

// devNonLoopbackIPs are this host's addresses other than loopback and
// link-local.
func devNonLoopbackIPs(t *testing.T) []net.IP {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatalf("interface addresses: %v", err)
	}
	var ips []net.IP
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() && !n.IP.IsLinkLocalMulticast() {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

// runDevToExit runs goldie with args and env and returns its output once it
// exits; a process that keeps running fails the test.
func runDevToExit(t *testing.T, env []string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goldieBinary(t), args...)
	cmd.Env = env
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("goldie %v kept running instead of failing\nstdout:\n%s\nstderr:\n%s", args, o.String(), e.String())
	}
	return o.String(), e.String(), err
}

// Content that does not load stops --dev before it prints anything: exit
// non-zero, the fixed error as the last stderr line, no export line, and no
// path anywhere in the output.
func TestServerDevUnloadableContentFailsWithoutAPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "s3cret-no-such-dir")
	file := filepath.Join(t.TempDir(), "s3cret-a-file")
	if err := os.WriteFile(file, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"missing directory": missing, "a file": file} {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, err := runDevToExit(t, devEnv(), "server", "--dev", dir)
			if err == nil {
				t.Fatal("exit 0, want non-zero")
			}
			errLines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
			if last := errLines[len(errLines)-1]; last != devLoadError {
				t.Errorf("last stderr line = %q, want %q\nstderr:\n%s", last, devLoadError, stderr)
			}
			all := stdout + stderr
			if strings.Contains(all, "GOLDIE_TOKEN=") || strings.Contains(all, "export GOLDIE_ADDR") {
				t.Errorf("an export line was printed for content that does not load:\n%s", all)
			}
			// The error and anything else outside the JSON log records
			// (where the startup report may show the setting, as for
			// goldie server) never names the path.
			for _, l := range strings.Split(all, "\n") {
				if json.Valid([]byte(l)) {
					continue
				}
				if strings.Contains(l, dir) || strings.Contains(l, "s3cret") {
					t.Errorf("the error output echoes the path: %q", l)
				}
			}
		})
	}
}

// More than one dir is a usage error, and plain `goldie server` still takes
// no argument.
func TestServerDevTakesAtMostOneDir(t *testing.T) {
	for _, args := range [][]string{
		{"server", "--dev", contentDir(t), contentDir(t)},
		{"server", contentDir(t)},
	} {
		stdout, stderr, err := runDevToExit(t, devEnv(), args...)
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 2 {
			t.Errorf("goldie %v: err = %v, want exit code 2 for misuse", args, err)
		}
		if !strings.Contains(stderr, "Run 'goldie server --help'.") {
			t.Errorf("goldie %v: stderr = %q, want the one-line error and the next step", args, stderr)
		}
		if strings.Contains(stdout+stderr, "GOLDIE_TOKEN=") {
			t.Errorf("goldie %v printed an export line", args)
		}
	}
}
