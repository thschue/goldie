package engine_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"golang.org/x/crypto/ssh"

	"github.com/thschue/goldie/internal/engine"
)

// bareRepo is a bare repository in a temp dir, written to by pushing from an
// in-memory working copy over file://, the way a real remote is.
type bareRepo struct {
	t    *testing.T
	url  string
	work *git.Repository
	n    int
}

func newBareRepo(t *testing.T) *bareRepo {
	t.Helper()
	// No git binary: with an empty PATH, a transport that exec'd
	// git-upload-pack or git-receive-pack would fail.
	t.Setenv("PATH", "")
	dir := t.TempDir()
	_, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
		Bare:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := git.PlainInit(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	url := "file://" + dir
	if _, err := work.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	return &bareRepo{t: t, url: url, work: work}
}

// commit replaces the working copy with files, commits on branch and pushes
// it. It returns the commit SHA.
func (r *bareRepo) commit(branch string, files map[string]string) string {
	r.t.Helper()
	wt, err := r.work.Worktree()
	if err != nil {
		r.t.Fatal(err)
	}
	ref := plumbing.NewBranchReferenceName(branch)
	if r.n > 0 {
		_, exists := r.work.Reference(ref, false)
		if err := wt.Checkout(&git.CheckoutOptions{Branch: ref, Create: exists != nil, Force: true}); err != nil {
			r.t.Fatal(err)
		}
	} else if err := r.work.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, ref)); err != nil {
		r.t.Fatal(err)
	}
	r.n++
	if err := removeAll(wt.Filesystem, "."); err != nil {
		r.t.Fatal(err)
	}
	for name, content := range files {
		if target, ok := strings.CutPrefix(content, "symlink:"); ok {
			if err := wt.Filesystem.MkdirAll(path.Dir(name), 0o755); err != nil {
				r.t.Fatal(err)
			}
			if err := wt.Filesystem.Symlink(target, name); err != nil {
				r.t.Fatal(err)
			}
			continue
		}
		f, err := wt.Filesystem.Create(name)
		if err != nil {
			r.t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			r.t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			r.t.Fatal(err)
		}
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		r.t.Fatal(err)
	}
	sig := &object.Signature{Name: "test", Email: "test@example.invalid", When: time.Unix(1700000000+int64(r.n), 0)}
	h, err := wt.Commit(fmt.Sprintf("commit %d", r.n), &git.CommitOptions{Author: sig, Committer: sig, AllowEmptyCommits: true})
	if err != nil {
		r.t.Fatal(err)
	}
	spec := config.RefSpec("+" + ref.String() + ":" + ref.String())
	if err := r.work.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{spec}}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		r.t.Fatal(err)
	}
	return h.String()
}

func (r *bareRepo) tag(name, sha string) {
	r.t.Helper()
	sig := &object.Signature{Name: "test", Email: "test@example.invalid", When: time.Unix(1700000000, 0)}
	if _, err := r.work.CreateTag(name, plumbing.NewHash(sha), &git.CreateTagOptions{Tagger: sig, Message: name}); err != nil {
		r.t.Fatal(err)
	}
	spec := config.RefSpec("refs/tags/" + name + ":refs/tags/" + name)
	if err := r.work.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{spec}}); err != nil {
		r.t.Fatal(err)
	}
}

type billyFS interface {
	ReadDir(string) ([]os.FileInfo, error)
	Remove(string) error
}

func removeAll(fsys billyFS, dir string) error {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		p := path.Join(dir, e.Name())
		if e.IsDir() {
			if err := removeAll(fsys, p); err != nil {
				return err
			}
		}
		if err := fsys.Remove(p); err != nil {
			return err
		}
	}
	return nil
}

func newGit(t *testing.T, opts engine.GitOptions) *engine.GitSource {
	t.Helper()
	src, err := engine.NewGitSource(opts)
	if err != nil {
		t.Fatalf("NewGitSource: %v", err)
	}
	return src
}

func TestGitSourceCommitAndLoad(t *testing.T) {
	repo := newBareRepo(t)
	sha := repo.commit("main", map[string]string{"global/articles/deploy.md": article("Deploy", "deploy")})

	var src engine.Source = newGit(t, engine.GitOptions{URL: repo.url})
	snap, err := src.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if a, ok := snap.Article("deploy"); !ok || a.Name != "Deploy" {
		t.Fatalf("article = %+v, %v", a, ok)
	}
	rev, ok := src.(engine.Revisioner)
	if !ok || rev.Revision() != sha {
		t.Fatalf("revision = %v, want %s", rev, sha)
	}

	// A new commit is picked up on the next Load; the revision follows it.
	sha2 := repo.commit("main", map[string]string{"global/articles/other.md": article("Other")})
	snap, err = src.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Article("deploy"); ok {
		t.Fatal("article from the previous commit still served")
	}
	if _, ok := snap.Article("other"); !ok || rev.Revision() != sha2 {
		t.Fatalf("revision = %s, want %s", rev.Revision(), sha2)
	}
}

func TestGitSourceRef(t *testing.T) {
	repo := newBareRepo(t)
	mainSHA := repo.commit("main", map[string]string{"global/articles/a.md": article("On main")})
	devSHA := repo.commit("dev", map[string]string{"global/articles/a.md": article("On dev")})
	repo.tag("v1", mainSHA)

	for _, tc := range []struct{ ref, name, sha string }{
		{"", "On main", mainSHA},
		{"main", "On main", mainSHA},
		{"dev", "On dev", devSHA},
		{"refs/heads/dev", "On dev", devSHA},
		{"v1", "On main", mainSHA}, // an annotated tag resolves to its commit
	} {
		t.Run(tc.ref, func(t *testing.T) {
			src := newGit(t, engine.GitOptions{URL: repo.url, Ref: tc.ref})
			snap, err := src.Load()
			if err != nil {
				t.Fatal(err)
			}
			if a, _ := snap.Article("a"); a.Name != tc.name || src.Revision() != tc.sha {
				t.Fatalf("got %q at %s, want %q at %s", a.Name, src.Revision(), tc.name, tc.sha)
			}
		})
	}

	_, err := newGit(t, engine.GitOptions{URL: repo.url, Ref: "nope"}).Load()
	if err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("unknown ref: err = %v", err)
	}
}

func TestGitSourceLoadsProjects(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit("main", projectContent())
	snap, err := newGit(t, engine.GitOptions{URL: repo.url}).Load()
	if err != nil {
		t.Fatal(err)
	}
	want := load(t, projectContent())
	if got := snap.Projects(); strings.Join(got, ",") != strings.Join(want.Projects(), ",") {
		t.Fatalf("projects = %v, want %v", got, want.Projects())
	}
	if a, ok := snap.ArticleIn(projA, "sub/payments"); !ok || a.Scope != projA {
		t.Fatalf("project article = %+v, %v", a, ok)
	}
}

func TestGitSourceInvalidContentNamesFile(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit("main", map[string]string{"global/articles/ok.md": article("Ok")})
	src := newGit(t, engine.GitOptions{URL: repo.url})
	syncer := engine.NewSyncer(src)
	if err := syncer.Poll(); err != nil {
		t.Fatal(err)
	}
	good := syncer.Status().Revision

	repo.commit("main", map[string]string{
		"global/articles/ok.md":      article("Ok"),
		"global/articles/sub/bad.md": "no frontmatter",
	})
	err := syncer.Poll()
	if err == nil || !strings.HasPrefix(err.Error(), "global/articles/sub/bad.md: ") {
		t.Fatalf("err = %v, want it to name global/articles/sub/bad.md", err)
	}
	if strings.Contains(err.Error(), repo.url) || strings.Contains(err.Error(), strings.TrimPrefix(repo.url, "file://")) {
		t.Fatalf("error leaks the repository location: %v", err)
	}
	// The last good Snapshot keeps serving.
	if _, ok := syncer.Current().Article("ok"); !ok || syncer.Status().Revision != good {
		t.Fatal("last good Snapshot was replaced")
	}
}

func TestGitSourceSymlinks(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit("main", map[string]string{
		"global/articles/a.md":    article("Ay"),
		"global/articles/link.md": "symlink:/etc/passwd",
		"global/articles/dir":     "symlink:../../projects",
	})
	snap, err := newGit(t, engine.GitOptions{URL: repo.url}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(snap.Articles()); n != 1 {
		t.Fatalf("articles = %d, want 1: symlinks must not be followed", n)
	}

	for _, link := range []string{"global", "global/articles", "projects/github.com/acme/shop/articles"} {
		t.Run(link, func(t *testing.T) {
			repo := newBareRepo(t)
			repo.commit("main", map[string]string{
				"elsewhere/articles/o.md": article("Outside"),
				"elsewhere/o.md":          article("Outside"),
				link:                      "symlink:/elsewhere",
			})
			snap, err := newGit(t, engine.GitOptions{URL: repo.url}).Load()
			if err == nil {
				t.Fatalf("loaded %d articles through a symlink", len(snap.Articles()))
			}
		})
	}
}

func TestGitSourceRejectsBadOptions(t *testing.T) {
	_, key := testSSHKey(t)
	for _, tc := range []struct {
		name string
		opts engine.GitOptions
	}{
		{"no url", engine.GitOptions{}},
		{"plain http", engine.GitOptions{URL: "http://example.invalid/r.git"}},
		{"git protocol", engine.GitOptions{URL: "git://example.invalid/r.git"}},
		{"password in url", engine.GitOptions{URL: "https://user:hunter2@example.invalid/r.git"}},
		{"user in https url", engine.GitOptions{URL: "https://hunter2@example.invalid/r.git"}},
		{"token over ssh", engine.GitOptions{URL: "ssh://git@example.invalid/r.git", Token: "hunter2"}},
		{"token over file", engine.GitOptions{URL: "file:///tmp/r.git", Token: "hunter2"}},
		{"key over https", engine.GitOptions{URL: "https://example.invalid/r.git", SSHKey: key}},
		{"ssh without key", engine.GitOptions{URL: "ssh://git@example.invalid/r.git", KnownHosts: "example.invalid ssh-ed25519 AAAA"}},
		{"ssh without known hosts", engine.GitOptions{URL: "ssh://git@example.invalid/r.git", SSHKey: key}},
		{"bad key", engine.GitOptions{URL: "ssh://git@example.invalid/r.git", SSHKey: "-----BEGIN hunter2", KnownHosts: "x"}},
		{"bad ref", engine.GitOptions{URL: "https://example.invalid/r.git", Ref: "a..b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := engine.NewGitSource(tc.opts)
			if err == nil {
				t.Fatal("accepted")
			}
			assertNoSecret(t, err.Error(), "hunter2", key)
		})
	}
}

// testSSHKey returns an ed25519 key pair, the private half as OpenSSH PEM.
func testSSHKey(t *testing.T) (ssh.PublicKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return sshPub, string(pem.EncodeToMemory(block))
}

// sshServer accepts SSH connections with a fixed host key, records the client
// keys offered and rejects every login.
type sshServer struct {
	addr    string
	hostKey ssh.PublicKey
	mu      sync.Mutex
	offered []ssh.PublicKey
}

func newSSHServer(t *testing.T) *sshServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sshServer{addr: ln.Addr().String(), hostKey: signer.PublicKey()}
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		s.mu.Lock()
		s.offered = append(s.offered, k)
		s.mu.Unlock()
		return nil, errors.New("denied")
	}}
	cfg.AddHostKey(signer)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				_, _, _, _ = ssh.NewServerConn(c, cfg)
				_ = c.Close()
			})
		}
	})
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	return s
}

func (s *sshServer) knownHostsLine(key ssh.PublicKey) string {
	host, port, _ := net.SplitHostPort(s.addr)
	return fmt.Sprintf("[%s]:%s %s", host, port, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
}

func (s *sshServer) offeredKeys() []ssh.PublicKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ssh.PublicKey(nil), s.offered...)
}

func TestGitSourceSSHHostKey(t *testing.T) {
	srv := newSSHServer(t)
	clientPub, clientKey := testSSHKey(t)
	otherHost, _ := testSSHKey(t)
	url := "ssh://git@" + srv.addr + "/repo.git"

	for _, tc := range []struct{ name, knownHosts string }{
		{"different key for the host", srv.knownHostsLine(otherHost)},
		{"host not listed", "other.example.invalid " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(srv.hostKey)))},
		{"key revoked", srv.knownHostsLine(srv.hostKey) + "\n@revoked * " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(srv.hostKey)))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(srv.offeredKeys())
			_, err := newGit(t, engine.GitOptions{URL: url, SSHKey: clientKey, KnownHosts: tc.knownHosts}).Load()
			if err == nil || !strings.Contains(err.Error(), "host key") {
				t.Fatalf("err = %v, want a host key failure", err)
			}
			if len(srv.offeredKeys()) != before {
				t.Fatal("client authenticated to an untrusted host")
			}
			assertNoSecret(t, err.Error(), clientKey, srv.addr, "git@")
		})
	}

	// A trusted host key gets as far as authentication with the given key.
	_, err := newGit(t, engine.GitOptions{URL: url, SSHKey: clientKey, KnownHosts: srv.knownHostsLine(srv.hostKey)}).Load()
	if err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("err = %v, want an authentication failure", err)
	}
	offered := srv.offeredKeys()
	if len(offered) == 0 || !bytes.Equal(offered[len(offered)-1].Marshal(), clientPub.Marshal()) {
		t.Fatal("the configured SSH key was not offered")
	}
	assertNoSecret(t, err.Error(), clientKey, srv.addr, "git@")
}

func TestGitSourceTokenOverHTTPS(t *testing.T) {
	const token = "ghp_0123456789abcdefghijklmnopqrstuvwxyzAB"
	var mu sync.Mutex
	var passwords []string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pw, _ := r.BasicAuth()
		mu.Lock()
		passwords = append(passwords, pw)
		mu.Unlock()
		if pw != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	// The rejected handshake is the point of the first case; keep it quiet.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	url := srv.URL + "/org/repo.git"

	// The server's certificate is not trusted: the load fails before any
	// request, so the token never leaves.
	_, err := newGit(t, engine.GitOptions{URL: url, Token: token}).Load()
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("err = %v, want a certificate failure", err)
	}
	if len(passwords) != 0 {
		t.Fatal("a request reached a server with an untrusted certificate")
	}
	assertNoSecret(t, err.Error(), token, srv.URL)

	// Trusted: the token is sent as the basic-auth password.
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	src, err := engine.NewGitSourceWithCABundle(engine.GitOptions{URL: url, Token: token}, certPEM)
	if err != nil {
		t.Fatal(err)
	}
	_, err = src.Load()
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want repository not found", err)
	}
	mu.Lock()
	sent := len(passwords) > 0 && passwords[len(passwords)-1] == token
	mu.Unlock()
	if !sent {
		t.Fatal("the token was not sent")
	}
	assertNoSecret(t, err.Error(), token, srv.URL)

	// A wrong token is an authentication failure that does not echo it.
	src, err = engine.NewGitSourceWithCABundle(engine.GitOptions{URL: url, Token: "wrong-hunter2"}, certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = src.Load(); err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("err = %v, want an authentication failure", err)
	}
	assertNoSecret(t, err.Error(), "wrong-hunter2", srv.URL)
}

// TestGitSourceNeverLogsOrLeaksCredentials runs failing loads with every kind
// of credential while capturing the standard and slog loggers, and checks that
// neither the errors, the redacted sync status nor any log line carries a
// credential or the URL's user info.
func TestGitSourceNeverLogsOrLeaksCredentials(t *testing.T) {
	const token = "glpat-supersecrettokenvalue123456"
	_, key := testSSHKey(t)
	srv := newSSHServer(t)
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

	sources := []engine.GitOptions{
		{URL: "https://" + closedAddr + "/org/repo.git", Token: token},
		{URL: "ssh://deploy-user@" + srv.addr + "/org/repo.git", SSHKey: key, KnownHosts: srv.knownHostsLine(srv.hostKey)},
		{URL: "deploy-user@" + closedAddr + ":org/repo.git", SSHKey: key, KnownHosts: "x ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB"},
	}
	for _, opts := range sources {
		src, err := engine.NewGitSource(opts)
		if err != nil {
			assertNoSecret(t, err.Error(), token, key, "deploy-user")
			continue
		}
		syncer := engine.NewSyncer(src)
		err = syncer.Poll()
		if err == nil {
			t.Fatalf("%s: load succeeded", opts.URL)
		}
		assertNoSecret(t, err.Error(), token, key, "deploy-user", closedAddr, srv.addr)
		assertNoSecret(t, syncer.Status().LastError, token, key, "deploy-user")
	}
	assertNoSecret(t, logs.String(), token, key, "deploy-user")
}

// FuzzNewGitSource checks that validating a hostile URL never panics and that
// no error echoes the URL or the token.
func FuzzNewGitSource(f *testing.F) {
	for _, u := range []string{
		"https://example.invalid/r.git", "https://u:p@example.invalid/r", "ssh://git@h:22/r",
		"git@h:org/r.git", "file:///tmp/r", "http://h/r", "::", "https://[::1", "\x00", "a@b@c:d",
	} {
		f.Add(u, "tok-secret-value")
	}
	f.Fuzz(func(t *testing.T, url, token string) {
		_, err := engine.NewGitSource(engine.GitOptions{URL: url, Token: token})
		if err == nil {
			return
		}
		msg := err.Error()
		if len(url) >= 6 && strings.Contains(msg, url) {
			t.Fatalf("error echoes the URL: %q", msg)
		}
		if len(token) >= 6 && strings.Contains(msg, token) {
			t.Fatalf("error echoes the token: %q", msg)
		}
	})
}

func assertNoSecret(t *testing.T, s string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if strings.Contains(s, secret) {
			t.Fatalf("output contains a secret %.12q...: %q", secret, s)
		}
		// A PEM key could leak line by line.
		for line := range strings.SplitSeq(secret, "\n") {
			if len(line) > 16 && !strings.HasPrefix(line, "-----") && strings.Contains(s, line) {
				t.Fatalf("output contains part of a secret: %q", s)
			}
		}
	}
}
