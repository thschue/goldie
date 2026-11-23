package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"golang.org/x/crypto/ssh"
)

// #96, process seam: goldie server serves a git Source. On a bare file://
// repository (with GOLDIE_GIT_ALLOW_FILE_URL=true) it starts without waiting
// for the first clone, becomes ready after the first load and serves a new
// commit after one poll. The short poll interval comes from the test
// binary's link-time floor (testPollFloor, server_test.go). Bad git settings
// stop it naming their variables; the token, the key, the known hosts and the
// URL's userinfo never reach its output, /version or /readyz.

// gitCanary marks values that must never reach the output.
const gitCanary = "c4n4ry9f1e2d3c4b5a69788"

// bareRepo is a bare repository in a temp dir, written with go-git's object
// store directly (no worktree, no transport, no git binary).
type bareRepo struct {
	t      *testing.T
	dir    string
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
	return &bareRepo{t: t, dir: dir, repo: repo}
}

func (r *bareRepo) url() string { return "file://" + filepath.ToSlash(r.dir) }

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

// gitArticles are article files for the git Source, one per id.
func gitArticles(ids ...string) map[string]string {
	out := map[string]string{}
	for _, id := range ids {
		out["global/articles/"+id+".md"] = "---\nname: " + id + "\nkeywords: [git]\n---\nBody of " + id + ".\n"
	}
	return out
}

// secretFile writes value to a file and returns its path.
func secretFile(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// sshSecrets are an unencrypted key, known hosts for host with a canary
// comment, and the strings of both that must never be printed.
func sshSecrets(t *testing.T, host string) (key, knownHosts string, material []string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	key = string(pem.EncodeToMemory(block))
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	auth := strings.Fields(string(ssh.MarshalAuthorizedKey(sp)))
	knownHosts = host + " " + auth[0] + " " + auth[1] + " kh-" + gitCanary + "\n"
	material = []string{"PRIVATE KEY", gitCanary, auth[1]}
	for _, l := range strings.Split(key, "\n") {
		if len(l) >= 16 && !strings.HasPrefix(l, "-----") {
			material = append(material, l)
		}
	}
	return key, knownHosts, material
}

// gitEnv is the environment of a server on the git Source at url: the
// content dir unset (the last value wins), the poll interval short.
func gitEnv(url string, more ...string) []string {
	return append([]string{"GOLDIE_CONTENT_DIR=", "GOLDIE_GIT_URL=" + url, "GOLDIE_POLL_INTERVAL=200ms", "GOLDIE_DRAIN_SECONDS=0"}, more...)
}

// authGet is a GET with the test token; it returns the status and body.
func authGet(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test URL
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// waitUntil polls cond until it holds or within passes.
func waitUntil(t *testing.T, p *proc, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("after %v: %s\n%s", within, what, p.logs())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// sourceGitOk reports whether /readyz answers 200, ready, with exactly
// source:git ok.
func sourceGitOk(t *testing.T, admin string) bool {
	t.Helper()
	code, body := httpGet(t, "http://"+admin+"/readyz")
	var got struct {
		Status string `json:"status"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	_ = json.Unmarshal([]byte(body), &got)
	return code == 200 && got.Status == "ready" && len(got.Checks) == 1 &&
		got.Checks[0].Name == "source:git" && got.Checks[0].Status == "ok"
}

// logLevels counts the log lines at each level, from line from on.
func logLevels(t *testing.T, p *proc, from int) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	lines := strings.Split(p.logs(), "\n")
	for _, l := range lines[min(from, len(lines)):] {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) != nil {
			continue
		}
		lvl, _ := m["level"].(string)
		out[lvl] = append(out[lvl], l)
	}
	return out
}

// goldie server on a bare file:// repository becomes ready after the first
// load, serves its content, serves a new commit after one poll, logs the new
// revision's short SHA at info, and stops cleanly with no error logged.
func TestBinaryServesABareFileRepoAndANewCommitAfterOnePoll(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit(gitArticles("git-first"))
	p := startServer(t, gitEnv(repo.url(), "GOLDIE_GIT_ALLOW_FILE_URL=true")...)

	waitUntil(t, p, 10*time.Second, "/readyz never listed source:git ok", func() bool { return sourceGitOk(t, p.admin) })
	if code, body := authGet(t, "http://"+p.app+"/api/v1/articles/git-first"); code != 200 {
		t.Fatalf("GET the first commit's article = %d %s, want 200", code, body)
	}
	if code, _ := authGet(t, "http://"+p.app+"/api/v1/articles/agent-loops"); code != 404 {
		t.Errorf("GET an example dir article = %d, want 404: the git Source alone is served", code)
	}

	second := repo.commit(gitArticles("git-first", "git-second"))
	waitUntil(t, p, 10*time.Second, "the new commit was never served", func() bool {
		code, _ := authGet(t, "http://"+p.app+"/api/v1/articles/git-second")
		return code == 200
	})
	waitUntil(t, p, 5*time.Second, "no info line carries the new revision's short SHA", func() bool {
		for _, l := range logLevels(t, p, 0)["info"] {
			if strings.Contains(l, second[:7]) {
				return true
			}
		}
		return false
	})

	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if err := p.waitExit(t, 10*time.Second); err != nil {
		t.Fatalf("exit: %v, want 0", err)
	}
	levels := logLevels(t, p, 0)
	if len(levels["error"]) != 0 {
		t.Errorf("the server logged errors:\n%s", strings.Join(levels["error"], "\n"))
	}
	for _, l := range levels["warn"] {
		if !strings.Contains(l, "ui not built") {
			t.Errorf("a healthy git Source logged a warning: %s", l)
		}
	}
}

// Bad git Source settings stop goldie server in one run labelled invalid
// configuration, naming the variable to fix and never a secret.
func TestBinaryRefusesBadGitSourceSettings(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit(gitArticles("git-first"))
	key, kh, material := sshSecrets(t, "git.example.invalid")
	token := "ghp_tok" + gitCanary
	for _, tc := range []struct {
		name  string
		names []string
		env   []string
	}{
		{"neither Source", []string{"GOLDIE_CONTENT_DIR", "GOLDIE_GIT_URL"}, []string{"GOLDIE_CONTENT_DIR="}},
		{"file without the flag", []string{"GOLDIE_GIT_URL", "GOLDIE_GIT_ALLOW_FILE_URL"}, gitEnv(repo.url())},
		{"file with the flag false", []string{"GOLDIE_GIT_URL", "GOLDIE_GIT_ALLOW_FILE_URL"}, gitEnv(repo.url(), "GOLDIE_GIT_ALLOW_FILE_URL=false")},
		{"ext remote helper", []string{"GOLDIE_GIT_URL"}, gitEnv("ext::sh -c touch% /tmp/pwned-"+gitCanary, "GOLDIE_GIT_ALLOW_FILE_URL=true")},
		{"http", []string{"GOLDIE_GIT_URL"}, gitEnv("http://git.example.invalid/org/content.git")},
		{"git protocol", []string{"GOLDIE_GIT_URL"}, gitEnv("git://git.example.invalid/org/content.git")},
		{"leading dash URL", []string{"GOLDIE_GIT_URL"}, gitEnv("-oProxyCommand=touch /tmp/"+gitCanary, "GOLDIE_GIT_ALLOW_FILE_URL=true")},
		{"leading dash ref", []string{"GOLDIE_GIT_REF"}, gitEnv("https://git.example.invalid/org/content.git", "GOLDIE_GIT_REF=--upload-pack=touch /tmp/pwned")},
		{"ssh without known hosts", []string{"GOLDIE_GIT_SSH_KNOWN_HOSTS"}, gitEnv("ssh://git@git.example.invalid/org/content.git", "GOLDIE_GIT_SSH_KEY_FILE="+secretFile(t, key))},
		{"token with ssh", []string{"GOLDIE_GIT_TOKEN"}, gitEnv("ssh://git@git.example.invalid/org/content.git",
			"GOLDIE_GIT_SSH_KEY_FILE="+secretFile(t, key), "GOLDIE_GIT_SSH_KNOWN_HOSTS_FILE="+secretFile(t, kh), "GOLDIE_GIT_TOKEN_FILE="+secretFile(t, token+"\n"))},
		{"password in URL", []string{"GOLDIE_GIT_URL"}, gitEnv("https://deploy:pw" + gitCanary + "@git.example.invalid/org/content.git")},
		{"key not a key", []string{"GOLDIE_GIT_SSH_KEY"}, gitEnv("ssh://git@git.example.invalid/org/content.git",
			"GOLDIE_GIT_SSH_KEY=not a key "+gitCanary, "GOLDIE_GIT_SSH_KNOWN_HOSTS_FILE="+secretFile(t, kh))},
	} {
		// A server that wrongly starts must fail this case, not hang the run.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, goldieBinary(t), "server")
		// The documented default interval: refusing a setting needs no poll.
		cmd.Env = append(append([]string{"PATH=" + os.Getenv("PATH"), "GOLDIE_ADDR=127.0.0.1:0", "GOLDIE_ADMIN_ADDR=127.0.0.1:0",
			"GOLDIE_API_TOKEN_FILE=" + tokenFile(t, testToken), "GOLDIE_CONTENT_DIR=" + contentDir(t)}, tc.env...), "GOLDIE_POLL_INTERVAL=1m")
		out, err := cmd.CombinedOutput()
		timedOut := ctx.Err() != nil
		cancel()
		if timedOut {
			t.Errorf("%s: the server started instead of failing", tc.name)
			continue
		}
		if err == nil {
			t.Errorf("%s: exit 0, want a failure: %s", tc.name, out)
			continue
		}
		_, failure, ok := strings.Cut(string(out), "goldie: ")
		if !ok || !strings.HasPrefix(failure, "invalid configuration") {
			t.Errorf("%s: no failure labelled invalid configuration: %s", tc.name, out)
		}
		for _, name := range tc.names {
			if !strings.Contains(failure, name) {
				t.Errorf("%s: the failure does not name %s: %s", tc.name, name, failure)
			}
		}
		for _, secret := range append(material, token, "pw"+gitCanary) {
			if strings.Contains(string(out), secret) {
				t.Errorf("%s: the output contains a secret (%.16q...): %s", tc.name, secret, out)
			}
		}
	}
}

// The git Source's secrets never reach the logs, /version or /readyz while
// its polls fail: not the token (nor its basic-auth form), not the SSH key,
// not the known hosts, not the URL's userinfo.
func TestBinaryNeverShowsGitSecrets(t *testing.T) {
	check := func(t *testing.T, p *proc, secrets []string) {
		t.Helper()
		waitUntil(t, p, 10*time.Second, "a failing poll logged no warn", func() bool { return len(logLevels(t, p, 0)["warn"]) > 0 })
		_, ready := httpGet(t, "http://"+p.admin+"/readyz")
		_, version := httpGet(t, "http://"+p.admin+"/version")
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		_ = p.waitExit(t, 10*time.Second)
		for where, s := range map[string]string{"the logs": p.logs(), "/readyz": ready, "/version": version} {
			for _, secret := range secrets {
				if strings.Contains(s, secret) {
					t.Errorf("%s contains a secret (%.16q...): %s", where, secret, s)
				}
			}
		}
		if strings.Contains(ready, "rror") {
			t.Errorf("/readyz echoes an error: %s", ready)
		}
	}
	t.Run("https token", func(t *testing.T) {
		token := "ghp_tok" + gitCanary
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Echo what the client sent, as a hostile server might.
			http.Error(w, r.Header.Get("Authorization")+" "+r.URL.String(), http.StatusInternalServerError)
		}))
		defer srv.Close()
		ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
		p := startServer(t, gitEnv(srv.URL+"/org/content.git",
			"GOLDIE_GIT_TOKEN_FILE="+secretFile(t, token+"\r\n"), "GOLDIE_GIT_CA_BUNDLE_FILE="+secretFile(t, ca))...)
		check(t, p, []string{gitCanary, token, base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))})
	})
	t.Run("ssh key, known hosts and user", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		_ = ln.Close() // nothing listens: every poll fails at once
		host, port, _ := net.SplitHostPort(addr)
		key, kh, material := sshSecrets(t, "["+host+"]:"+port)
		user := "usr" + gitCanary
		p := startServer(t, gitEnv("ssh://"+user+"@"+addr+"/org/content.git",
			"GOLDIE_GIT_SSH_KEY_FILE="+secretFile(t, key), "GOLDIE_GIT_SSH_KNOWN_HOSTS_FILE="+secretFile(t, kh))...)
		check(t, p, append(material, user))
	})
}

// Startup does not wait for the first clone: the listeners come up while the
// git server stalls the first load, /readyz is not ready and /api/v1 answers
// 503 not_ready. SIGTERM during that load ends the process within the drain
// period, exit 0, and the cancelled load logs no warn and no error.
func TestBinaryStopsDuringTheFirstCloneWithinTheDrain(t *testing.T) {
	var requests atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	p := startServer(t, append(gitEnv(srv.URL+"/org/content.git", "GOLDIE_GIT_CA_BUNDLE_FILE="+secretFile(t, ca)), "GOLDIE_DRAIN_SECONDS=1")...)

	waitUntil(t, p, 10*time.Second, "the first load never reached the git server", func() bool { return requests.Load() > 0 })
	if code, body := httpGet(t, "http://"+p.admin+"/readyz"); code != 503 || !strings.Contains(body, `"source:git"`) {
		t.Errorf("/readyz during the first clone = %d %s, want 503 listing source:git", code, body)
	}
	if code, body := authGet(t, "http://"+p.app+"/api/v1/articles/git-first"); code != 503 || !strings.Contains(body, `"not_ready"`) {
		t.Errorf("GET article during the first clone = %d %s, want 503 not_ready", code, body)
	}

	from := len(strings.Split(p.logs(), "\n"))
	began := time.Now()
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if err := p.waitExit(t, 10*time.Second); err != nil {
		t.Fatalf("exit: %v, want 0", err)
	}
	if d := time.Since(began); d > 5*time.Second {
		t.Errorf("stopping took %v, want it within the 1s drain and the shutdown", d)
	}
	levels := logLevels(t, p, from)
	for _, l := range append(levels["warn"], levels["error"]...) {
		t.Errorf("stopping during the first clone logged %s", l)
	}
}
