package app

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// #283: StartLocal(ctx, dir) (addr, token string, stop func(), err error)
// starts the server on loopback over the dir Source at dir, loaded through
// the same content() path as goldie server, with a fresh random token. It is
// what goldie server -dev and the embedded server of the client commands
// (#284) run.
//
//   - addr is the base URL a client talks to: http://127.0.0.1:<port>, the
//     value goldie server -dev prints as GOLDIE_ADDR.
//   - token is 32 bytes from crypto/rand in lower-case hex: 64 characters.
//   - stop shuts the listeners and the poll loop down and returns once they
//     have stopped. It is safe to call more than once, and concurrently.
//   - Cancelling ctx stops the same things.
//   - StartLocal reads no GOLDIE_ variable: the address, the token and the
//     content come from its arguments only.
//   - Content that does not load is an error, startLocalErrPrefix followed by
//     the loader's reason, which names a file relative to dir and never dir
//     itself; addr and token are empty and nothing keeps running.
//
// StartLocal cannot be stubbed in a test file (the implementation would
// collide with the stub), so this package's tests do not compile until it
// lands, as with #257.

// startLocalErrPrefix starts every error for content that does not load.
const startLocalErrPrefix = "content does not load: "

var (
	localAddrRE  = regexp.MustCompile(`^http://127\.0\.0\.1:[1-9][0-9]{0,4}$`)
	localTokenRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// startLocal runs StartLocal on dir under a context that ends with the test,
// fails the test on error, and stops the server when the test ends.
func startLocal(t *testing.T, dir string) (addr, token string, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addr, token, stop, err := StartLocal(ctx, dir)
	if err != nil {
		t.Fatalf("StartLocal(%q): %v", dir, err)
	}
	if stop == nil {
		t.Fatal("StartLocal returned a nil stop without an error")
	}
	t.Cleanup(stop)
	return addr, token, stop
}

// localStatus is the status of an authenticated (token != "") or anonymous
// GET of path on the server at addr.
func localStatus(t *testing.T, addr, path, token string) int {
	t.Helper()
	var auth func(*http.Request)
	if token != "" {
		auth = bearer(token)
	}
	resp, _ := rawDo(t, http.MethodGet, addr+path, auth)
	return resp.StatusCode
}

// dialable reports whether something accepts TCP connections at hostport.
func dialable(hostport string) bool {
	c, err := net.DialTimeout("tcp", hostport, time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// hostPort is the host:port of a base URL.
func hostPort(t *testing.T, addr string) string {
	t.Helper()
	u, err := url.Parse(addr)
	if err != nil {
		t.Fatalf("addr %q is not a URL: %v", addr, err)
	}
	return u.Host
}

// nonLoopbackIPs are this host's addresses other than loopback and link-local.
func nonLoopbackIPs(t *testing.T) []net.IP {
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

// settledGoroutines closes the client's idle connections and returns the
// goroutine count once it stops falling.
func settledGoroutines() int {
	http.DefaultClient.CloseIdleConnections()
	n := runtime.NumGoroutine()
	for range 50 {
		time.Sleep(10 * time.Millisecond)
		m := runtime.NumGoroutine()
		if m >= n {
			return m
		}
		n = m
	}
	return n
}

// waitGoroutines waits until the goroutine count is back to base: whatever
// StartLocal started has ended.
func waitGoroutines(t *testing.T, base int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		http.DefaultClient.CloseIdleConnections()
		got := runtime.NumGoroutine()
		if got <= base {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			t.Fatalf("goroutines = %d, want at most %d: StartLocal leaked\n%s", got, base, buf[:n])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// StartLocal serves the content at dir on a loopback base URL, with a 64-hex
// token that the API accepts; no token or a wrong one is a 401.
func TestStartLocalServesTheContentOnLoopbackWithItsToken(t *testing.T) {
	addr, token, _ := startLocal(t, testContent)
	if !localAddrRE.MatchString(addr) {
		t.Fatalf("addr = %q, want http://127.0.0.1:<port>", addr)
	}
	if !localTokenRE.MatchString(token) {
		t.Fatalf("token is not 64 lower-case hex characters (32 random bytes): len %d", len(token))
	}
	if got := localStatus(t, addr, lookupPath, token); got != http.StatusOK {
		t.Errorf("GET %s with the token = %d, want 200", lookupPath, got)
	}
	for name, tok := range map[string]string{"no token": "", "wrong token": strings.Repeat("0", 64), "test token": testToken} {
		if got := localStatus(t, addr, lookupPath, tok); got != http.StatusUnauthorized {
			t.Errorf("GET %s with %s = %d, want 401", lookupPath, name, got)
		}
	}
}

// Every StartLocal makes its own token and its own port; one server's token
// opens nothing on another.
func TestStartLocalTwiceGivesDifferentTokensAndAddresses(t *testing.T) {
	addr1, token1, _ := startLocal(t, testContent)
	addr2, token2, _ := startLocal(t, testContent)
	if token1 == token2 {
		t.Fatal("two StartLocal calls returned the same token")
	}
	if addr1 == addr2 {
		t.Fatalf("two StartLocal calls returned the same address %q", addr1)
	}
	if got := localStatus(t, addr2, lookupPath, token1); got != http.StatusUnauthorized {
		t.Errorf("the first server's token on the second = %d, want 401", got)
	}
	if got := localStatus(t, addr1, lookupPath, token2); got != http.StatusUnauthorized {
		t.Errorf("the second server's token on the first = %d, want 401", got)
	}
}

// StartLocal reads none of the server's variables: a shell that ran the
// export line, or one set up for a real server, with a public address, a
// broken or conflicting token and missing content, changes nothing.
func TestStartLocalIgnoresTheServerEnvironment(t *testing.T) {
	envToken := "env-token-0123456789abcdef0123456789abcdef"
	t.Setenv(platform.EnvAddr, "0.0.0.0:0")
	t.Setenv(platform.EnvAdminAddr, "0.0.0.0:0")
	t.Setenv(platform.EnvAPIToken, envToken)
	t.Setenv(platform.EnvAPITokens, "broken line without a role")
	t.Setenv(platform.EnvAPIToken+"_FILE", filepath.Join(t.TempDir(), "missing"))
	t.Setenv(platform.EnvContentDir, filepath.Join(t.TempDir(), "missing"))
	t.Setenv("GOLDIE_TOKEN", strings.Repeat("ab", 32))

	addr, token, _ := startLocal(t, testContent)
	if !localAddrRE.MatchString(addr) {
		t.Fatalf("addr = %q, want http://127.0.0.1:<port> whatever %s says", addr, platform.EnvAddr)
	}
	if got := localStatus(t, addr, lookupPath, token); got != http.StatusOK {
		t.Errorf("GET with StartLocal's token = %d, want 200", got)
	}
	for _, tok := range []string{envToken, strings.Repeat("ab", 32)} {
		if got := localStatus(t, addr, lookupPath, tok); got != http.StatusUnauthorized {
			t.Errorf("GET with a token from the environment = %d, want 401", got)
		}
	}
}

// The listener is bound to loopback only: none of the host's other addresses
// accepts a connection on its port.
func TestStartLocalIsNotReachableOnOtherAddresses(t *testing.T) {
	t.Setenv(platform.EnvAddr, "0.0.0.0:0")
	addr, _, _ := startLocal(t, testContent)
	_, port, err := net.SplitHostPort(hostPort(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	ips := nonLoopbackIPs(t)
	if len(ips) == 0 {
		t.Log("this host has no non-loopback address; the URL check above is the whole test")
	}
	for _, ip := range ips {
		if hp := net.JoinHostPort(ip.String(), port); dialable(hp) {
			t.Errorf("StartLocal's port answers on %s: it must listen on 127.0.0.1 only", ip)
		}
	}
}

// stop returns once the listener is closed and the poll loop has ended, with
// no goroutine left behind, and a second call is a no-op.
func TestStartLocalStopShutsDownAndIsSafeTwice(t *testing.T) {
	waitLoops(t, 0)
	base := settledGoroutines()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, token, stop, err := StartLocal(ctx, testContent)
	if err != nil {
		t.Fatalf("StartLocal: %v", err)
	}
	if got := localStatus(t, addr, lookupPath, token); got != http.StatusOK {
		t.Fatalf("GET before stop = %d, want 200", got)
	}
	if got := pollLoops(); got != 1 {
		t.Fatalf("poll loops while running = %d, want 1", got)
	}
	stopReturns(t, stop)
	if dialable(hostPort(t, addr)) {
		t.Error("the listener still accepts connections after stop returned")
	}
	if got := pollLoops(); got != 0 {
		t.Errorf("poll loops after stop returned = %d, want 0", got)
	}
	stopReturns(t, stop)
	waitGoroutines(t, base)
}

// stopReturns calls stop and fails if it does not return.
func stopReturns(t *testing.T, stop func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); stop() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return")
	}
}

// Concurrent stops and a cancel racing them are safe under -race and leave
// nothing running.
func TestStartLocalConcurrentStopsAndCancel(t *testing.T) {
	waitLoops(t, 0)
	base := settledGoroutines()
	ctx, cancel := context.WithCancel(context.Background())
	addr, _, stop, err := StartLocal(ctx, testContent)
	if err != nil {
		t.Fatalf("StartLocal: %v", err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(stop)
	}
	wg.Go(cancel)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent stops did not return")
	}
	if dialable(hostPort(t, addr)) {
		t.Error("the listener still accepts connections after stop returned")
	}
	waitLoops(t, 0)
	waitGoroutines(t, base)
}

// Cancelling ctx stops the server without stop: the listener closes and the
// poll loop ends. stop afterwards is still safe.
func TestStartLocalStopsWhenTheContextEnds(t *testing.T) {
	waitLoops(t, 0)
	base := settledGoroutines()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, token, stop, err := StartLocal(ctx, testContent)
	if err != nil {
		t.Fatalf("StartLocal: %v", err)
	}
	if got := localStatus(t, addr, lookupPath, token); got != http.StatusOK {
		t.Fatalf("GET before cancel = %d, want 200", got)
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for dialable(hostPort(t, addr)) {
		if time.Now().After(deadline) {
			t.Fatal("the listener still accepts connections 5s after ctx ended")
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitLoops(t, 0)
	waitGoroutines(t, base)
	stopReturns(t, stop)
}

// A context that has already ended neither hangs StartLocal nor leaves
// anything running, whether StartLocal refuses it or starts and stops.
func TestStartLocalWithAnEndedContextLeavesNothingRunning(t *testing.T) {
	waitLoops(t, 0)
	base := settledGoroutines()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	type result struct {
		addr string
		stop func()
		err  error
	}
	res := make(chan result, 1)
	go func() {
		addr, _, stop, err := StartLocal(ctx, testContent)
		res <- result{addr, stop, err}
	}()
	var r result
	select {
	case r = <-res:
	case <-time.After(5 * time.Second):
		t.Fatal("StartLocal hung on an ended context")
	}
	if r.err == nil {
		if r.stop == nil {
			t.Fatal("StartLocal returned a nil stop without an error")
		}
		stopReturns(t, r.stop)
		if r.addr != "" && dialable(hostPort(t, r.addr)) {
			t.Error("the listener of an ended context still accepts connections after stop")
		}
	}
	waitLoops(t, 0)
	waitGoroutines(t, base)
}

// Content that does not load is a fixed error that names the reason and,
// for a bad file, the file relative to dir, never dir itself. Nothing is
// returned and nothing keeps running.
func TestStartLocalUnloadableContentFailsWithoutAPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "s3cret-no-such-dir")
	file := filepath.Join(t.TempDir(), "s3cret-a-file")
	if err := os.WriteFile(file, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "s3cret-bad-content")
	if err := os.MkdirAll(filepath.Join(bad, "global", "articles"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "global", "articles", "broken.md"), []byte("no frontmatter here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, dir, want string }{
		{"missing directory", missing, startLocalErrPrefix + "content directory is not readable"},
		{"a file, not a directory", file, startLocalErrPrefix + "content directory is not readable"},
		{"an article without frontmatter", bad, startLocalErrPrefix + "global/articles/broken.md: missing frontmatter (first line must be ---)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			waitLoops(t, 0)
			base := settledGoroutines()
			addr, token, stop, err := StartLocal(context.Background(), tc.dir)
			if err == nil {
				if stop != nil {
					stop()
				}
				t.Fatalf("StartLocal(%s) succeeded, want an error", tc.name)
			}
			if err.Error() != tc.want {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), tc.dir) || strings.Contains(err.Error(), "s3cret") {
				t.Errorf("error echoes the path: %q", err)
			}
			if addr != "" || token != "" {
				t.Errorf("StartLocal returned addr %q and a token of %d bytes with an error, want both empty", addr, len(token))
			}
			waitLoops(t, 0)
			waitGoroutines(t, base)
		})
	}
}
