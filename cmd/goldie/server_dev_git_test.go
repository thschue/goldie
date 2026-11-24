package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// #849: goldie server --dev --git serves a git Source on loopback with a
// random token, the demo content (examples/demo) of the goldie repository by
// default, at the ref v<version> of a release build (main for a build without
// one). GOLDIE_GIT_URL, GOLDIE_GIT_REF and GOLDIE_GIT_PATH serve another
// repository. A failed first clone keeps the server running, not ready.
//
// Seam for the default repository URL: the string variable devGitURL in
// internal/app, set at link time like platform.pollIntervalFloor
// (-X github.com/thschue/goldie/internal/app.devGitURL=<url>). Unset, it is
// https://github.com/thschue/goldie.git. The tests link it to a file://
// repository and set GOLDIE_GIT_ALLOW_FILE_URL=true.

const devGitURLSeam = "github.com/thschue/goldie/internal/app.devGitURL"

// buildDevGitBinary builds goldie linked with version (empty keeps the "dev"
// build) and with url as the default --git repository.
func buildDevGitBinary(t *testing.T, version, url string) string {
	t.Helper()
	ld := "-X " + devGitURLSeam + "=" + url + " " +
		"-X github.com/thschue/goldie/internal/platform.pollIntervalFloor=" + testPollFloor
	if version != "" {
		ld += " -X github.com/thschue/goldie/internal/platform.Version=" + version
	}
	bin := filepath.Join(t.TempDir(), "goldie")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "go", "build", "-ldflags", ld, "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// demoFiles is a demo tree under examples/demo, plus an article outside it.
func demoFiles(id string) map[string]string {
	out := map[string]string{"global/articles/outside.md": "---\nname: outside\nkeywords: [git]\n---\nOutside the path.\n"}
	for name, content := range gitArticles(id) {
		out["examples/demo/"+name] = content
	}
	return out
}

// releaseRepo is a bare repository whose tag v1.2.3 holds the article
// "demo-tagged" and whose main holds "demo-main", both under examples/demo.
func releaseRepo(t *testing.T) *bareRepo {
	t.Helper()
	repo := newBareRepo(t)
	tagged := repo.commit(demoFiles("demo-tagged"))
	if err := repo.repo.Storer.SetReference(plumbing.NewHashReference("refs/tags/v1.2.3", plumbing.NewHash(tagged))); err != nil {
		t.Fatal(err)
	}
	repo.commit(demoFiles("demo-main"))
	return repo
}

// gitDevEnv is the environment of a --dev --git run over file:// repositories
// with a short poll.
func gitDevEnv(extra ...string) []string {
	return devEnv(append([]string{"GOLDIE_GIT_ALLOW_FILE_URL=true", "GOLDIE_POLL_INTERVAL=200ms"}, extra...)...)
}

// waitArticle waits until the article id is served by the --dev server p.
func waitArticle(t *testing.T, p *devProc, id string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for devStatus(t, p.addr, "/api/v1/articles/"+id, p.token) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatalf("article %s was never served\n%s", id, p.dump())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// noticeLines are the stderr lines that are not the startup report and
// contain all of subs.
func noticeLines(p *devProc, subs ...string) []string {
	_, errs := p.lines()
	var got []string
next:
	for _, l := range errs {
		if strings.Contains(l, `"msg":"config"`) {
			continue
		}
		for _, s := range subs {
			if !strings.Contains(l, s) {
				continue next
			}
		}
		got = append(got, l)
	}
	return got
}

// Criterion 1: --dev --git serves examples/demo from the default repository
// as a git Source, on loopback with a random token (the export line), and
// says once which repository and path.
func TestServerDevGitServesTheDemoFromTheDefaultRepository(t *testing.T) {
	repo := releaseRepo(t)
	bin := buildDevGitBinary(t, "", repo.url())
	p := startDevBin(t, bin, t.TempDir(), gitDevEnv(), "--git")
	waitArticle(t, p, "demo-main")
	if got := devStatus(t, p.addr, "/api/v1/articles/outside", p.token); got != http.StatusNotFound {
		t.Errorf("GET an article outside examples/demo = %d, want 404\n%s", got, p.dump())
	}
	if got := devStatus(t, p.addr, "/api/v1/articles/retries", p.token); got != http.StatusNotFound {
		t.Errorf("GET a built-in example = %d, want 404: the git Source alone is served", got)
	}
	if lines := noticeLines(p, repo.dir, "examples/demo"); len(lines) != 1 {
		t.Errorf("stderr has %d lines naming the repository and examples/demo, want exactly 1: %q\n%s", len(lines), lines, p.dump())
	}
	p.stop(t)
	assertTokenOnlyInExportLine(t, p)
}

// Criterion 2: the ref is the release tag v<version>; a build without a
// release version serves main.
func TestServerDevGitServesTheReleaseTagOfTheBinary(t *testing.T) {
	repo := releaseRepo(t)
	t.Run("release build", func(t *testing.T) {
		p := startDevBin(t, buildDevGitBinary(t, "1.2.3", repo.url()), t.TempDir(), gitDevEnv(), "--git")
		waitArticle(t, p, "demo-tagged")
		if got := devStatus(t, p.addr, "/api/v1/articles/demo-main", p.token); got != http.StatusNotFound {
			t.Errorf("GET the article of main = %d, want 404: the content must match the binary's tag", got)
		}
		p.stop(t)
	})
	t.Run("build without a release version", func(t *testing.T) {
		p := startDevBin(t, buildDevGitBinary(t, "", repo.url()), t.TempDir(), gitDevEnv(), "--git")
		waitArticle(t, p, "demo-main")
		if got := devStatus(t, p.addr, "/api/v1/articles/demo-tagged", p.token); got != http.StatusNotFound {
			t.Errorf("GET the article of the tag = %d, want 404", got)
		}
		p.stop(t)
	})
}

// Criterion 3: GOLDIE_GIT_URL (with GOLDIE_GIT_REF and GOLDIE_GIT_PATH)
// serves that repository instead of the default one.
func TestServerDevGitServesTheRepositoryOfGoldieGitURL(t *testing.T) {
	repo := newBareRepo(t)
	main := repo.commit(map[string]string{"docs/content/global/articles/own-main.md": gitArticles("own-main")["global/articles/own-main.md"]})
	repo.commit(map[string]string{"docs/content/global/articles/own-release.md": gitArticles("own-release")["global/articles/own-release.md"]})
	if err := repo.repo.Storer.SetReference(plumbing.NewHashReference("refs/heads/release", plumbing.NewHash(main))); err != nil {
		t.Fatal(err)
	}
	// The default repository is unreachable: it must not be used.
	p := startDevBin(t, buildDevGitBinary(t, "", "file://"+filepath.Join(t.TempDir(), "no-such-repo")), t.TempDir(),
		gitDevEnv("GOLDIE_GIT_URL="+repo.url(), "GOLDIE_GIT_REF=release", "GOLDIE_GIT_PATH=docs/content"), "--git")
	waitArticle(t, p, "own-main")
	if got := devStatus(t, p.addr, "/api/v1/articles/own-release", p.token); got != http.StatusNotFound {
		t.Errorf("GET an article of another ref = %d, want 404", got)
	}
	p.stop(t)
}

// Criterion 4: a failed first clone keeps the server running and not ready,
// and says why in one line on stderr, with no credential and the hint to run
// --dev without --git.
func TestServerDevGitFailedFirstCloneKeepsRunningNotReady(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens: the clone fails at once
	token := "ghp_tok" + gitCanary
	env := gitDevEnv("GOLDIE_GIT_URL=https://"+addr+"/org/content.git", "GOLDIE_GIT_TOKEN_FILE="+secretFile(t, token+"\n"))
	p := startDev(t, env, "--git")
	deadline := time.Now().Add(15 * time.Second)
	for len(noticeLines(p, "without --git")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no stderr line carries the hint to run without --git\n%s", p.dump())
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(time.Second) // several polls: the hint is said once, not per poll
	if lines := noticeLines(p, "without --git"); len(lines) != 1 {
		t.Errorf("stderr has %d lines with the hint, want exactly 1: %q", len(lines), lines)
	} else if !strings.Contains(lines[0], "goldie server --dev") {
		t.Errorf("the hint does not name goldie server --dev: %q", lines[0])
	}
	if code, body := httpGet(t, "http://"+p.admin+"/readyz"); code != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d %s, want 503 while the first clone has failed", code, body)
	}
	if got := devStatus(t, p.addr, "/api/v1/articles/anything", p.token); got != http.StatusServiceUnavailable {
		t.Errorf("GET /api/v1 = %d, want 503 not_ready", got)
	}
	select {
	case err := <-p.exited:
		t.Fatalf("the server exited after a failed first clone: %v\n%s", err, p.dump())
	default:
	}
	p.stop(t)
	out, errs := p.lines()
	all := strings.Join(out, "\n") + strings.Join(errs, "\n")
	for _, s := range []string{token, gitCanary} {
		if strings.Contains(all, s) {
			t.Errorf("the output contains the credential %.12q...", s)
		}
	}
}

// Criterion 5: --git with a directory argument, or without --dev, stops with
// an error naming the conflict, before serving anything.
func TestServerGitConflictsStopWithAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"with a directory", []string{"server", "--dev", "--git", t.TempDir()}, []string{"--git", "directory"}},
		{"without --dev", []string{"server", "--git"}, []string{"--git", "--dev"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runDevToExit(t, gitDevEnv(), tc.args...)
			if err == nil {
				t.Fatalf("exit 0, want non-zero\n%s", stdout+stderr)
			}
			for _, w := range tc.want {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr does not name %q: %s", w, stderr)
				}
			}
			if strings.Contains(stdout+stderr, "GOLDIE_TOKEN=") {
				t.Errorf("an export line was printed:\n%s", stdout+stderr)
			}
		})
	}
}

// Criterion 6: --dev without --git behaves as today, even with GOLDIE_GIT_URL
// set: the directory (here none, so the built-in examples) is served.
func TestServerDevWithoutGitIgnoresGoldieGitURL(t *testing.T) {
	repo := releaseRepo(t)
	p := startDev(t, gitDevEnv("GOLDIE_GIT_URL="+repo.url()), t.TempDir())
	if got := devStatus(t, p.addr, "/api/v1/articles/retries", p.token); got != http.StatusOK {
		t.Errorf("GET a built-in example = %d, want 200\n%s", got, p.dump())
	}
	if got := devStatus(t, p.addr, "/api/v1/articles/demo-main", p.token); got != http.StatusNotFound {
		t.Errorf("GET an article of the git repository = %d, want 404: --git was not given", got)
	}
	p.stop(t)
}

// Criterion 7: the README's step 2, the server help and the CLI reference
// describe --git; the platform contract's PC-04 names it.
func TestServerDevGitIsDocumented(t *testing.T) {
	read := func(parts ...string) string {
		b, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	readme := read("README.md")
	_, rest, ok := strings.Cut(readme, "**2. Start a server**")
	if !ok {
		t.Fatal("README has no step 2")
	}
	step, _, _ := strings.Cut(rest, "**3. ")
	for _, want := range []string{"goldie server --dev --git", "GOLDIE_GIT_URL"} {
		if !strings.Contains(step, want) {
			t.Errorf("README step 2 does not show %q", want)
		}
	}
	if !strings.Contains(strings.ReplaceAll(step, "--dev --git", ""), "goldie server --dev") {
		t.Error("README step 2 no longer keeps plain goldie server --dev as the offline alternative")
	}
	if !strings.Contains(read("docs", "reference", "cli.md"), "--git") {
		t.Error("docs/reference/cli.md does not describe --git")
	}
	pc04, _, _ := strings.Cut(read("docs", "platform-contract.md"), "| PC-05")
	if !strings.Contains(pc04, "--git") {
		t.Error("PC-04 does not name --git")
	}
	stdout, _, _ := runHelp(t, "server", "--help")
	if !strings.Contains(stdout, "--git") {
		t.Errorf("goldie server --help does not describe --git:\n%s", stdout)
	}
}
