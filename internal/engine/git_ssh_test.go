package engine_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/thschue/goldie/internal/engine"
)

// sshKey is a key pair: the private half as unencrypted OpenSSH PEM, the way
// an operator passes GitOptions.SSHKey.
type sshKey struct {
	pub    ssh.PublicKey
	pem    string
	signer ssh.Signer
	priv   any
}

func newSSHKey(t testing.TB) sshKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return sshKeyFrom(t, priv)
}

func sshKeyFrom(t testing.TB, priv any) sshKey {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	return sshKey{pub: signer.PublicKey(), pem: string(pem.EncodeToMemory(block)), signer: signer, priv: priv}
}

// material is everything about k that must never reach an error or a log:
// the private key, the public key blob and its fingerprints.
func (k sshKey) material() []string {
	sha := ssh.FingerprintSHA256(k.pub)
	return []string{k.pem, authorizedKey(k.pub), sha, strings.TrimPrefix(sha, "SHA256:"), ssh.FingerprintLegacyMD5(k.pub)}
}

func authorizedKey(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

// sshLogin is a successful authentication: the client proved it holds key.
type sshLogin struct {
	user string
	key  []byte
}

// sshServer is a loopback SSH server with fixed host keys. It accepts a login
// only with the authorized key, records every client key offered and every
// login, and serves no repository: each channel is refused.
type sshServer struct {
	addr     string
	hostKeys []ssh.Signer

	mu        sync.Mutex
	cond      *sync.Cond
	closed    bool
	conns     []net.Conn
	remotes   []string            // remote address of every accepted connection
	sentinels map[string]struct{} // connections the test itself made
	offered   []ssh.PublicKey
	logins    []sshLogin
}

func newSSHServer(t *testing.T, authorized ssh.PublicKey, hostKeys ...ssh.Signer) *sshServer {
	t.Helper()
	if len(hostKeys) == 0 {
		hostKeys = []ssh.Signer{newSSHKey(t).signer}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sshServer{addr: ln.Addr().String(), hostKeys: hostKeys, sentinels: map[string]struct{}{}}
	s.cond = sync.NewCond(&s.mu)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		s.mu.Lock()
		s.offered = append(s.offered, k)
		s.mu.Unlock()
		if authorized == nil || !bytes.Equal(k.Marshal(), authorized.Marshal()) {
			return nil, errors.New("key not authorized")
		}
		return &ssh.Permissions{Extensions: map[string]string{"key": string(k.Marshal())}}, nil
	}}
	for _, k := range hostKeys {
		cfg.AddHostKey(k)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				_ = c.Close()
				return
			}
			s.conns = append(s.conns, c)
			s.remotes = append(s.remotes, c.RemoteAddr().String())
			s.cond.Broadcast()
			s.mu.Unlock()
			wg.Go(func() { s.serve(c, cfg) })
		}
	})
	t.Cleanup(func() {
		_ = ln.Close()
		s.mu.Lock()
		s.closed = true
		for _, c := range s.conns {
			_ = c.Close()
		}
		s.mu.Unlock()
		wg.Wait()
	})
	return s
}

func (s *sshServer) serve(c net.Conn, cfg *ssh.ServerConfig) {
	defer func() { _ = c.Close() }()
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.logins = append(s.logins, sshLogin{user: conn.User(), key: []byte(conn.Permissions.Extensions["key"])})
	s.mu.Unlock()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		_ = nc.Reject(ssh.Prohibited, "test server serves no repository")
	}
}

// connections counts the TCP connections the server accepted, apart from the
// ones it made itself. It dials a sentinel and waits for the server to accept
// it: connections are accepted in order, so any earlier one is counted.
func (s *sshServer) connections(t *testing.T) int {
	t.Helper()
	c, err := net.Dial("tcp", s.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	local := c.LocalAddr().String()
	s.mu.Lock()
	defer s.mu.Unlock()
	for !slices.Contains(s.remotes, local) {
		s.cond.Wait()
	}
	s.sentinels[local] = struct{}{}
	n := 0
	for _, r := range s.remotes {
		if _, ok := s.sentinels[r]; !ok {
			n++
		}
	}
	return n
}

// seen returns how many client keys were offered and how many logins
// succeeded so far.
func (s *sshServer) seen() (offered, logins int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.offered), len(s.logins)
}

func (s *sshServer) offeredSince(n int) []ssh.PublicKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.offered[n:])
}

func (s *sshServer) loginsSince(n int) []sshLogin {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.logins[n:])
}

// pattern is the known_hosts host pattern for the server: [127.0.0.1]:port.
func (s *sshServer) pattern() string {
	host, port, _ := net.SplitHostPort(s.addr)
	return "[" + host + "]:" + port
}

func (s *sshServer) port() string {
	_, port, _ := net.SplitHostPort(s.addr)
	return port
}

func (s *sshServer) url(user string) string {
	return "ssh://" + user + "@" + s.addr + "/org/repo.git"
}

// line is a known_hosts line trusting the server's first host key.
func (s *sshServer) line() string {
	return s.pattern() + " " + authorizedKey(s.hostKeys[0].PublicKey())
}

// hostMaterial is everything about the server that must never reach an
// error or a log: its address and every host key's blob and fingerprints.
func (s *sshServer) hostMaterial() []string {
	out := []string{s.addr, s.pattern()}
	for _, k := range s.hostKeys {
		sha := ssh.FingerprintSHA256(k.PublicKey())
		out = append(out, authorizedKey(k.PublicKey()), sha, strings.TrimPrefix(sha, "SHA256:"), ssh.FingerprintLegacyMD5(k.PublicKey()))
	}
	return out
}

// hashedPattern hashes a host the way ssh-keygen -H does:
// |1|base64(salt)|base64(HMAC-SHA1(salt, host)).
func hashedPattern(host string) string {
	salt := []byte("goldie-test-salt-20b")
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(host))
	return "|1|" + base64.StdEncoding.EncodeToString(salt) + "|" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// knownHostsMaterial is the known_hosts content and each long field of it, so
// a leak of one line or one key blob is caught too.
func knownHostsMaterial(content string) []string {
	out := []string{content}
	for _, f := range strings.Fields(content) {
		if len(f) >= 16 {
			out = append(out, f)
		}
	}
	return out
}

// assertNoKeyMaterial is assertNoSecret, plus every line of a multi-line
// secret, so a PEM key leaked line by line is caught.
func assertNoKeyMaterial(t *testing.T, s string, material ...string) {
	t.Helper()
	assertNoSecret(t, s, material...)
	for _, m := range material {
		for line := range strings.SplitSeq(m, "\n") {
			line = strings.TrimSpace(line)
			if len(line) >= 16 && !strings.HasPrefix(line, "-----") && strings.Contains(s, line) {
				t.Fatalf("output contains part of a secret %.12q...: %q", line, s)
			}
		}
	}
}

func assertFixedGitError(t *testing.T, err error) {
	t.Helper()
	if !strings.HasPrefix(err.Error(), "git source: ") {
		t.Fatalf("err = %q, want a fixed git source error", err)
	}
}

func TestGitSourceAcceptsSSH(t *testing.T) {
	key := newSSHKey(t)
	host := newSSHKey(t)
	kh := "git.example.invalid " + authorizedKey(host.pub)
	for _, opts := range []engine.GitOptions{
		{URL: "ssh://git@git.example.invalid/org/repo.git", SSHKey: key.pem, KnownHosts: kh},
		{URL: "ssh://git@git.example.invalid:2222/org/repo.git", SSHKey: key.pem, KnownHosts: "[git.example.invalid]:2222 " + authorizedKey(host.pub), Ref: "v1"},
		{URL: "ssh://git@git.example.invalid/org/repo.git", SSHKey: key.pem, KnownHosts: kh, Ref: "refs/heads/main"},
	} {
		if _, err := engine.NewGitSource(opts); err != nil {
			t.Errorf("%s: %v", opts.URL, err)
		}
	}
}

// TestGitSourceSSHNeedsKnownHostsAndAnSSHURL builds each case twice, against
// two servers with different keys, and requires the same fixed error for both,
// before any connection reaches either server.
func TestGitSourceSSHNeedsKnownHostsAndAnSSHURL(t *testing.T) {
	type env struct {
		srv *sshServer
		key sshKey
	}
	a := env{key: newSSHKey(t)}
	a.srv = newSSHServer(t, a.key.pub)
	b := env{key: newSSHKey(t)}
	b.srv = newSSHServer(t, b.key.pub)

	for _, tc := range []struct {
		name string
		opts func(e env) engine.GitOptions
	}{
		{"ssh key without known hosts", func(e env) engine.GitOptions {
			return engine.GitOptions{URL: e.srv.url("git"), SSHKey: e.key.pem}
		}},
		{"ssh key with blank known hosts", func(e env) engine.GitOptions {
			return engine.GitOptions{URL: e.srv.url("git"), SSHKey: e.key.pem, KnownHosts: " \n\t \r\n"}
		}},
		{"ssh url without key or known hosts", func(e env) engine.GitOptions {
			return engine.GitOptions{URL: e.srv.url("git")}
		}},
		{"ssh key over https", func(e env) engine.GitOptions {
			return engine.GitOptions{URL: "https://" + e.srv.addr + "/org/repo.git", SSHKey: e.key.pem, KnownHosts: e.srv.line()}
		}},
		{"ssh key over https without known hosts", func(e env) engine.GitOptions {
			return engine.GitOptions{URL: "https://" + e.srv.addr + "/org/repo.git", SSHKey: e.key.pem}
		}},
		{"ssh key over file", func(e env) engine.GitOptions {
			return engine.GitOptions{URL: "file:///srv/" + e.srv.port() + "/repo.git", SSHKey: e.key.pem, KnownHosts: e.srv.line()}
		}},
		{"password in ssh url", func(e env) engine.GitOptions {
			return engine.GitOptions{URL: "ssh://git:pw-" + e.srv.port() + "@" + e.srv.addr + "/org/repo.git", SSHKey: e.key.pem, KnownHosts: e.srv.line()}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := engine.NewGitSource(tc.opts(a))
			if err == nil {
				t.Fatal("accepted")
			}
			assertFixedGitError(t, err)
			material := append(append(a.key.material(), a.srv.hostMaterial()...), knownHostsMaterial(a.srv.line())...)
			assertNoKeyMaterial(t, err.Error(), append(material, "pw-"+a.srv.port())...)
			_, other := engine.NewGitSource(tc.opts(b))
			if other == nil || other.Error() != err.Error() {
				t.Fatalf("error is not a fixed message: %q, then %v", err, other)
			}
		})
	}
	if n := a.srv.connections(t) + b.srv.connections(t); n != 0 {
		t.Fatalf("building the Source contacted a server %d times", n)
	}
}

// TestGitSourceSSHTrustedHostKeyAuthenticatesWithTheConfiguredKey loads from
// a server whose host key KnownHosts lists, in each supported form. The load
// gets as far as a login with the configured key as the URL's user; the test
// server then refuses to serve a repository.
func TestGitSourceSSHTrustedHostKeyAuthenticatesWithTheConfiguredKey(t *testing.T) {
	client := newSSHKey(t)
	other := newSSHKey(t)
	srv := newSSHServer(t, client.pub)
	hostKey := authorizedKey(srv.hostKeys[0].PublicKey())

	for _, tc := range []struct{ name, knownHosts string }{
		{"plain host and port", srv.pattern() + " " + hostKey},
		{"hashed host and port", hashedPattern(srv.pattern()) + " " + hostKey},
		{"one of several hosts", "git.example.invalid," + srv.pattern() + " " + hostKey},
		{"another host negated", "!git.example.invalid," + srv.pattern() + " " + hostKey},
		{"key comment", srv.pattern() + " " + hostKey + " deploy@goldie"},
		{"comments, blank lines and other hosts", "# goldie\n\ngit.example.invalid " + authorizedKey(other.pub) + "\n" + srv.pattern() + " " + hostKey + "\n"},
		{"another key revoked", srv.pattern() + " " + hostKey + "\n@revoked * " + authorizedKey(other.pub)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offered, logins := srv.seen()
			_, err := newGit(t, engine.GitOptions{URL: srv.url("git"), SSHKey: client.pem, KnownHosts: tc.knownHosts}).Load(t.Context())
			if err == nil {
				t.Fatal("loaded from a server that serves no repository")
			}
			assertFixedGitError(t, err)
			assertNoKeyMaterial(t, err.Error(), append(append(client.material(), srv.hostMaterial()...), knownHostsMaterial(tc.knownHosts)...)...)
			got := srv.loginsSince(logins)
			if len(got) == 0 {
				t.Fatalf("no login with a trusted host key (err = %v)", err)
			}
			for _, l := range got {
				if l.user != "git" || !bytes.Equal(l.key, client.pub.Marshal()) {
					t.Fatalf("logged in as %q with another key, want the URL's user with the configured key", l.user)
				}
			}
			for _, k := range srv.offeredSince(offered) {
				if !bytes.Equal(k.Marshal(), client.pub.Marshal()) {
					t.Fatal("offered a key other than the configured one")
				}
			}
		})
	}
}

// TestGitSourceSSHNegotiatesTheListedHostKeyType serves ed25519, ECDSA and RSA
// host keys and lists only one of them in KnownHosts: the load must ask the
// server for that key, not fail on another type it was not told about.
func TestGitSourceSSHNegotiatesTheListedHostKeyType(t *testing.T) {
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	hostKeys := map[string]sshKey{
		"ed25519": sshKeyFrom(t, edPriv),
		"ecdsa":   sshKeyFrom(t, ecPriv),
		"rsa":     sshKeyFrom(t, rsaPriv),
	}
	client := newSSHKey(t)
	srv := newSSHServer(t, client.pub, hostKeys["ed25519"].signer, hostKeys["ecdsa"].signer, hostKeys["rsa"].signer)

	for _, name := range []string{"ed25519", "ecdsa", "rsa"} {
		t.Run(name, func(t *testing.T) {
			_, logins := srv.seen()
			kh := srv.pattern() + " " + authorizedKey(hostKeys[name].pub)
			_, err := newGit(t, engine.GitOptions{URL: srv.url("git"), SSHKey: client.pem, KnownHosts: kh}).Load(t.Context())
			if err == nil {
				t.Fatal("loaded from a server that serves no repository")
			}
			assertFixedGitError(t, err)
			if len(srv.loginsSince(logins)) == 0 {
				t.Fatalf("no login with only the %s host key listed (err = %v)", name, err)
			}
		})
	}
}

// TestGitSourceSSHUntrustedHostKeyFails points the load at a server that
// would accept the configured key, with KnownHosts that do not trust its
// host key. Each fails with the same fixed host key error, before the client
// offers its key.
func TestGitSourceSSHUntrustedHostKeyFails(t *testing.T) {
	client := newSSHKey(t)
	other := newSSHKey(t)
	srv := newSSHServer(t, client.pub)
	hostKey := authorizedKey(srv.hostKeys[0].PublicKey())
	trusted := srv.pattern() + " " + hostKey

	cases := []struct{ name, knownHosts string }{
		{"another key for the host", srv.pattern() + " " + authorizedKey(other.pub)},
		{"host not listed", "git.example.invalid " + hostKey},
		{"host listed without its port", "127.0.0.1 " + hostKey},
		{"host listed with another port", "[127.0.0.1]:1 " + hostKey},
		{"hashed pattern of another host", hashedPattern("git.example.invalid") + " " + hostKey},
		{"hashed host without its port", hashedPattern("127.0.0.1") + " " + hostKey},
		{"key revoked for every host", trusted + "\n@revoked * " + hostKey},
		{"key revoked before it is trusted", "@revoked * " + hostKey + "\n" + trusted},
		{"key revoked for the host", trusted + "\n@revoked " + srv.pattern() + " " + hostKey},
		{"host negated first", "!" + srv.pattern() + "," + srv.pattern() + " " + hostKey},
		{"host negated last", srv.pattern() + ",!" + srv.pattern() + " " + hostKey},
		{"hashed host negated", "!" + hashedPattern(srv.pattern()) + "," + srv.pattern() + " " + hostKey},
		{"wildcard for every host", "* " + hostKey},
		{"wildcard address", "[127.0.0.*]:" + srv.port() + " " + hostKey},
		{"single-character wildcard", "[127.0.0.?]:" + srv.port() + " " + hostKey},
		{"wildcard port", "[127.0.0.1]:* " + hostKey},
		{"wildcard next to a negation", "*,!git.example.invalid " + hostKey},
		{"host key listed as a certificate authority", "@cert-authority " + srv.pattern() + " " + hostKey + "\ngit.example.invalid " + authorizedKey(other.pub)},
	}
	var first string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			offered, logins := srv.seen()
			_, err := newGit(t, engine.GitOptions{URL: srv.url("git"), SSHKey: client.pem, KnownHosts: tc.knownHosts}).Load(t.Context())
			if err == nil || !strings.Contains(err.Error(), "host key") {
				t.Fatalf("err = %v, want a host key failure", err)
			}
			assertFixedGitError(t, err)
			if first == "" {
				first = err.Error()
			} else if err.Error() != first {
				t.Fatalf("host key error is not one fixed message: %q, then %q", first, err)
			}
			if o, l := srv.seen(); o != offered || l != logins {
				t.Fatal("the client offered its key to an untrusted host")
			}
			assertNoKeyMaterial(t, err.Error(), append(append(client.material(), srv.hostMaterial()...), knownHostsMaterial(tc.knownHosts)...)...)
		})
	}
}

// TestGitSourceSSHCertAuthorityFailsClosed serves a host certificate signed by
// a CA that KnownHosts lists with @cert-authority. Certificates are not
// trusted: the load fails with the host key error, before the client offers
// its key.
func TestGitSourceSSHCertAuthorityFailsClosed(t *testing.T) {
	ca := newSSHKey(t)
	host := newSSHKey(t)
	cert := &ssh.Certificate{
		Key:             host.pub,
		CertType:        ssh.HostCert,
		KeyId:           "goldie-test-host",
		ValidPrincipals: []string{"127.0.0.1"},
		ValidAfter:      0,
		ValidBefore:     ssh.CertTimeInfinity,
	}
	if err := cert.SignCert(rand.Reader, ca.signer); err != nil {
		t.Fatal(err)
	}
	certSigner, err := ssh.NewCertSigner(cert, host.signer)
	if err != nil {
		t.Fatal(err)
	}
	client := newSSHKey(t)
	other := newSSHKey(t)
	srv := newSSHServer(t, client.pub, certSigner, host.signer)

	for _, tc := range []struct{ name, knownHosts string }{
		{"authority for the host", "@cert-authority " + srv.pattern() + " " + authorizedKey(ca.pub) + "\ngit.example.invalid " + authorizedKey(other.pub)},
		{"authority for every host", "@cert-authority * " + authorizedKey(ca.pub) + "\ngit.example.invalid " + authorizedKey(other.pub)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offered, logins := srv.seen()
			_, err := newGit(t, engine.GitOptions{URL: srv.url("git"), SSHKey: client.pem, KnownHosts: tc.knownHosts}).Load(t.Context())
			if err == nil || !strings.Contains(err.Error(), "host key") {
				t.Fatalf("err = %v, want a host key failure", err)
			}
			assertFixedGitError(t, err)
			if o, l := srv.seen(); o != offered || l != logins {
				t.Fatal("the client trusted a host certificate and offered its key")
			}
			assertNoKeyMaterial(t, err.Error(), append(append(client.material(), ca.material()...), knownHostsMaterial(tc.knownHosts)...)...)
		})
	}
}

// TestGitSourceSSHUnusableKnownHostsFailClosed passes KnownHosts that trust
// no key for the server. The Source either refuses them when it is built, or
// its load fails with the host key error; it never offers the client key.
func TestGitSourceSSHUnusableKnownHostsFailClosed(t *testing.T) {
	client := newSSHKey(t)
	srv := newSSHServer(t, client.pub)
	hostKey := authorizedKey(srv.hostKeys[0].PublicKey())

	for _, tc := range []struct{ name, knownHosts string }{
		{"not known_hosts content", "this is not a known_hosts file"},
		{"comments only", "# " + srv.pattern() + " " + hostKey + "\n# nothing else\n"},
		{"revoked lines only", "@revoked " + srv.pattern() + " " + hostKey},
		{"authority lines only", "@cert-authority " + srv.pattern() + " " + hostKey},
		{"truncated key", srv.pattern() + " ssh-ed25519 AAAAC3NzaC1lZDI1NTE5"},
		{"key type with no key", srv.pattern() + " ssh-ed25519"},
		{"unknown marker", "@trusted " + srv.pattern() + " " + hostKey},
		{"bad hashed pattern", "|1|not base64|!!! " + hostKey},
		{"NUL in the host", srv.pattern() + "\x00 " + hostKey},
		{"NUL after the host", "\x00" + srv.pattern() + " " + hostKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offered, logins := srv.seen()
			material := append(append(client.material(), srv.hostMaterial()...), knownHostsMaterial(tc.knownHosts)...)
			src, err := engine.NewGitSource(engine.GitOptions{URL: srv.url("git"), SSHKey: client.pem, KnownHosts: tc.knownHosts})
			if err != nil {
				assertFixedGitError(t, err)
				assertNoKeyMaterial(t, err.Error(), material...)
				return
			}
			_, err = src.Load(t.Context())
			if err == nil || !strings.Contains(err.Error(), "host key") {
				t.Fatalf("err = %v, want a host key failure", err)
			}
			assertFixedGitError(t, err)
			if o, l := srv.seen(); o != offered || l != logins {
				t.Fatal("the client offered its key without a trusted host key")
			}
			assertNoKeyMaterial(t, err.Error(), material...)
		})
	}
}

// TestGitSourceSSHUnusableKeyFails passes an SSHKey that is not an unencrypted
// private key. The Source refuses it when it is built, or its load fails; the
// key is never echoed and the server sees no login.
func TestGitSourceSSHUnusableKeyFails(t *testing.T) {
	client := newSSHKey(t)
	srv := newSSHServer(t, client.pub)
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encBlock, err := ssh.MarshalPrivateKeyWithPassphrase(edPriv, "", []byte("hunter2-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	encrypted := string(pem.EncodeToMemory(encBlock))

	for _, tc := range []struct{ name, key string }{
		{"not a key", "hunter2-not-a-private-key-0123456789"},
		{"truncated PEM", client.pem[:len(client.pem)/2]},
		{"public key", authorizedKey(client.pub)},
		{"encrypted key", encrypted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, logins := srv.seen()
			material := append([]string{tc.key, "hunter2-passphrase"}, client.material()...)
			src, err := engine.NewGitSource(engine.GitOptions{URL: srv.url("git"), SSHKey: tc.key, KnownHosts: srv.line()})
			if err == nil {
				_, err = src.Load(t.Context())
			}
			if err == nil {
				t.Fatal("loaded with an unusable key")
			}
			assertFixedGitError(t, err)
			assertNoKeyMaterial(t, err.Error(), material...)
			if len(srv.loginsSince(logins)) != 0 {
				t.Fatal("logged in with an unusable key")
			}
		})
	}
}

// TestGitSourceSSHIgnoresAmbientSSHSetup gives the process a known_hosts file
// that trusts the server and an SSH agent holding the key the server accepts.
// The Source uses neither: only KnownHosts decides the host key, and only
// SSHKey is offered.
func TestGitSourceSSHIgnoresAmbientSSHSetup(t *testing.T) {
	agentKey := newSSHKey(t)
	client := newSSHKey(t)
	other := newSSHKey(t)
	srv := newSSHServer(t, agentKey.pub)

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	ambient := filepath.Join(home, ".ssh", "known_hosts")
	if err := os.WriteFile(ambient, []byte(srv.line()+"\n* "+authorizedKey(srv.hostKeys[0].PublicKey())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SSH_KNOWN_HOSTS", ambient)

	sockDir, err := os.MkdirTemp("", "goldie-agent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: agentKey.priv}); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(sockDir, "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() { _ = agent.ServeAgent(keyring, c); _ = c.Close() })
		}
	})
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	t.Setenv("SSH_AUTH_SOCK", sock)

	t.Run("ambient known_hosts does not trust the host", func(t *testing.T) {
		offered, logins := srv.seen()
		kh := srv.pattern() + " " + authorizedKey(other.pub)
		_, err := newGit(t, engine.GitOptions{URL: srv.url("git"), SSHKey: client.pem, KnownHosts: kh}).Load(t.Context())
		if err == nil || !strings.Contains(err.Error(), "host key") {
			t.Fatalf("err = %v, want a host key failure", err)
		}
		if o, l := srv.seen(); o != offered || l != logins {
			t.Fatal("the client offered a key to a host only the ambient known_hosts trusts")
		}
	})

	t.Run("agent key is not offered", func(t *testing.T) {
		offered, logins := srv.seen()
		_, err := newGit(t, engine.GitOptions{URL: srv.url("git"), SSHKey: client.pem, KnownHosts: srv.line()}).Load(t.Context())
		if err == nil || !strings.Contains(err.Error(), "authentication") {
			t.Fatalf("err = %v, want an authentication failure", err)
		}
		assertFixedGitError(t, err)
		assertNoKeyMaterial(t, err.Error(), append(append(client.material(), agentKey.material()...), srv.hostMaterial()...)...)
		if len(srv.loginsSince(logins)) != 0 {
			t.Fatal("logged in with the agent's key")
		}
		keys := srv.offeredSince(offered)
		if len(keys) == 0 {
			t.Fatal("the configured key was not offered")
		}
		for _, k := range keys {
			if !bytes.Equal(k.Marshal(), client.pub.Marshal()) {
				t.Fatal("offered a key other than SSHKey")
			}
		}
	})
}

// TestGitSourceSSHWrongKeyFailsAuthentication trusts the host but configures
// a key the server does not accept: a fixed authentication error.
func TestGitSourceSSHWrongKeyFailsAuthentication(t *testing.T) {
	accepted := newSSHKey(t)
	client := newSSHKey(t)
	srv := newSSHServer(t, accepted.pub)
	_, err := newGit(t, engine.GitOptions{URL: srv.url("git"), SSHKey: client.pem, KnownHosts: srv.line()}).Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("err = %v, want an authentication failure", err)
	}
	assertFixedGitError(t, err)
	assertNoKeyMaterial(t, err.Error(), append(append(client.material(), srv.hostMaterial()...), knownHostsMaterial(srv.line())...)...)
}

// TestGitSourceSSHNeverLogsOrLeaksKeyMaterial runs failing SSH loads through a
// Syncer while capturing the standard and slog loggers, and checks that no
// error, sync status or log line carries the private key, a fingerprint, the
// known_hosts content, the URL's user or the address.
func TestGitSourceSSHNeverLogsOrLeaksKeyMaterial(t *testing.T) {
	client := newSSHKey(t)
	other := newSSHKey(t)
	accepting := newSSHServer(t, client.pub)
	refusing := newSSHServer(t, other.pub)
	var logs bytes.Buffer
	prevLog, prevFlags := log.Writer(), log.Flags()
	prevSlog := slog.Default()
	log.SetOutput(&logs)
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { log.SetOutput(prevLog); log.SetFlags(prevFlags); slog.SetDefault(prevSlog) })

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	_ = closed.Close()
	_, closedPort, _ := net.SplitHostPort(closedAddr)

	material := append(client.material(), "deploy-user", closedAddr)
	material = append(append(material, accepting.hostMaterial()...), refusing.hostMaterial()...)
	for _, tc := range []struct {
		name string
		opts engine.GitOptions
	}{
		{"untrusted host key", engine.GitOptions{URL: accepting.url("deploy-user"), SSHKey: client.pem, KnownHosts: accepting.pattern() + " " + authorizedKey(other.pub)}},
		{"revoked host key", engine.GitOptions{URL: accepting.url("deploy-user"), SSHKey: client.pem, KnownHosts: accepting.line() + "\n@revoked * " + authorizedKey(accepting.hostKeys[0].PublicKey())}},
		{"key refused", engine.GitOptions{URL: refusing.url("deploy-user"), SSHKey: client.pem, KnownHosts: refusing.line()}},
		{"no repository served", engine.GitOptions{URL: accepting.url("deploy-user"), SSHKey: client.pem, KnownHosts: hashedPattern(accepting.pattern()) + " " + authorizedKey(accepting.hostKeys[0].PublicKey())}},
		{"nothing listening", engine.GitOptions{URL: "ssh://deploy-user@" + closedAddr + "/org/repo.git", SSHKey: client.pem, KnownHosts: "[127.0.0.1]:" + closedPort + " " + authorizedKey(other.pub)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := append(slices.Clone(material), knownHostsMaterial(tc.opts.KnownHosts)...)
			syncer := engine.NewSyncer(newGit(t, tc.opts))
			err := syncer.Poll(t.Context())
			if err == nil {
				t.Fatal("load succeeded")
			}
			assertFixedGitError(t, err)
			assertNoKeyMaterial(t, err.Error(), m...)
			assertNoKeyMaterial(t, syncer.Status().LastError, m...)
			assertNoKeyMaterial(t, logs.String(), m...)
		})
	}
}

// TestGitOptionsNeverPrintSSHKeyOrKnownHosts formats and logs GitOptions every
// way a caller might. Since #96 the URL is never printed either, so its user
// and host do not appear; the Ref does.
func TestGitOptionsNeverPrintSSHKeyOrKnownHosts(t *testing.T) {
	key := newSSHKey(t)
	host := newSSHKey(t)
	kh := "[git.example.invalid]:2222 " + authorizedKey(host.pub) + "\n" + hashedPattern("git.example.invalid") + " " + authorizedKey(host.pub)
	opts := engine.GitOptions{URL: "ssh://deploy-user@git.example.invalid:2222/org/repo.git", Ref: "main", SSHKey: key.pem, KnownHosts: kh}
	var logs bytes.Buffer
	slog.New(slog.NewTextHandler(&logs, nil)).Info("source", "opts", opts)
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("source", "opts", opts)
	material := append(append(key.material(), knownHostsMaterial(kh)...), "deploy-user")
	for _, s := range []string{fmt.Sprint(opts), fmt.Sprintf("%v %+v %#v %s", opts, opts, opts, opts), logs.String()} {
		assertNoKeyMaterial(t, s, append(material, "git.example.invalid", "org/repo.git")...)
		if !strings.Contains(s, "main") {
			t.Fatalf("redaction dropped the Ref: %q", s)
		}
	}
}

// FuzzGitSourceKnownHosts checks that parsing hostile KnownHosts never panics
// and that no error echoes the content or the key.
func FuzzGitSourceKnownHosts(f *testing.F) {
	key := sshKeyFrom(f, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize)))
	hostKey := authorizedKey(key.pub)
	for _, kh := range []string{
		"git.example.invalid " + hostKey,
		"[git.example.invalid]:2222 " + hostKey,
		hashedPattern("git.example.invalid") + " " + hostKey,
		"@revoked * " + hostKey,
		"@cert-authority *.example.invalid " + hostKey,
		"!git.example.invalid,* " + hostKey,
		"|1||| " + hostKey,
		"[git.example.invalid " + hostKey,
		"git.example.invalid ssh-ed25519 \x00\x00\x00",
		strings.Repeat(",", 4096) + " " + hostKey,
		"@revoked",
		"\xff\xfe " + hostKey,
	} {
		f.Add(kh)
	}
	f.Fuzz(func(t *testing.T, kh string) {
		_, err := engine.NewGitSource(engine.GitOptions{URL: "ssh://git@git.example.invalid/org/repo.git", SSHKey: key.pem, KnownHosts: kh})
		if err == nil {
			return
		}
		msg := err.Error()
		if len(kh) >= 16 && strings.Contains(msg, kh) {
			t.Fatalf("error echoes KnownHosts: %q", msg)
		}
		for _, m := range key.material() {
			if strings.Contains(msg, m) {
				t.Fatalf("error echoes key material: %q", msg)
			}
		}
	})
}
