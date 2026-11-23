package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"golang.org/x/crypto/ssh"

	"github.com/thschue/goldie/internal/platform"
)

// #96: goldie server serves a git Source. PublicHandler builds the git
// Source's GitOptions from the settings, reports every problem in the one
// *ConfigError naming its variable and never its value, and returns without
// waiting for the first clone. Start runs the poll loop under the server
// context at GOLDIE_POLL_INTERVAL and registers "source:git", which fails
// until the first load and then stays ok while stale (PC-02). A failed poll
// keeps the last good Snapshot and logs one redacted warn; a new revision logs
// one info with the short SHA; a poll cancelled at shutdown logs nothing.
//
// Test seam (does not exist yet, no stub here): minPollInterval, a package
// variable of type time.Duration that is the floor newPublic applies to
// GOLDIE_POLL_INTERVAL (today platform.MinPollInterval), so these tests can
// poll every few milliseconds. It follows activityKeepalive: a variable so
// tests can shorten it.

// gitCanary marks values that must never reach an error, a log line or a probe.
const gitCanary = "c4n4ry9f1e2d3c4b5a69788"

// shortPolls lowers the poll interval floor for the test, so the poll loop
// runs every interval of cfg.PollInterval.
func shortPolls(t *testing.T) {
	t.Helper()
	old := minPollInterval
	minPollInterval = time.Millisecond
	t.Cleanup(func() { minPollInterval = old })
}

// gitConfig is publicConfig with the git Source at url instead of the dir
// Source, polled every interval.
func gitConfig(t *testing.T, url string, interval time.Duration) platform.Config {
	t.Helper()
	cfg := publicConfig(t)
	cfg.ContentDir = ""
	cfg.GitURL = url
	cfg.PollInterval = interval
	return cfg
}

// bareRepo is a bare repository in a temp dir, written with go-git's object
// store directly (no worktree, no transport, no git binary), served over
// file://.
type bareRepo struct {
	t      *testing.T
	dir    string
	url    string
	repo   *git.Repository
	parent plumbing.Hash
	n      int
}

func newBareRepo(t *testing.T) *bareRepo {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		Bare:        true,
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &bareRepo{t: t, dir: dir, url: "file://" + dir, repo: repo}
}

// commit writes files as the whole tree of a new commit on main and returns
// its SHA.
func (r *bareRepo) commit(files map[string]string) string {
	r.t.Helper()
	r.n++
	sig := object.Signature{Name: "test", Email: "test@example.invalid", When: time.Unix(1700000000+int64(r.n), 0)}
	c := &object.Commit{Author: sig, Committer: sig, Message: fmt.Sprintf("commit %d", r.n), TreeHash: r.tree(files)}
	if !r.parent.IsZero() {
		c.ParentHashes = []plumbing.Hash{r.parent}
	}
	obj := r.repo.Storer.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		r.t.Fatal(err)
	}
	h, err := r.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("main"), h)); err != nil {
		r.t.Fatal(err)
	}
	r.parent = h
	return h.String()
}

// tree stores files (slash-separated paths) as tree objects and returns the
// root tree's hash.
func (r *bareRepo) tree(files map[string]string) plumbing.Hash {
	r.t.Helper()
	sub := map[string]map[string]string{}
	var entries []object.TreeEntry
	for name, content := range files {
		if dir, rest, ok := strings.Cut(name, "/"); ok {
			if sub[dir] == nil {
				sub[dir] = map[string]string{}
			}
			sub[dir][rest] = content
			continue
		}
		obj := r.repo.Storer.NewEncodedObject()
		obj.SetType(plumbing.BlobObject)
		w, err := obj.Writer()
		if err != nil {
			r.t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			r.t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			r.t.Fatal(err)
		}
		h, err := r.repo.Storer.SetEncodedObject(obj)
		if err != nil {
			r.t.Fatal(err)
		}
		entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Regular, Hash: h})
	}
	for dir, files := range sub {
		entries = append(entries, object.TreeEntry{Name: dir, Mode: filemode.Dir, Hash: r.tree(files)})
	}
	key := func(e object.TreeEntry) string {
		if e.Mode == filemode.Dir {
			return e.Name + "/"
		}
		return e.Name
	}
	sort.Slice(entries, func(i, j int) bool { return key(entries[i]) < key(entries[j]) })
	obj := r.repo.Storer.NewEncodedObject()
	if err := (&object.Tree{Entries: entries}).Encode(obj); err != nil {
		r.t.Fatal(err)
	}
	h, err := r.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		r.t.Fatal(err)
	}
	return h
}

// gitArticle is an article file for the git Source at global/articles/<id>.md.
func gitArticle(id string) map[string]string {
	return map[string]string{"global/articles/" + id + ".md": "---\nname: " + id + "\nkeywords: [git]\n---\nBody of " + id + ".\n"}
}

// with returns files merged with more.
func with(files map[string]string, more map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range files {
		out[k] = v
	}
	for k, v := range more {
		out[k] = v
	}
	return out
}

// testSSHKey is an unencrypted ed25519 key: its PEM and its authorized_keys
// form (type and base64, no comment).
func testSSHKey(t *testing.T) (pemKey, authorized string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block)), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp)))
}

// keyMaterial are the lines of a PEM key that must never be printed.
func keyMaterial(pemKey string) []string {
	out := []string{"PRIVATE KEY"}
	for _, l := range strings.Split(pemKey, "\n") {
		if len(l) >= 16 && !strings.HasPrefix(l, "-----") {
			out = append(out, l)
		}
	}
	return out
}

// gitSecrets is a git Source configuration whose secrets are canaries: an
// SSH key, known hosts carrying a canary comment, a token and a user name.
type gitSecrets struct {
	key, knownHostsLine, token, user string
	material                         []string
}

func newGitSecrets(t *testing.T, host string) gitSecrets {
	t.Helper()
	key, auth := testSSHKey(t)
	hostAuth := strings.Fields(auth)
	s := gitSecrets{
		key:            key,
		knownHostsLine: host + " " + hostAuth[0] + " " + hostAuth[1] + " kh-" + gitCanary,
		token:          "ghp_tok" + gitCanary,
		user:           "usr" + gitCanary,
	}
	s.material = append(keyMaterial(key), gitCanary, hostAuth[1])
	return s
}

// assertNoGitSecret fails when s holds any canary or key material.
func assertNoGitSecret(t *testing.T, where, s string, secrets ...string) {
	t.Helper()
	for _, secret := range append(secrets, gitCanary) {
		if secret != "" && strings.Contains(s, secret) {
			t.Errorf("%s contains a secret (%.16q...): %s", where, secret, s)
		}
	}
}

// configError returns err as the one *ConfigError, failing the test when it
// is anything else.
func configError(t *testing.T, err error) *ConfigError {
	t.Helper()
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want a *ConfigError", err, err)
	}
	return ce
}

// eventually polls cond until it holds or within passes.
func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("after %v: %s", within, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// pubGet sends an authenticated GET for path straight to h.
func pubGet(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// runningGit is a git Source started the way goldie server starts it.
type runningGit struct {
	pub    *Public
	admin  string
	logs   *lockedBuffer
	from   int // log lines written before Start
	cancel context.CancelFunc
}

// startGit builds the public handler over cfg with a JSON logger, starts it
// under a cancellable server context and serves its readiness on an admin
// listener. PublicHandler must succeed.
func startGit(t *testing.T, cfg platform.Config) *runningGit {
	t.Helper()
	logs := &lockedBuffer{}
	pub, err := PublicHandler(cfg, platform.NewLogger(logs, slog.LevelDebug, "server"))
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	if pub == nil {
		t.Fatal("PublicHandler returned a nil *Public without an error")
	}
	g := &runningGit{pub: pub, logs: logs, from: len(logs.lines())}
	reg := platform.NewRegistry(0)
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	t.Cleanup(cancel)
	startReturns(t, pub, ctx, reg)
	g.admin = adminServer(t, cfg, pub, reg)
	return g
}

// count is the number of records since Start at level whose line contains
// every one of subs.
func (g *runningGit) count(t *testing.T, level string, subs ...string) int {
	t.Helper()
	n := 0
	for _, l := range g.logs.lines()[g.from:] {
		if !strings.Contains(l, `"level":"`+level+`"`) {
			continue
		}
		all := true
		for _, s := range subs {
			all = all && strings.Contains(l, s)
		}
		if all {
			n++
		}
	}
	return n
}

// sourceGit returns the /readyz status code, body and the source:git check's
// status ("" when it is not listed).
func (g *runningGit) sourceGit(t *testing.T) (int, string, string) {
	t.Helper()
	code, raw, checks := readyz(t, g.admin)
	for _, c := range checks {
		if c.Name == "source:git" {
			return code, raw, c.Status
		}
	}
	return code, raw, ""
}

// readyOnGit waits for /readyz to answer 200 with exactly [source:git ok].
func (g *runningGit) readyOnGit(t *testing.T) {
	t.Helper()
	eventually(t, 5*time.Second, "/readyz never listed source:git ok", func() bool {
		code, _, checks := readyz(t, g.admin)
		return code == http.StatusOK && len(checks) == 1 && checks[0] == (readyzCheck{Name: "source:git", Status: "ok"})
	})
}

// --- exactly one Source --------------------------------------------------------

// With neither GOLDIE_CONTENT_DIR nor GOLDIE_GIT_URL, PublicHandler fails in
// the one *ConfigError, naming both variables, together with every other
// setting problem.
func TestNoSourceFailsNamingBothVariablesInTheOneConfigError(t *testing.T) {
	cfg := publicConfig(t)
	cfg.ContentDir, cfg.GitURL = "", ""
	pub, err := PublicHandler(cfg, nil)
	if err == nil || pub != nil {
		t.Fatalf("PublicHandler without a Source = %v, %v; want a nil *Public and an error", pub, err)
	}
	ce := configError(t, err)
	for _, name := range []string{platform.EnvContentDir, platform.EnvGitURL} {
		if !strings.Contains(ce.Error(), name) {
			t.Errorf("error = %q, want it to name %s", ce, name)
		}
	}

	cfg.APIToken = ""
	_, err = PublicHandler(cfg, nil)
	ce = configError(t, err)
	for _, name := range []string{platform.EnvAPIToken, platform.EnvContentDir, platform.EnvGitURL} {
		if !strings.Contains(ce.Error(), name) {
			t.Errorf("error = %q, want every problem in one *ConfigError, naming %s", ce, name)
		}
	}
}

// --- file:// and the allow flag ------------------------------------------------

// A file:// URL is refused unless GOLDIE_GIT_ALLOW_FILE_URL is true, in any
// letter case of the scheme; the error names both variables and never the
// path. With the flag it is accepted.
func TestFileURLIsRefusedUnlessTheAllowFlagIsTrue(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit(gitArticle("git-first"))
	for _, url := range []string{repo.url, "FILE://" + repo.dir, "File://" + repo.dir, "file://localhost" + repo.dir} {
		cfg := gitConfig(t, url, time.Hour)
		cfg.GitAllowFileURL = false
		pub, err := PublicHandler(cfg, nil)
		if err == nil || pub != nil {
			t.Errorf("%q without %s: PublicHandler = %v, %v; want it refused", url, platform.EnvGitAllowFileURL, pub, err)
			continue
		}
		ce := configError(t, err)
		for _, name := range []string{platform.EnvGitURL, platform.EnvGitAllowFileURL} {
			if !strings.Contains(ce.Error(), name) {
				t.Errorf("%q: error = %q, want it to name %s", url, ce, name)
			}
		}
		if strings.Contains(ce.Error(), repo.dir) {
			t.Errorf("%q: error echoes the path: %q", url, ce)
		}
	}
	cfg := gitConfig(t, repo.url, time.Hour)
	cfg.GitAllowFileURL = true
	if _, err := PublicHandler(cfg, nil); err != nil {
		t.Fatalf("file:// with %s=true: %v, want it accepted", platform.EnvGitAllowFileURL, err)
	}
}

// --- the scheme allowlist ------------------------------------------------------

// Only https, ssh, scp-like and (with the flag) file URLs are accepted.
// Everything else, the ext:: and fd:: remote helpers above all, is refused
// naming GOLDIE_GIT_URL, even with GOLDIE_GIT_ALLOW_FILE_URL=true, and the
// error never echoes the URL. Accepting contacts no server.
func TestOnlyAllowlistedGitURLSchemesAreAccepted(t *testing.T) {
	sec := newGitSecrets(t, "git.example.invalid")
	withSSH := func(cfg *platform.Config) { cfg.GitSSHKey, cfg.GitSSHKnownHosts = sec.key, sec.knownHostsLine }
	repo := newBareRepo(t)
	repo.commit(gitArticle("git-first"))
	accepted := map[string]func(*platform.Config){
		"https://git.example.invalid/org/content.git":     nil,
		"HTTPS://git.example.invalid/org/content.git":     nil,
		"ssh://git@git.example.invalid/org/content.git":   withSSH,
		"ssh://git.example.invalid:2222/org/content.git":  withSSH,
		"git@git.example.invalid:org/content.git":         withSSH,
		"deploy@git.example.invalid:/srv/git/content.git": withSSH,
		repo.url: func(cfg *platform.Config) { cfg.GitAllowFileURL = true },
	}
	for url, set := range accepted {
		cfg := gitConfig(t, url, time.Hour)
		if set != nil {
			set(&cfg)
		}
		began := time.Now()
		if _, err := PublicHandler(cfg, nil); err != nil {
			t.Errorf("%q: PublicHandler: %v, want it accepted", url, err)
		}
		if d := time.Since(began); d > 3*time.Second {
			t.Errorf("%q: PublicHandler took %v: it must not contact the server", url, d)
		}
	}
	refused := []string{
		"ext::sh -c touch% /tmp/pwned-" + gitCanary,
		"ext::ssh -oProxyCommand=touch% /tmp/pwned-" + gitCanary + " git.example.invalid %S content.git",
		"fd::17",
		"fd::0,1/" + gitCanary,
		"https::git.example.invalid/" + gitCanary + ".git",
		"http://git.example.invalid/" + gitCanary + ".git",
		"git://git.example.invalid/" + gitCanary + ".git",
		"git+ssh://git@git.example.invalid/" + gitCanary + ".git",
		"ftp://git.example.invalid/" + gitCanary + ".git",
		"file:" + gitCanary + "/content.git",
		"/srv/" + gitCanary + "/content.git",
		"./" + gitCanary + "/content.git",
		gitCanary + "/content.git",
	}
	for _, url := range refused {
		cfg := gitConfig(t, url, time.Hour)
		cfg.GitAllowFileURL = true
		pub, err := PublicHandler(cfg, nil)
		if err == nil || pub != nil {
			t.Errorf("%q: PublicHandler = %v, %v; want it refused", url, pub, err)
			continue
		}
		ce := configError(t, err)
		if !strings.Contains(ce.Error(), platform.EnvGitURL) {
			t.Errorf("%q: error = %q, want it to name %s", url, ce, platform.EnvGitURL)
		}
		assertNoGitSecret(t, "the error for "+url, ce.Error())
	}
}

// --- no leading dash -----------------------------------------------------------

// GOLDIE_GIT_REF and GOLDIE_GIT_URL may not start with "-", which a git
// command line would read as an option. Each is refused naming its variable.
// A dash elsewhere is fine.
func TestRefAndURLMayNotStartWithADash(t *testing.T) {
	const url = "https://git.example.invalid/org/content.git"
	for _, ref := range []string{"-", "-main", "--upload-pack=touch /tmp/pwned", "--output=/tmp/" + gitCanary} {
		cfg := gitConfig(t, url, time.Hour)
		cfg.GitRef = ref
		pub, err := PublicHandler(cfg, nil)
		if err == nil || pub != nil {
			t.Errorf("ref %q: PublicHandler = %v, %v; want it refused", ref, pub, err)
			continue
		}
		if ce := configError(t, err); !strings.Contains(ce.Error(), platform.EnvGitRef) {
			t.Errorf("ref %q: error = %q, want it to name %s", ref, ce, platform.EnvGitRef)
		}
	}
	for _, bad := range []string{
		"-oProxyCommand=touch /tmp/pwned",
		"--upload-pack=touch /tmp/pwned",
		"-https://git.example.invalid/org/content.git",
		"-git@git.example.invalid:org/content.git",
	} {
		cfg := gitConfig(t, bad, time.Hour)
		cfg.GitAllowFileURL = true
		pub, err := PublicHandler(cfg, nil)
		if err == nil || pub != nil {
			t.Errorf("URL %q: PublicHandler = %v, %v; want it refused", bad, pub, err)
			continue
		}
		ce := configError(t, err)
		if !strings.Contains(ce.Error(), platform.EnvGitURL) {
			t.Errorf("URL %q: error = %q, want it to name %s", bad, ce, platform.EnvGitURL)
		}
		if strings.Contains(ce.Error(), "ProxyCommand") || strings.Contains(ce.Error(), "upload-pack") {
			t.Errorf("URL %q: error echoes the URL: %q", bad, ce)
		}
	}
	for _, ref := range []string{"feature-x", "refs/heads/release-1", "v1.0.0-rc.1"} {
		cfg := gitConfig(t, url, time.Hour)
		cfg.GitRef = ref
		if _, err := PublicHandler(cfg, nil); err != nil {
			t.Errorf("ref %q: %v, want a dash after the first character accepted", ref, err)
		}
	}
}

// --- every NewGitSource error names its variable --------------------------------

// Each problem the git Source finds in its options is reported in the one
// *ConfigError, naming the variable to fix, and never a secret, the key, the
// known hosts or the URL's userinfo.
func TestEveryGitSourceErrorNamesItsVariable(t *testing.T) {
	sec := newGitSecrets(t, "git.example.invalid")
	ca := httptest.NewTLSServer(http.NotFoundHandler())
	defer ca.Close()
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Certificate().Raw}))
	const (
		https = "https://git.example.invalid/org/content.git"
		sshU  = "ssh://git@git.example.invalid/org/content.git"
		scp   = "git@git.example.invalid:org/content.git"
	)
	repo := newBareRepo(t)
	for _, tc := range []struct {
		name string
		want string
		cfg  platform.Config
	}{
		{"query", platform.EnvGitURL, platform.Config{GitURL: https + "?private_token=" + sec.token}},
		{"fragment", platform.EnvGitURL, platform.Config{GitURL: https + "#" + sec.token}},
		{"password", platform.EnvGitURL, platform.Config{GitURL: "https://deploy:pw" + gitCanary + "@git.example.invalid/org/content.git"}},
		{"token as https user", platform.EnvGitURL, platform.Config{GitURL: "https://" + sec.token + "@git.example.invalid/org/content.git"}},
		{"ssh password", platform.EnvGitURL, platform.Config{GitURL: "ssh://git:pw" + gitCanary + "@git.example.invalid/org/content.git", GitSSHKey: sec.key, GitSSHKnownHosts: sec.knownHostsLine}},
		{"no host", platform.EnvGitURL, platform.Config{GitURL: "https:///org/content.git"}},
		{"bad port", platform.EnvGitURL, platform.Config{GitURL: "https://git.example.invalid:port/org/content.git"}},
		{"token with ssh", platform.EnvGitToken, platform.Config{GitURL: sshU, GitToken: sec.token, GitSSHKey: sec.key, GitSSHKnownHosts: sec.knownHostsLine}},
		{"token with scp-like", platform.EnvGitToken, platform.Config{GitURL: scp, GitToken: sec.token, GitSSHKey: sec.key, GitSSHKnownHosts: sec.knownHostsLine}},
		{"token with file", platform.EnvGitToken, platform.Config{GitURL: repo.url, GitAllowFileURL: true, GitToken: sec.token}},
		{"key with https", platform.EnvGitSSHKey, platform.Config{GitURL: https, GitSSHKey: sec.key}},
		{"known hosts with https", platform.EnvGitSSHKnownHosts, platform.Config{GitURL: https, GitSSHKnownHosts: sec.knownHostsLine}},
		{"CA bundle with ssh", platform.EnvGitCABundle, platform.Config{GitURL: sshU, GitCABundle: caPEM, GitSSHKey: sec.key, GitSSHKnownHosts: sec.knownHostsLine}},
		{"CA bundle not PEM", platform.EnvGitCABundle, platform.Config{GitURL: https, GitCABundle: "not a bundle " + gitCanary}},
		{"CA bundle holding a key", platform.EnvGitCABundle, platform.Config{GitURL: https, GitCABundle: caPEM + sec.key}},
		{"ssh without key", platform.EnvGitSSHKey, platform.Config{GitURL: sshU, GitSSHKnownHosts: sec.knownHostsLine}},
		{"scp-like without key", platform.EnvGitSSHKey, platform.Config{GitURL: scp, GitSSHKnownHosts: sec.knownHostsLine}},
		{"ssh without known hosts", platform.EnvGitSSHKnownHosts, platform.Config{GitURL: sshU, GitSSHKey: sec.key}},
		{"scp-like without known hosts", platform.EnvGitSSHKnownHosts, platform.Config{GitURL: scp, GitSSHKey: sec.key}},
		{"ssh with blank known hosts", platform.EnvGitSSHKnownHosts, platform.Config{GitURL: sshU, GitSSHKey: sec.key, GitSSHKnownHosts: " \n\t\r\n"}},
		{"ssh key not a key", platform.EnvGitSSHKey, platform.Config{GitURL: sshU, GitSSHKey: "-----BEGIN OPENSSH PRIVATE KEY-----\n" + gitCanary + "\n-----END OPENSSH PRIVATE KEY-----", GitSSHKnownHosts: sec.knownHostsLine}},
		{"known hosts malformed", platform.EnvGitSSHKnownHosts, platform.Config{GitURL: sshU, GitSSHKey: sec.key, GitSSHKnownHosts: "git.example.invalid ssh-ed25519 " + gitCanary}},
		{"known hosts only revoked", platform.EnvGitSSHKnownHosts, platform.Config{GitURL: sshU, GitSSHKey: sec.key, GitSSHKnownHosts: "@revoked " + sec.knownHostsLine}},
		{"bad ref", platform.EnvGitRef, platform.Config{GitURL: https, GitRef: "a..b"}},
		{"ref with a space", platform.EnvGitRef, platform.Config{GitURL: https, GitRef: "main branch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := gitConfig(t, tc.cfg.GitURL, time.Hour)
			cfg.GitRef, cfg.GitToken, cfg.GitSSHKey, cfg.GitSSHKnownHosts = tc.cfg.GitRef, tc.cfg.GitToken, tc.cfg.GitSSHKey, tc.cfg.GitSSHKnownHosts
			cfg.GitCABundle, cfg.GitAllowFileURL = tc.cfg.GitCABundle, tc.cfg.GitAllowFileURL
			pub, err := PublicHandler(cfg, nil)
			if err == nil || pub != nil {
				t.Fatalf("PublicHandler = %v, %v; want a nil *Public and an error", pub, err)
			}
			ce := configError(t, err)
			if !strings.Contains(ce.Error(), tc.want) {
				t.Errorf("error = %q, want it to name %s", ce, tc.want)
			}
			assertNoGitSecret(t, "the error", ce.Error(), append(sec.material, sec.token, "deploy:", caPEM[30:60])...)
		})
	}
}

// A git Source problem and every other setting problem are reported together
// in one *ConfigError, so the operator fixes them in one restart.
func TestGitSourceProblemsJoinEveryOtherSettingProblem(t *testing.T) {
	sec := newGitSecrets(t, "git.example.invalid")
	cfg := gitConfig(t, "ssh://git@git.example.invalid/org/content.git", time.Hour)
	cfg.GitSSHKey = sec.key
	cfg.APIToken = "short " + gitCanary
	cfg.RateLimit = platform.RateLimit{}
	_, err := PublicHandler(cfg, nil)
	ce := configError(t, err)
	for _, name := range []string{platform.EnvAPIToken, platform.EnvRateLimit, platform.EnvGitSSHKnownHosts} {
		if !strings.Contains(ce.Error(), name) {
			t.Errorf("error = %q, want it to name %s", ce, name)
		}
	}
	assertNoGitSecret(t, "the error", ce.Error(), sec.material...)
}

// --- startup does not wait; readiness; not_ready --------------------------------

// stallingGitServer is an https server whose every request blocks until the
// client gives up or the test ends. requests counts the requests that
// arrived.
func stallingGitServer(t *testing.T) (srv *httptest.Server, caPEM string, requests *atomic.Int32) {
	t.Helper()
	requests = &atomic.Int32{}
	release := make(chan struct{})
	srv = httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return srv, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})), requests
}

// Startup does not wait for the first clone: PublicHandler and Start return
// while the server stalls the first load. Until that load, admin /readyz
// fails source:git and /api/v1 answers 503 not_ready. Nothing on /readyz or
// in the logs shows the token.
func TestStartupDoesNotWaitForTheFirstClone(t *testing.T) {
	srv, caPEM, requests := stallingGitServer(t)
	sec := newGitSecrets(t, "git.example.invalid")
	cfg := gitConfig(t, srv.URL+"/org/content.git", time.Hour)
	cfg.GitToken, cfg.GitCABundle = sec.token, caPEM

	began := time.Now()
	g := startGit(t, cfg)
	if d := time.Since(began); d > 3*time.Second {
		t.Fatalf("PublicHandler and Start took %v: startup must not wait for the first clone", d)
	}
	eventually(t, 5*time.Second, "the poll loop never asked the git server", func() bool { return requests.Load() > 0 })

	code, raw, status := g.sourceGit(t)
	if code != http.StatusServiceUnavailable || status == "" || status == "ok" {
		t.Errorf("/readyz before the first load = %d %s, want 503 with source:git not ok", code, raw)
	}
	assertNoGitSecret(t, "/readyz", raw, sec.token, srv.URL, srv.Listener.Addr().String())
	for _, path := range []string{lookupPath, "/api/v1/search?q=git", "/api/v1/projects"} {
		code, body := pubGet(t, g.pub, path)
		if code != http.StatusServiceUnavailable || !strings.Contains(body, `"not_ready"`) {
			t.Errorf("GET %s before the first load = %d %s, want 503 not_ready", path, code, body)
		}
	}
	assertNoGitSecret(t, "the logs", strings.Join(g.logs.lines(), "\n"), sec.token,
		base64.StdEncoding.EncodeToString([]byte("x-access-token:"+sec.token)))
}

// A Source whose first load fails keeps the server alive but not ready: one
// redacted warn per failed poll, source:git failed, /api/v1 not_ready. A
// missing ref and a missing repository behave alike.
func TestFirstLoadFailureKeepsTheServerAliveButNotReady(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit(gitArticle("git-first"))
	missing := t.TempDir() + "/no-such-repo.git"
	for name, set := range map[string]func(*platform.Config){
		"missing ref":  func(cfg *platform.Config) { cfg.GitRef = "no-such-branch" },
		"missing repo": func(cfg *platform.Config) { cfg.GitURL = "file://" + missing },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := gitConfig(t, repo.url, time.Hour)
			cfg.GitAllowFileURL = true
			set(&cfg)
			g := startGit(t, cfg)
			eventually(t, 5*time.Second, "a failed first load logged no warn", func() bool { return g.count(t, "warn") > 0 })
			time.Sleep(200 * time.Millisecond)
			if n := g.count(t, "warn"); n != 1 {
				t.Errorf("warn records after one failed poll = %d, want 1:\n%s", n, strings.Join(g.logs.lines(), "\n"))
			}
			if n := g.count(t, "error"); n != 0 {
				t.Errorf("error records = %d, want 0: a failed poll is a warn", n)
			}
			code, raw, status := g.sourceGit(t)
			if code != http.StatusServiceUnavailable || status == "" || status == "ok" {
				t.Errorf("/readyz = %d %s, want 503 with source:git not ok", code, raw)
			}
			if strings.Contains(raw, "rror") || strings.Contains(raw, "no-such") {
				t.Errorf("/readyz echoes the error: %s", raw)
			}
			if code, body := pubGet(t, g.pub, "/api/v1/articles/git-first"); code != http.StatusServiceUnavailable || !strings.Contains(body, `"not_ready"`) {
				t.Errorf("GET article = %d %s, want 503 not_ready", code, body)
			}
		})
	}
}

// After the first load source:git is ok and the API serves the git content.
// When later polls fail, the last good Snapshot is still served and
// source:git stays ok (PC-02), and every failed poll logs exactly one warn.
func TestSourceGitStaysReadyWhileStaleAndAFailedPollLogsOneWarn(t *testing.T) {
	shortPolls(t)
	repo := newBareRepo(t)
	repo.commit(gitArticle("git-first"))
	cfg := gitConfig(t, repo.url, 400*time.Millisecond)
	cfg.GitAllowFileURL = true
	g := startGit(t, cfg)
	g.readyOnGit(t)
	if code, body := pubGet(t, g.pub, "/api/v1/articles/git-first"); code != http.StatusOK {
		t.Fatalf("GET article after the first load = %d %s, want 200", code, body)
	}
	if n := g.count(t, "warn"); n != 0 {
		t.Fatalf("warn records before any failure = %d, want 0", n)
	}

	if err := os.RemoveAll(repo.dir); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "a failed poll logged no warn", func() bool { return g.count(t, "warn") > 0 })
	time.Sleep(150 * time.Millisecond) // well under one interval: still one failed poll
	if n := g.count(t, "warn"); n != 1 {
		t.Errorf("warn records after one failed poll = %d, want exactly 1:\n%s", n, strings.Join(g.logs.lines(), "\n"))
	}
	code, raw, status := g.sourceGit(t)
	if code != http.StatusOK || status != "ok" {
		t.Errorf("/readyz while stale = %d %s, want 200 with source:git ok (PC-02)", code, raw)
	}
	if code, body := pubGet(t, g.pub, "/api/v1/articles/git-first"); code != http.StatusOK {
		t.Errorf("GET article while stale = %d %s, want the last good Snapshot served", code, body)
	}
	if n := g.count(t, "error"); n != 0 {
		t.Errorf("error records = %d, want 0: a failed poll is a warn", n)
	}
}

// A failed poll's warn is redacted: no token, no basic-auth header, no key
// material, no known hosts and no URL userinfo, whatever the transport
// failure says.
func TestFailedPollWarnIsRedacted(t *testing.T) {
	t.Run("https", func(t *testing.T) {
		sec := newGitSecrets(t, "git.example.invalid")
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Echo everything the client sent, as a hostile server might.
			http.Error(w, r.Header.Get("Authorization")+" "+r.URL.String()+" "+sec.token, http.StatusInternalServerError)
		}))
		defer srv.Close()
		cfg := gitConfig(t, srv.URL+"/org/content.git", time.Hour)
		cfg.GitToken = sec.token
		cfg.GitCABundle = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
		g := startGit(t, cfg)
		eventually(t, 5*time.Second, "a failed poll logged no warn", func() bool { return g.count(t, "warn") > 0 })
		logs := strings.Join(g.logs.lines(), "\n")
		assertNoGitSecret(t, "the logs", logs, sec.token, base64.StdEncoding.EncodeToString([]byte("x-access-token:"+sec.token)))
		_, raw, _ := g.sourceGit(t)
		assertNoGitSecret(t, "/readyz", raw, sec.token)
	})
	t.Run("ssh", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		_ = ln.Close() // nothing listens: every poll fails at once
		host, port, _ := net.SplitHostPort(addr)
		sec := newGitSecrets(t, "["+host+"]:"+port)
		cfg := gitConfig(t, "ssh://"+sec.user+"@"+addr+"/org/content.git", time.Hour)
		cfg.GitSSHKey, cfg.GitSSHKnownHosts = sec.key, sec.knownHostsLine
		g := startGit(t, cfg)
		eventually(t, 5*time.Second, "a failed poll logged no warn", func() bool { return g.count(t, "warn") > 0 })
		assertNoGitSecret(t, "the logs", strings.Join(g.logs.lines(), "\n"), append(sec.material, sec.user)...)
		_, raw, _ := g.sourceGit(t)
		assertNoGitSecret(t, "/readyz", raw, append(sec.material, sec.user, addr)...)
	})
}

// --- revisions -------------------------------------------------------------------

// The first load and each new revision log exactly one info record with the
// short SHA, never the full one; polls that find the same revision log
// nothing more about it.
func TestNewRevisionLogsOneInfoWithTheShortSHA(t *testing.T) {
	shortPolls(t)
	repo := newBareRepo(t)
	first := repo.commit(gitArticle("git-first"))
	cfg := gitConfig(t, repo.url, 30*time.Millisecond)
	cfg.GitAllowFileURL = true
	g := startGit(t, cfg)
	g.readyOnGit(t)
	eventually(t, 5*time.Second, "no info record with the first revision's short SHA", func() bool { return g.count(t, "info", first[:7]) > 0 })
	time.Sleep(300 * time.Millisecond) // ten polls at the same revision
	if n := g.count(t, "info", first[:7]); n != 1 {
		t.Errorf("info records with %s = %d, want exactly 1", first[:7], n)
	}

	second := repo.commit(with(gitArticle("git-first"), gitArticle("git-second")))
	eventually(t, 5*time.Second, "the new commit was never served", func() bool {
		code, _ := pubGet(t, g.pub, "/api/v1/articles/git-second")
		return code == http.StatusOK
	})
	eventually(t, 5*time.Second, "no info record with the new revision's short SHA", func() bool { return g.count(t, "info", second[:7]) > 0 })
	time.Sleep(300 * time.Millisecond)
	if n := g.count(t, "info", second[:7]); n != 1 {
		t.Errorf("info records with %s = %d, want exactly 1", second[:7], n)
	}
	logs := strings.Join(g.logs.lines(), "\n")
	for _, full := range []string{first, second} {
		if strings.Contains(logs, full) {
			t.Errorf("the logs carry the full SHA %s, want only the short one:\n%s", full, logs)
		}
	}
	if n := g.count(t, "warn") + g.count(t, "error"); n != 0 {
		t.Errorf("warn or error records on healthy polls = %d, want 0", n)
	}
}

// --- the poll loop runs at GOLDIE_POLL_INTERVAL under the server context ------------

// No poll runs between ticks of GOLDIE_POLL_INTERVAL: with an hour's interval
// a new commit is not served.
func TestPollsRunAtThePollInterval(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit(gitArticle("git-first"))
	cfg := gitConfig(t, repo.url, time.Hour)
	cfg.GitAllowFileURL = true
	g := startGit(t, cfg)
	g.readyOnGit(t)
	repo.commit(with(gitArticle("git-first"), gitArticle("git-second")))
	time.Sleep(500 * time.Millisecond)
	if code, body := pubGet(t, g.pub, "/api/v1/articles/git-second"); code != http.StatusNotFound {
		t.Errorf("GET new article before the next tick = %d %s, want 404: no poll between ticks", code, body)
	}
}

// Cancelling the server context ends the poll loop: a commit made afterwards
// is never served, while source:git stays ok and the last good Snapshot is
// served (PC-02).
func TestCancellingTheServerContextStopsPolling(t *testing.T) {
	shortPolls(t)
	waitLoops(t, 0)
	repo := newBareRepo(t)
	repo.commit(gitArticle("git-first"))
	cfg := gitConfig(t, repo.url, 30*time.Millisecond)
	cfg.GitAllowFileURL = true
	g := startGit(t, cfg)
	g.readyOnGit(t)
	waitLoops(t, 1)
	g.cancel()
	waitLoops(t, 0)
	repo.commit(with(gitArticle("git-first"), gitArticle("git-after-stop")))
	time.Sleep(300 * time.Millisecond)
	if code, _ := pubGet(t, g.pub, "/api/v1/articles/git-after-stop"); code != http.StatusNotFound {
		t.Errorf("GET a commit made after cancel = %d, want 404: no poll after the server context ends", code)
	}
	if code, _ := pubGet(t, g.pub, "/api/v1/articles/git-first"); code != http.StatusOK {
		t.Errorf("GET the last good content after cancel = %d, want 200", code)
	}
	if code, raw, status := g.sourceGit(t); code != http.StatusOK || status != "ok" {
		t.Errorf("/readyz after cancel = %d %s, want 200 with source:git ok", code, raw)
	}
}

// A poll cancelled at shutdown (ctx.Err() != nil) is not a failure: stopping
// the server during a load logs no warn and no error.
func TestShutdownDuringALoadLogsNoError(t *testing.T) {
	waitLoops(t, 0)
	srv, caPEM, requests := stallingGitServer(t)
	cfg := gitConfig(t, srv.URL+"/org/content.git", time.Hour)
	cfg.GitCABundle = caPEM
	g := startGit(t, cfg)
	eventually(t, 5*time.Second, "the first load never reached the git server", func() bool { return requests.Load() > 0 })
	before := len(g.logs.lines())
	g.cancel()
	waitLoops(t, 0)
	time.Sleep(100 * time.Millisecond)
	for _, l := range g.logs.lines()[before:] {
		if strings.Contains(l, `"level":"warn"`) || strings.Contains(l, `"level":"error"`) {
			t.Errorf("stopping the server logged %s", l)
		}
	}
}
