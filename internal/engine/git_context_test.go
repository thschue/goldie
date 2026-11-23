package engine_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"

	"github.com/thschue/goldie/internal/engine"
)

// stallServer serves smart HTTPS. With stallOnList it never answers the ref
// listing; otherwise it advertises one commit and never answers the fetch
// that follows, so a Load hangs inside the clone. reached receives one value
// per stalled request; requests counts every request.
func stallServer(t *testing.T, stallOnList bool) (srv *httptest.Server, reached chan struct{}, requests *atomic.Int64) {
	t.Helper()
	head := plumbing.NewHash("0123456789abcdef0123456789abcdef01234567")
	reached = make(chan struct{}, 8)
	requests = new(atomic.Int64)
	release := make(chan struct{})
	stall := func(r *http.Request) {
		reached <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}
	srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/info/refs"):
			if stallOnList {
				stall(r)
				return
			}
			ar := packp.NewAdvRefs()
			ar.Prefix = [][]byte{[]byte("# service=git-upload-pack"), pktline.Flush}
			ar.Head = &head
			ar.References["refs/heads/main"] = head
			for _, c := range []capability.Capability{capability.Shallow, capability.OFSDelta, capability.NoProgress} {
				if err := ar.Capabilities.Add(c); err != nil {
					t.Error(err)
				}
			}
			if err := ar.Capabilities.Add(capability.SymRef, "HEAD:refs/heads/main"); err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
			if err := ar.Encode(w); err != nil {
				t.Error(err)
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git-upload-pack"):
			_, _ = io.Copy(io.Discard, r.Body)
			stall(r)
		default:
			http.NotFound(w, r)
		}
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv, reached, requests
}

// loadAsync runs src.Load(ctx) and returns its result on a channel.
func loadAsync(ctx context.Context, src *engine.GitSource) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := src.Load(ctx)
		done <- err
	}()
	return done
}

// TestGitSourceCancelStopsAnInFlightFetch cancels the context of a Load that
// is stuck on the server, once while listing the refs and once inside the
// clone, with the two-minute bound left as it is: only the cancel can end the
// Load, and it does at once, with a fixed error, caching nothing and leaving
// the load lock free.
func TestGitSourceCancelStopsAnInFlightFetch(t *testing.T) {
	const token = "cancel-hunter2-token"
	for _, tc := range []struct {
		name        string
		stallOnList bool
	}{
		{"while listing refs", true},
		{"during the clone", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, reached, _ := stallServer(t, tc.stallOnList)
			src := newGit(t, engine.GitOptions{URL: srv.URL + "/org/repo.git", Token: token, CABundle: string(certPEM(srv))})
			if got := engine.GitLoadTimeout(src); got != 2*time.Minute {
				t.Fatalf("fixture: load timeout = %s, want the default", got)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := loadAsync(ctx, src)
			select {
			case <-reached:
			case err := <-done:
				t.Fatalf("Load ended before the server stalled it: %v", err)
			case <-time.After(30 * time.Second):
				t.Fatal("fixture: the Load never reached the stalling request")
			}
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("a cancelled Load succeeded")
				}
				assertFixedGitError(t, err)
				assertCancelledGitError(t, err)
				assertNoSecret(t, err.Error(), token, srv.URL, srv.Listener.Addr().String())
			case <-time.After(10 * time.Second):
				t.Fatal("cancelling the context did not stop the fetch in flight")
			}
			if src.Revision() != "" {
				t.Fatal("a cancelled Load set a revision")
			}

			// The load lock is free: the next Load starts and, with its own
			// context already cancelled, ends at once.
			next, stop := context.WithCancel(context.Background())
			stop()
			select {
			case err := <-loadAsync(next, src):
				if err == nil {
					t.Fatal("a Load with a cancelled context succeeded")
				}
				assertCancelledGitError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("the Load after a cancel never ran: the load lock was not released")
			}
		})
	}
}

// TestGitSourceCancelStopsAnSSHHandshake cancels a Load stuck in the SSH
// handshake against a server that accepts the connection and never speaks.
func TestGitSourceCancelStopsAnSSHHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		for {
			select {
			case c := <-accepted:
				_ = c.Close()
			default:
				return
			}
		}
	})
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	kh := "[" + host + "]:" + port + " " + authorizedKey(newSSHKey(t).pub)
	src := newGit(t, engine.GitOptions{URL: "ssh://git@" + ln.Addr().String() + "/org/repo.git", SSHKey: newSSHKey(t).pem, KnownHosts: kh})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := loadAsync(ctx, src)
	var conn net.Conn
	select {
	case conn = <-accepted:
		accepted <- conn // closed by the cleanup
	case err := <-done:
		t.Fatalf("Load ended before the handshake: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("fixture: the Load never connected")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled Load succeeded")
		}
		assertFixedGitError(t, err)
		assertCancelledGitError(t, err)
		assertNoSecret(t, err.Error(), ln.Addr().String())
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling the context did not stop the SSH handshake")
	}
}

// TestGitSourceLoadWithCancelledContextFetchesNothing: a Load whose context is
// already over sends no request and loads nothing, over HTTPS and file://.
func TestGitSourceLoadWithCancelledContextFetchesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srv, _, requests := stallServer(t, true)
	src := newGit(t, engine.GitOptions{URL: srv.URL + "/org/repo.git", CABundle: string(certPEM(srv))})
	select {
	case err := <-loadAsync(ctx, src):
		if err == nil {
			t.Fatal("a Load with a cancelled context succeeded")
		}
		assertFixedGitError(t, err)
		assertCancelledGitError(t, err)
		assertNoSecret(t, err.Error(), srv.URL, srv.Listener.Addr().String())
	case <-time.After(10 * time.Second):
		t.Fatal("a Load with a cancelled context went on to fetch")
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("a Load with a cancelled context sent %d requests", n)
	}

	repo := newGitRepo(t)
	repo.commit("main", map[string]string{"global/articles/a.md": article("Ay")})
	file := newGit(t, engine.GitOptions{URL: repo.url})
	snap, err := file.Load(ctx)
	if err == nil || snap != nil {
		t.Fatalf("file:// Load with a cancelled context = %v, %v; want an error and no Snapshot", snap, err)
	}
	assertFixedGitError(t, err)
	assertCancelledGitError(t, err)
	assertNoSecret(t, err.Error(), repo.url, repo.dir)
	if file.Revision() != "" {
		t.Fatal("a Load with a cancelled context set a revision")
	}
	// The same Source loads once given a live context.
	if _, err := file.Load(context.Background()); err != nil {
		t.Fatalf("Load after a cancelled one: %v", err)
	}
}

// TestGitSourceLoadTimeoutBoundsInsideTheContext gives Load a context with a
// far deadline: the Source's own bound still applies inside it.
func TestGitSourceLoadTimeoutBoundsInsideTheContext(t *testing.T) {
	srv, _, _ := stallServer(t, true)
	src := newGit(t, engine.GitOptions{URL: srv.URL + "/org/repo.git", Token: "bound-hunter2", CABundle: string(certPEM(srv))})
	engine.SetGitLoadTimeout(src, 300*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	select {
	case err := <-loadAsync(ctx, src):
		if want := "git source: timed out after 300ms"; err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %q", err, want)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a 300ms bound did not end the Load inside a 10m context")
	}
}

// TestSyncerRunOverGitSourceStopsOnCancel is the shutdown case: Run is polling
// a git Source whose server has stalled, and cancelling Run's context ends Run
// at once rather than after the two-minute bound.
func TestSyncerRunOverGitSourceStopsOnCancel(t *testing.T) {
	const token = "shutdown-hunter2-token"
	srv, reached, _ := stallServer(t, false)
	src := newGit(t, engine.GitOptions{URL: srv.URL + "/org/repo.git", Token: token, CABundle: string(certPEM(srv))})
	syncer := engine.NewSyncer(src)
	var mu sync.Mutex
	var reports []engine.SyncStatus
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		syncer.Run(ctx, time.Hour, func(st engine.SyncStatus) {
			mu.Lock()
			reports = append(reports, st)
			mu.Unlock()
		})
		close(done)
	}()
	select {
	case <-reached:
	case <-time.After(30 * time.Second):
		t.Fatal("fixture: the first poll never reached the server")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel while a clone was in flight")
	}
	if syncer.Current() != nil {
		t.Fatal("a cancelled first clone served a Snapshot")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, st := range reports {
		assertNoSecret(t, st.LastError, token, srv.URL, srv.Listener.Addr().String())
		if st.LastError != "" && st.LastError != cancelledGitError {
			t.Fatalf("reported LastError = %q, want empty or %q", st.LastError, cancelledGitError)
		}
	}
	assertNoSecret(t, syncer.Status().LastError, token, srv.URL, srv.Listener.Addr().String())
	if got := syncer.Status().LastError; got != "" && got != cancelledGitError {
		t.Fatalf("Status().LastError = %q, want empty or %q", got, cancelledGitError)
	}
}

// cancelledGitError is the whole text of the error a git Source returns when
// its Load's context ended, however the Load noticed.
const cancelledGitError = "git source: the load was cancelled"

// assertCancelledGitError checks the exact text, as the timeout tests do.
func assertCancelledGitError(t *testing.T, err error) {
	t.Helper()
	if err == nil || err.Error() != cancelledGitError {
		t.Fatalf("err = %v, want exactly %q", err, cancelledGitError)
	}
}

// TestGitSourcePollOfACancelledContextRecordsTheFixedError: a Syncer polling a
// git Source with a context that is already over fails that poll with the
// fixed text, which is what Status shows, and serves no Snapshot.
func TestGitSourcePollOfACancelledContextRecordsTheFixedError(t *testing.T) {
	srv, _, requests := stallServer(t, true)
	src := newGit(t, engine.GitOptions{URL: srv.URL + "/org/repo.git", Token: "poll-hunter2", CABundle: string(certPEM(srv))})
	syncer := engine.NewSyncer(src)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertCancelledGitError(t, syncer.Poll(ctx))
	if got := syncer.Status().LastError; got != cancelledGitError {
		t.Fatalf("Status().LastError = %q, want %q", got, cancelledGitError)
	}
	if syncer.Current() != nil {
		t.Fatal("a cancelled poll served a Snapshot")
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("a cancelled poll sent %d requests", n)
	}
}

// TestGitSourceCancelledErrorIsTheSameWhereverItIsBuilt is the drift guard. A
// Load cancelled before it starts and one cancelled while it fetches reach the
// error by different paths; both must return the same text and the same
// error type, wrap nothing, and show no URL, address or path.
func TestGitSourceCancelledErrorIsTheSameWhereverItIsBuilt(t *testing.T) {
	const token = "drift-hunter2-token"
	srv, reached, _ := stallServer(t, true)
	src := newGit(t, engine.GitOptions{URL: srv.URL + "/org/repo.git", Token: token, CABundle: string(certPEM(srv))})
	repo := newGitRepo(t)
	repo.commit("main", map[string]string{"global/articles/a.md": article("Ay")})
	file := newGit(t, engine.GitOptions{URL: repo.url})

	over, stop := context.WithCancel(context.Background())
	stop()
	_, early := src.Load(over)
	_, earlyFile := file.Load(over)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := loadAsync(ctx, src)
	select {
	case <-reached:
	case err := <-done:
		t.Fatalf("Load ended before the server stalled it: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("fixture: the Load never reached the stalling request")
	}
	cancel()
	var mid error
	select {
	case mid = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling the context did not stop the fetch in flight")
	}

	for name, err := range map[string]error{"early return": early, "early return, file://": earlyFile, "during the fetch": mid} {
		if err == nil {
			t.Fatalf("%s: a cancelled Load succeeded", name)
		}
		assertCancelledGitError(t, err)
		assertNoSecret(t, err.Error(), token, srv.URL, srv.Listener.Addr().String(), repo.url, repo.dir)
		if u := errors.Unwrap(err); u != nil {
			t.Fatalf("%s: the error wraps %v; it must carry only the fixed text", name, u)
		}
		if errors.Is(err, context.Canceled) {
			t.Fatalf("%s: the error wraps context.Canceled; it must carry only the fixed text", name)
		}
	}
	if early.Error() != mid.Error() || fmt.Sprintf("%T", early) != fmt.Sprintf("%T", mid) {
		t.Fatalf("the two paths drifted: %T %q vs %T %q", early, early, mid, mid)
	}
	if early.Error() != earlyFile.Error() || fmt.Sprintf("%T", early) != fmt.Sprintf("%T", earlyFile) {
		t.Fatalf("the same path drifted between Sources: %T %q vs %T %q", early, early, earlyFile, earlyFile)
	}
}

// otherCAPEM is a self-signed CA certificate that signed none of the test
// servers.
func otherCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "other test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestGitSourceAcceptsCABundleWithHTTPS(t *testing.T) {
	ca := otherCAPEM(t)
	for _, opts := range []engine.GitOptions{
		{URL: "https://git.example.invalid/org/repo.git", CABundle: ca},
		{URL: "https://git.example.invalid:8443/org/repo.git", Token: "hunter2", Ref: "main", CABundle: ca},
		{URL: "HTTPS://git.example.invalid/org/repo.git", CABundle: ca},
		// Two certificates in one bundle.
		{URL: "https://git.example.invalid/org/repo.git", CABundle: ca + otherCAPEM(t)},
	} {
		if _, err := engine.NewGitSource(opts); err != nil {
			t.Errorf("%s: %v", opts.URL, err)
		}
	}
}

// TestGitSourceCABundleNeedsHTTPS gives a valid CA bundle with every other
// scheme, each otherwise valid. Each is refused with one fixed error, built
// twice with a different host, so it echoes neither the host nor the bundle.
func TestGitSourceCABundleNeedsHTTPS(t *testing.T) {
	const want = "git source: CABundle needs an HTTPS URL"
	key := newSSHKey(t)
	hostKey := newSSHKey(t)
	for _, tc := range []struct {
		name string
		opts func(host string) engine.GitOptions
	}{
		{"ssh", func(h string) engine.GitOptions {
			return engine.GitOptions{URL: "ssh://git@" + h + "/r.git", SSHKey: key.pem, KnownHosts: h + " " + authorizedKey(hostKey.pub)}
		}},
		{"ssh, upper case", func(h string) engine.GitOptions {
			return engine.GitOptions{URL: "SSH://git@" + h + "/r.git", SSHKey: key.pem, KnownHosts: h + " " + authorizedKey(hostKey.pub)}
		}},
		{"file", func(h string) engine.GitOptions { return engine.GitOptions{URL: "file:///srv/" + h + "/r.git"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts("git.example.invalid")
			if _, err := engine.NewGitSource(opts); err != nil {
				t.Fatalf("fixture: the options without a CA bundle are refused: %v", err)
			}
			ca := otherCAPEM(t)
			opts.CABundle = ca
			_, err := engine.NewGitSource(opts)
			if err == nil {
				t.Fatal("accepted")
			}
			if err.Error() != want {
				t.Fatalf("err = %q, want %q", err, want)
			}
			assertNoSecret(t, err.Error(), "git.example.invalid", "/srv/", ca, "CERTIFICATE")
			other := tc.opts("other.example.invalid")
			other.CABundle = otherCAPEM(t)
			if _, err2 := engine.NewGitSource(other); err2 == nil || err2.Error() != err.Error() {
				t.Fatalf("error is not a fixed message: %q, then %v", err, err2)
			}
		})
	}
	// Plain http stays refused with a CA bundle.
	if _, err := engine.NewGitSource(engine.GitOptions{URL: "http://git.example.invalid/r.git", CABundle: otherCAPEM(t)}); err == nil {
		t.Fatal("plain http accepted with a CA bundle")
	}
}

// TestGitSourceCABundleMustBePEMCertificates gives bundles that hold no
// certificate. Each is refused with one fixed error that never shows the
// content: built twice with different content, the message is the same.
func TestGitSourceCABundleMustBePEMCertificates(t *testing.T) {
	const want = "git source: CABundle is not a PEM certificate bundle"
	key := newSSHKey(t)
	for _, tc := range []struct {
		name   string
		bundle func(secret string) string
	}{
		{"not PEM", func(s string) string { return "trust me " + s }},
		{"a private key, not a certificate", func(s string) string { return key.pem + "# " + s + "\n" }},
		{"a certificate block that is not DER", func(s string) string {
			return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not der " + s)}))
		}},
		{"a cut-off certificate", func(s string) string {
			return "-----BEGIN CERTIFICATE-----\n" + s + "\n"
		}},
		{"a certificate request", func(s string) string {
			return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: []byte(s)}))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := tc.bundle("hunter2-pem-secret")
			_, err := engine.NewGitSource(engine.GitOptions{URL: "https://git.example.invalid/r.git", CABundle: bundle})
			if err == nil {
				t.Fatal("accepted")
			}
			if err.Error() != want {
				t.Fatalf("err = %q, want %q", err, want)
			}
			assertNoSecret(t, err.Error(), "hunter2-pem-secret", "git.example.invalid", "-----", "PRIVATE")
			_, other := engine.NewGitSource(engine.GitOptions{URL: "https://other.example.invalid/r.git", CABundle: tc.bundle("swordfish-pem-secret")})
			if other == nil || other.Error() != err.Error() {
				t.Fatalf("error is not a fixed message: %q, then %v", err, other)
			}
		})
	}
	// GitOptions printed every way never shows the bundle's content either.
	opts := engine.GitOptions{URL: "https://git.example.invalid/r.git", CABundle: "hunter2-pem-secret"}
	for _, s := range []string{opts.String(), opts.GoString()} {
		assertNoSecret(t, s, "hunter2-pem-secret")
	}
}

// TestGitSourceCABundleOfAnotherCADoesNotTrustTheServer: a CA bundle adds the
// CAs it holds and nothing else. A server its CA did not sign is refused
// before any request, so the token never leaves.
func TestGitSourceCABundleOfAnotherCADoesNotTrustTheServer(t *testing.T) {
	const token = "other-ca-hunter2-token"
	var requests atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()

	src := newGit(t, engine.GitOptions{URL: srv.URL + "/org/repo.git", Token: token, CABundle: otherCAPEM(t)})
	_, err := src.Load(context.Background())
	if want := "git source: the server's TLS certificate is not trusted"; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("%d requests reached a server the CA bundle does not trust", n)
	}

	// The server's own CA in the bundle does trust it.
	src = newGit(t, engine.GitOptions{URL: srv.URL + "/org/repo.git", Token: token, CABundle: otherCAPEM(t) + string(certPEM(srv))})
	if _, err := src.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want repository not found once the CA is trusted", err)
	}
}
