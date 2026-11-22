package engine_test

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/engine"
)

const limitMsg = "exceeds the load limits"

func randomText(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n/2)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", b)
}

// TestGitSourceMemoryLimit loads a repository over and under a lowered memory
// budget: content that does not compress fills the pack itself, and content
// that compresses to almost nothing is caught from its declared size before
// it is unpacked.
func TestGitSourceMemoryLimit(t *testing.T) {
	const limit = 512 << 10
	for name, big := range map[string]string{
		"incompressible": randomText(t, 2<<20),
		"compressible":   strings.Repeat("a", 16<<20),
	} {
		t.Run(name, func(t *testing.T) {
			repo := newGitRepo(t)
			repo.commit("main", map[string]string{"global/articles/a.md": article("Ay"), "big.bin": big})
			src := newGit(t, engine.GitOptions{URL: repo.url})
			engine.SetGitLoadLimits(src, 64<<20, limit)
			syncer := engine.NewSyncer(src)
			err := syncer.Poll()
			if err == nil || !strings.Contains(err.Error(), limitMsg) {
				t.Fatalf("err = %v, want the load limit", err)
			}
			assertNoSecret(t, err.Error(), repo.url, repo.dir)
			assertNoSecret(t, syncer.Status().LastError, repo.url, repo.dir)
			if src.Revision() != "" {
				t.Fatal("a load over the limit set a revision")
			}
		})
	}

	repo := newGitRepo(t)
	repo.commit("main", map[string]string{"global/articles/a.md": article("Ay")})
	src := newGit(t, engine.GitOptions{URL: repo.url})
	engine.SetGitLoadLimits(src, 64<<20, limit)
	if _, err := src.Load(); err != nil {
		t.Fatalf("a repository under the limit: %v", err)
	}
}

// TestGitSourceWireLimit serves a ref listing that never ends. Only the wire
// budget can stop the Load before its timeout.
func TestGitSourceWireLimit(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		_, _ = io.WriteString(w, "001e# service=git-upload-pack\n0000")
		hash := strings.Repeat("ab", 20)
		line := hash + " HEAD\x00no-progress\n"
		_, _ = fmt.Fprintf(w, "%04x%s", len(line)+4, line)
		for i := 0; r.Context().Err() == nil; i++ {
			line := fmt.Sprintf("%s refs/heads/b%08d\n", hash, i)
			if _, err := fmt.Fprintf(w, "%04x%s", len(line)+4, line); err != nil {
				return
			}
		}
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()

	src, err := engine.NewGitSourceWithCABundle(engine.GitOptions{URL: srv.URL + "/org/repo.git", Token: "hunter2-token"}, certPEM(srv))
	if err != nil {
		t.Fatal(err)
	}
	engine.SetGitLoadLimits(src, 1<<20, 64<<20)
	engine.SetGitLoadTimeout(src, 30*time.Second)
	start := time.Now()
	_, err = src.Load()
	if err == nil || !strings.Contains(err.Error(), limitMsg) {
		t.Fatalf("err = %v after %s, want the load limit", err, time.Since(start))
	}
	assertNoSecret(t, err.Error(), "hunter2-token", srv.URL, srv.Listener.Addr().String())
}

// TestGitSourceRedirects sends the first request on to another origin and to
// plain http: the token never reaches the other origin, and nothing is sent
// over http.
func TestGitSourceRedirects(t *testing.T) {
	const token = "redirect-hunter2-token"
	var mu sync.Mutex
	var got []string
	record := func(w http.ResponseWriter, r *http.Request) {
		_, pw, _ := r.BasicAuth()
		mu.Lock()
		got = append(got, pw+r.Header.Get("Authorization"))
		mu.Unlock()
		http.NotFound(w, r)
	}
	other := httptest.NewUnstartedServer(http.HandlerFunc(record))
	other.Config.ErrorLog = log.New(io.Discard, "", 0)
	other.StartTLS()
	defer other.Close()
	plain := httptest.NewServer(http.HandlerFunc(record))
	defer plain.Close()

	for _, target := range []string{other.URL, plain.URL} {
		t.Run(target[:strings.Index(target, ":")], func(t *testing.T) {
			mu.Lock()
			got = nil
			mu.Unlock()
			first := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target+r.URL.RequestURI(), http.StatusFound)
			}))
			first.Config.ErrorLog = log.New(io.Discard, "", 0)
			first.StartTLS()
			defer first.Close()

			src, err := engine.NewGitSourceWithCABundle(engine.GitOptions{URL: first.URL + "/org/repo.git", Token: token}, certPEM(first))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := src.Load(); err == nil {
				t.Fatal("loaded through a redirect to a server with no repository")
			} else {
				assertNoSecret(t, err.Error(), token, first.URL, target)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, req := range got {
				if req != "" {
					t.Fatalf("credentials followed the redirect: %q", req)
				}
			}
			if target == other.URL && len(got) == 0 {
				t.Fatal("the redirect was not followed, so stripping was not tested")
			}
			if target == plain.URL && len(got) != 0 {
				t.Fatalf("a redirect downgraded to http: %d requests", len(got))
			}
		})
	}
}

// TestGitOptionsNeverPrintTheToken formats and logs GitOptions every way a
// caller might.
func TestGitOptionsNeverPrintTheToken(t *testing.T) {
	const token = "ghp_printme0123456789"
	opts := engine.GitOptions{URL: "https://user:pw-secret@git.example.invalid/r.git", Ref: "main", Token: token}
	var logs bytes.Buffer
	slog.New(slog.NewTextHandler(&logs, nil)).Info("source", "opts", opts)
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("source", "opts", opts)
	for _, s := range []string{fmt.Sprint(opts), fmt.Sprintf("%v %+v %#v %s", opts, opts, opts, opts), logs.String()} {
		assertNoSecret(t, s, token, "pw-secret")
		if !strings.Contains(s, "git.example.invalid") || !strings.Contains(s, "main") {
			t.Fatalf("redaction dropped the non-secret fields: %q", s)
		}
	}
}
