package engine_test

import (
	"bytes"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"

	"github.com/thschue/goldie/internal/engine"
)

// A git Source is a Source that names the revision it read.
var _ interface {
	engine.Source
	engine.Revisioner
} = (*engine.GitSource)(nil)

// gitRepo is a repository in a temp dir, served to the git Source over
// file://. Commits are written with go-git directly, never through a
// transport, so the only git client in play is the one under test.
type gitRepo struct {
	t    *testing.T
	dir  string
	url  string
	repo *git.Repository
	n    int
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Helper()
	// No git binary: with an empty PATH, a transport that exec'd
	// git-upload-pack would fail.
	t.Setenv("PATH", "")
	dir := t.TempDir()
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &gitRepo{t: t, dir: dir, url: "file://" + filepath.ToSlash(filepath.Join(dir, ".git")), repo: repo}
}

// commit replaces the tree with files, commits it on branch and returns the
// commit SHA. A value "symlink:<target>" writes a symlink. HEAD stays on main,
// as a remote's HEAD does, whichever branch was written.
func (r *gitRepo) commit(branch string, files map[string]string) string {
	r.t.Helper()
	wt, err := r.repo.Worktree()
	if err != nil {
		r.t.Fatal(err)
	}
	ref := plumbing.NewBranchReferenceName(branch)
	if r.n > 0 {
		_, missing := r.repo.Reference(ref, false)
		if err := wt.Checkout(&git.CheckoutOptions{Branch: ref, Create: missing != nil, Force: true}); err != nil {
			r.t.Fatal(err)
		}
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
	head := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))
	if err := r.repo.Storer.SetReference(head); err != nil {
		r.t.Fatal(err)
	}
	return h.String()
}

// tag creates an annotated tag on sha.
func (r *gitRepo) tag(name, sha string) {
	r.t.Helper()
	sig := &object.Signature{Name: "test", Email: "test@example.invalid", When: time.Unix(1700000000, 0)}
	if _, err := r.repo.CreateTag(name, plumbing.NewHash(sha), &git.CreateTagOptions{Tagger: sig, Message: name}); err != nil {
		r.t.Fatal(err)
	}
}

// hideObjects moves the object database away until the returned func puts it
// back. The refs stay readable, so a client can still list them, but any
// fetch fails.
func (r *gitRepo) hideObjects() (restore func()) {
	r.t.Helper()
	objects := filepath.Join(r.dir, ".git", "objects")
	if err := os.Rename(objects, objects+".hidden"); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Mkdir(objects, 0o755); err != nil {
		r.t.Fatal(err)
	}
	return func() {
		r.t.Helper()
		if err := os.RemoveAll(objects); err != nil {
			r.t.Fatal(err)
		}
		if err := os.Rename(objects+".hidden", objects); err != nil {
			r.t.Fatal(err)
		}
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
	repo := newGitRepo(t)
	sha := repo.commit("main", map[string]string{"global/articles/deploy.md": article("Deploy", "deploy")})

	var src engine.Source = newGit(t, engine.GitOptions{URL: repo.url})
	snap, err := src.Load(t.Context())
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
	snap, err = src.Load(t.Context())
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

func TestGitSourceRevisionEmptyBeforeFirstLoad(t *testing.T) {
	repo := newGitRepo(t)
	repo.commit("main", map[string]string{"global/articles/a.md": article("Ay")})
	src := newGit(t, engine.GitOptions{URL: repo.url})
	if got := src.Revision(); got != "" {
		t.Fatalf("revision before any Load = %q, want empty", got)
	}
	// A failed Load names no revision either.
	if _, err := newGit(t, engine.GitOptions{URL: repo.url, Ref: "nope"}).Load(t.Context()); err == nil {
		t.Fatal("unknown ref loaded")
	}
}

func TestGitSourceRef(t *testing.T) {
	repo := newGitRepo(t)
	mainSHA := repo.commit("main", map[string]string{"global/articles/a.md": article("On main")})
	devSHA := repo.commit("dev", map[string]string{"global/articles/a.md": article("On dev")})
	repo.tag("v1", mainSHA)

	for _, tc := range []struct{ ref, name, sha string }{
		{"", "On main", mainSHA},
		{"main", "On main", mainSHA},
		{"dev", "On dev", devSHA},
		{"refs/heads/dev", "On dev", devSHA},
		{"v1", "On main", mainSHA},           // an annotated tag resolves to its commit
		{"refs/tags/v1", "On main", mainSHA}, // so does its full name
	} {
		t.Run(tc.ref, func(t *testing.T) {
			src := newGit(t, engine.GitOptions{URL: repo.url, Ref: tc.ref})
			snap, err := src.Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if a, _ := snap.Article("a"); a.Name != tc.name || src.Revision() != tc.sha {
				t.Fatalf("got %q at %s, want %q at %s", a.Name, src.Revision(), tc.name, tc.sha)
			}
		})
	}

	_, err := newGit(t, engine.GitOptions{URL: repo.url, Ref: "nope"}).Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("unknown ref: err = %v", err)
	}
	assertNoSecret(t, err.Error(), repo.url, repo.dir)
}

func TestGitSourceUnchangedRefReturnsCachedSnapshot(t *testing.T) {
	repo := newGitRepo(t)
	sha := repo.commit("main", map[string]string{"global/articles/a.md": article("Ay")})
	src := newGit(t, engine.GitOptions{URL: repo.url, Ref: "main"})
	first, err := src.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	// A commit on another branch does not move main.
	repo.commit("dev", map[string]string{"global/articles/b.md": article("Bee")})
	// With the objects gone a clone cannot succeed, so a Load that succeeds
	// only listed the ref and served the cache.
	restore := repo.hideObjects()
	second, err := src.Load(t.Context())
	if err != nil {
		t.Fatalf("Load with the ref unchanged fetched again: %v", err)
	}
	if second != first {
		t.Fatal("Load with the ref unchanged built a new Snapshot")
	}
	if src.Revision() != sha {
		t.Fatalf("revision = %s, want %s", src.Revision(), sha)
	}
	restore()

	// Once the ref moves, the next Load reads the new commit.
	sha2 := repo.commit("main", map[string]string{"global/articles/c.md": article("Sea")})
	third, err := src.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if third == first || src.Revision() != sha2 {
		t.Fatalf("moved ref served the cache: revision %s, want %s", src.Revision(), sha2)
	}
	if _, ok := third.Article("c"); !ok {
		t.Fatal("article from the new commit missing")
	}
}

func TestGitSourceClonesIntoMemory(t *testing.T) {
	repo := newGitRepo(t)
	repo.commit("main", map[string]string{"global/articles/a.md": article("Ay")})
	src := newGit(t, engine.GitOptions{URL: repo.url})

	// Every place a clone could land on disk starts empty and stays empty.
	dirs := map[string]string{"TMPDIR": t.TempDir(), "HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir(), "XDG_CACHE_HOME": t.TempDir()}
	for k, v := range dirs {
		t.Setenv(k, v)
	}
	cwd := t.TempDir()
	t.Chdir(cwd)
	dirs["working directory"] = cwd

	if _, err := src.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	for name, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("Load wrote %s to %s", entries[0].Name(), name)
		}
	}
}

func TestGitSourceLoadsProjects(t *testing.T) {
	repo := newGitRepo(t)
	repo.commit("main", projectContent())
	snap, err := newGit(t, engine.GitOptions{URL: repo.url}).Load(t.Context())
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
	repo := newGitRepo(t)
	repo.commit("main", map[string]string{"global/articles/ok.md": article("Ok")})
	src := newGit(t, engine.GitOptions{URL: repo.url})
	syncer := engine.NewSyncer(src)
	if err := syncer.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	good := syncer.Status().Revision

	repo.commit("main", map[string]string{
		"global/articles/ok.md":      article("Ok"),
		"global/articles/sub/bad.md": "no frontmatter",
	})
	err := syncer.Poll(t.Context())
	if err == nil || !strings.HasPrefix(err.Error(), "global/articles/sub/bad.md: ") {
		t.Fatalf("err = %v, want it to name global/articles/sub/bad.md", err)
	}
	assertNoSecret(t, err.Error(), repo.url, repo.dir)
	// The last good Snapshot keeps serving.
	if _, ok := syncer.Current().Article("ok"); !ok || syncer.Status().Revision != good {
		t.Fatal("last good Snapshot was replaced")
	}
}

func TestGitSourceSymlinks(t *testing.T) {
	repo := newGitRepo(t)
	repo.commit("main", map[string]string{
		"global/articles/a.md":    article("Ay"),
		"global/articles/link.md": "symlink:/etc/passwd",
		"global/articles/dir":     "symlink:../../projects",
	})
	snap, err := newGit(t, engine.GitOptions{URL: repo.url}).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n := len(snap.Articles()); n != 1 {
		t.Fatalf("articles = %d, want 1: symlinks must not be followed", n)
	}

	for _, link := range []string{"global", "global/articles", "projects/github.com/acme/shop/articles"} {
		t.Run(link, func(t *testing.T) {
			repo := newGitRepo(t)
			repo.commit("main", map[string]string{
				"elsewhere/articles/o.md": article("Outside"),
				"elsewhere/o.md":          article("Outside"),
				link:                      "symlink:/elsewhere",
			})
			snap, err := newGit(t, engine.GitOptions{URL: repo.url}).Load(t.Context())
			if err == nil {
				t.Fatalf("loaded %d articles through a symlink", len(snap.Articles()))
			}
		})
	}
}

func TestGitSourceAcceptsHTTPSAndFile(t *testing.T) {
	for _, opts := range []engine.GitOptions{
		{URL: "https://example.invalid/org/repo.git"},
		{URL: "https://example.invalid/org/repo.git", Token: "hunter2", Ref: "v1"},
		{URL: "https://example.invalid:8443/org/repo.git", Token: "hunter2", Ref: "refs/heads/main"},
		{URL: "file:///srv/git/repo.git"},
	} {
		if _, err := engine.NewGitSource(opts); err != nil {
			t.Errorf("%s: %v", opts.URL, err)
		}
	}
}

// TestGitSourceRejectsBadOptions builds each case twice, with a different
// host and secret, and requires the same fixed error for both: an error that
// changed with them would be echoing them.
func TestGitSourceRejectsBadOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts func(host, secret string) engine.GitOptions
	}{
		{"no url", func(_, _ string) engine.GitOptions { return engine.GitOptions{} }},
		{"blank url", func(_, _ string) engine.GitOptions { return engine.GitOptions{URL: "  "} }},
		{"plain http", func(h, _ string) engine.GitOptions { return engine.GitOptions{URL: "http://" + h + "/r.git"} }},
		{"plain http, upper case", func(h, _ string) engine.GitOptions { return engine.GitOptions{URL: "HTTP://" + h + "/r.git"} }},
		{"token over http", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "http://" + h + "/r.git", Token: s}
		}},
		{"git protocol", func(h, _ string) engine.GitOptions { return engine.GitOptions{URL: "git://" + h + "/r.git"} }},
		{"token over git protocol", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "git://" + h + "/r.git", Token: s}
		}},
		{"password in https url", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "https://user:" + s + "@" + h + "/r.git"}
		}},
		{"token as https url user", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "https://" + s + "@" + h + "/r.git"}
		}},
		{"password in https url with a token", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "https://user:" + s + "@" + h + "/r.git", Token: s}
		}},
		{"password in file url", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "file://user:" + s + "@" + h + "/srv/r.git"}
		}},
		{"token over ssh", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "ssh://git@" + h + "/r.git", Token: s}
		}},
		{"token over file", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "file:///srv/" + h + "/r.git", Token: s}
		}},
		{"ext transport", func(h, _ string) engine.GitOptions {
			return engine.GitOptions{URL: "ext::sh -c touch% /tmp/" + h}
		}},
		{"unparseable url", func(h, _ string) engine.GitOptions { return engine.GitOptions{URL: "https://[" + h} }},
		{"bad ref", func(h, _ string) engine.GitOptions {
			return engine.GitOptions{URL: "https://" + h + "/r.git", Ref: "a..b"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := engine.NewGitSource(tc.opts("git.example.invalid", "hunter2"))
			if err == nil {
				t.Fatal("accepted")
			}
			assertNoSecret(t, err.Error(), "hunter2", "git.example.invalid", "/srv/")
			_, other := engine.NewGitSource(tc.opts("other.example.invalid", "swordfish"))
			if other == nil || other.Error() != err.Error() {
				t.Fatalf("error is not a fixed message: %q, then %v", err, other)
			}
		})
	}
}

// TestGitSourceRejectsQueryAndFragment gives NewGitSource URLs that carry a
// query string or a fragment, where a token is easily pasted, on every scheme
// it accepts. Each is refused with one fixed error, built twice with a
// different host and secret, so it echoes neither.
func TestGitSourceRejectsQueryAndFragment(t *testing.T) {
	const want = "git source: URL must not carry a query or a fragment"
	key := newSSHKey(t)
	hostKey := newSSHKey(t)
	for _, tc := range []struct {
		name string
		opts func(host, secret string) engine.GitOptions
	}{
		{"token in https query", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "https://" + h + "/r.git?private_token=" + s}
		}},
		{"token in https query with a Token", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "https://" + h + "/r.git?access_token=" + s, Token: "tok-" + s}
		}},
		{"https fragment", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "https://" + h + "/r.git#" + s}
		}},
		{"https query and fragment", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "https://" + h + "/r.git?a=" + s + "#" + s}
		}},
		{"https empty query", func(h, _ string) engine.GitOptions { return engine.GitOptions{URL: "https://" + h + "/r.git?"} }},
		{"https empty fragment", func(h, _ string) engine.GitOptions { return engine.GitOptions{URL: "https://" + h + "/r.git#"} }},
		{"https query on the host", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "https://" + h + "?" + s}
		}},
		{"upper-case https scheme", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "HTTPS://" + h + "/r.git?t=" + s}
		}},
		{"ssh query", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "ssh://git@" + h + "/r.git?t=" + s, SSHKey: key.pem, KnownHosts: h + " " + authorizedKey(hostKey.pub)}
		}},
		{"ssh fragment", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "ssh://git@" + h + "/r.git#" + s, SSHKey: key.pem, KnownHosts: h + " " + authorizedKey(hostKey.pub)}
		}},
		{"file query", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "file:///srv/" + h + "/r.git?t=" + s}
		}},
		{"file fragment", func(h, s string) engine.GitOptions {
			return engine.GitOptions{URL: "file:///srv/" + h + "/r.git#" + s}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := engine.NewGitSource(tc.opts("git.example.invalid", "hunter2-query-secret"))
			if err == nil {
				t.Fatal("accepted")
			}
			if err.Error() != want {
				t.Fatalf("err = %q, want %q", err, want)
			}
			assertNoSecret(t, err.Error(), "hunter2-query-secret", "git.example.invalid", "/srv/", "?", "#")
			_, other := engine.NewGitSource(tc.opts("other.example.invalid", "swordfish-query-secret"))
			if other == nil || other.Error() != err.Error() {
				t.Fatalf("error is not a fixed message: %q, then %v", err, other)
			}
		})
	}
}

func TestGitSourceLoadTimeoutIsTwoMinutes(t *testing.T) {
	for _, url := range []string{"https://example.invalid/r.git", "file:///srv/r.git"} {
		if got := engine.GitLoadTimeout(newGit(t, engine.GitOptions{URL: url})); got != 2*time.Minute {
			t.Errorf("%s: load timeout = %s, want 2m", url, got)
		}
	}
}

// TestGitSourceStalledServerTimesOut runs a Load against a server that never
// answers, with the bound shortened, and expects a fixed timeout error.
func TestGitSourceStalledServerTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	defer close(release)

	src, err := engine.NewGitSource(engine.GitOptions{URL: srv.URL + "/org/repo.git", Token: "hunter2-token", CABundle: string(certPEM(srv))})
	if err != nil {
		t.Fatal(err)
	}
	engine.SetGitLoadTimeout(src, 300*time.Millisecond)
	start := time.Now()
	_, err = src.Load(t.Context())
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("Load took %s with a 300ms bound", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	assertNoSecret(t, err.Error(), "hunter2-token", srv.URL, srv.Listener.Addr().String())
}

// TestGitSourceShallowCloneOverHTTPS serves a ref advertisement over smart
// HTTP and records what the client asks for: one commit, not the history.
func TestGitSourceShallowCloneOverHTTPS(t *testing.T) {
	head := plumbing.NewHash("0123456789abcdef0123456789abcdef01234567")
	var mu sync.Mutex
	var uploadPack [][]byte
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/info/refs"):
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
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			uploadPack = append(uploadPack, body)
			mu.Unlock()
			http.Error(w, "test server sends no pack", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()

	src, err := engine.NewGitSource(engine.GitOptions{URL: srv.URL + "/org/repo.git", CABundle: string(certPEM(srv))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Load(t.Context()); err == nil {
		t.Fatal("loaded from a server that sends no pack")
	} else {
		assertNoSecret(t, err.Error(), srv.URL, srv.Listener.Addr().String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(uploadPack) == 0 {
		t.Fatal("the client never asked for the commit")
	}
	req := uploadPack[len(uploadPack)-1]
	if !bytes.Contains(req, []byte("want "+head.String())) {
		t.Fatalf("upload-pack request does not want the ref's commit: %q", req)
	}
	if !bytes.Contains(req, []byte("deepen 1\n")) {
		t.Fatalf("upload-pack request is not depth 1: %q", req)
	}
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
	addr := srv.Listener.Addr().String()

	// The server's certificate is not trusted: the load fails before any
	// request, so the token never leaves.
	_, err := newGit(t, engine.GitOptions{URL: url, Token: token}).Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("err = %v, want a certificate failure", err)
	}
	mu.Lock()
	reached := len(passwords)
	mu.Unlock()
	if reached != 0 {
		t.Fatal("a request reached a server with an untrusted certificate")
	}
	assertNoSecret(t, err.Error(), token, srv.URL, addr)

	// Trusted: the token is sent as the basic-auth password.
	src, err := engine.NewGitSource(engine.GitOptions{URL: url, Token: token, CABundle: string(certPEM(srv))})
	if err != nil {
		t.Fatal(err)
	}
	_, err = src.Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want repository not found", err)
	}
	mu.Lock()
	sent := len(passwords) > 0 && passwords[len(passwords)-1] == token
	mu.Unlock()
	if !sent {
		t.Fatal("the token was not sent")
	}
	assertNoSecret(t, err.Error(), token, srv.URL, addr)

	// A wrong token is an authentication failure that does not echo it.
	src, err = engine.NewGitSource(engine.GitOptions{URL: url, Token: "wrong-hunter2", CABundle: string(certPEM(srv))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = src.Load(t.Context()); err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("err = %v, want an authentication failure", err)
	}
	assertNoSecret(t, err.Error(), "wrong-hunter2", srv.URL, addr)

	// No token, no credentials: nothing is sent as a password.
	mu.Lock()
	passwords = nil
	mu.Unlock()
	src, err = engine.NewGitSource(engine.GitOptions{URL: url, CABundle: string(certPEM(srv))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = src.Load(t.Context()); err == nil {
		t.Fatal("loaded without the token")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, pw := range passwords {
		if pw != "" {
			t.Fatalf("a password was sent without a Token: %q", pw)
		}
	}
}

// TestGitSourceNeverLogsOrLeaksCredentials runs failing loads while capturing
// the standard and slog loggers, and checks that neither the errors, the sync
// status nor any log line carries the token, the address or a host path.
func TestGitSourceNeverLogsOrLeaksCredentials(t *testing.T) {
	const token = "glpat-supersecrettokenvalue123456"
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
	missing := filepath.ToSlash(filepath.Join(t.TempDir(), "secret-repo-path", "repo.git"))

	for _, opts := range []engine.GitOptions{
		{URL: "https://" + closedAddr + "/org/repo.git", Token: token},
		{URL: "file://" + missing},
	} {
		src := newGit(t, opts)
		syncer := engine.NewSyncer(src)
		err := syncer.Poll(t.Context())
		if err == nil {
			t.Fatalf("%s: load succeeded", opts.URL)
		}
		assertNoSecret(t, err.Error(), token, closedAddr, missing, "secret-repo-path")
		assertNoSecret(t, syncer.Status().LastError, token, closedAddr, missing, "secret-repo-path")
	}
	assertNoSecret(t, logs.String(), token, closedAddr, missing, "secret-repo-path")
}

// FuzzNewGitSource checks that validating a hostile URL never panics and that
// no error echoes the URL or the token.
func FuzzNewGitSource(f *testing.F) {
	for _, u := range []string{
		"https://example.invalid/r.git", "https://u:p@example.invalid/r", "http://h/r", "git://h/r",
		"file:///tmp/r", "ssh://git@h:22/r", "git@h:org/r.git", "::", "https://[::1", "\x00", "a@b@c:d",
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

func certPEM(srv *httptest.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}

func assertNoSecret(t *testing.T, s string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(s, secret) {
			t.Fatalf("output contains a secret %.12q...: %q", secret, s)
		}
	}
}
