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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/revlist"
	"github.com/go-git/go-git/v5/storage/memory"

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
			err := syncer.Poll(t.Context())
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
	if _, err := src.Load(t.Context()); err != nil {
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

	src, err := engine.NewGitSource(engine.GitOptions{URL: srv.URL + "/org/repo.git", Token: "hunter2-token", CABundle: string(certPEM(srv))})
	if err != nil {
		t.Fatal(err)
	}
	engine.SetGitLoadLimits(src, 1<<20, 64<<20)
	engine.SetGitLoadTimeout(src, 30*time.Second)
	start := time.Now()
	_, err = src.Load(t.Context())
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

			src, err := engine.NewGitSource(engine.GitOptions{URL: first.URL + "/org/repo.git", Token: token, CABundle: string(certPEM(first))})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := src.Load(t.Context()); err == nil {
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

// slowPackServer serves one commit over smart HTTPS, in a pack that also holds
// n tiny loose blobs: the pack arrives at once and takes long to unpack. It
// returns the server, the commit and when the server last finished writing
// the pack.
func slowPackServer(t *testing.T, n int) (*httptest.Server, plumbing.Hash, func() time.Time) {
	t.Helper()
	repo := newGitRepo(t)
	head := plumbing.NewHash(repo.commit("main", map[string]string{"global/articles/a.md": article("Ay")}))
	hashes, err := revlist.Objects(repo.repo.Storer, []plumbing.Hash{head}, nil)
	if err != nil {
		t.Fatal(err)
	}
	objs := memory.NewStorage()
	for _, h := range hashes {
		o, err := repo.repo.Storer.EncodedObject(plumbing.AnyObject, h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := objs.SetEncodedObject(o); err != nil {
			t.Fatal(err)
		}
	}
	for i := range n {
		o := objs.NewEncodedObject()
		o.SetType(plumbing.BlobObject)
		w, err := o.Writer()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(strconv.Itoa(i))); err != nil {
			t.Fatal(err)
		}
		h, err := objs.SetEncodedObject(o)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	var pack bytes.Buffer
	if _, err := packfile.NewEncoder(&pack, objs, false).Encode(hashes, 0); err != nil {
		t.Fatal(err)
	}

	var sent atomic.Int64
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
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
			shallow := "shallow " + head.String() + "\n"
			_, _ = fmt.Fprintf(w, "%04x%s0000", len(shallow)+4, shallow)
			_, _ = io.WriteString(w, "0008NAK\n")
			_, _ = w.Write(pack.Bytes())
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			sent.Store(time.Now().UnixNano())
		default:
			http.NotFound(w, r)
		}
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, head, func() time.Time { return time.Unix(0, sent.Load()) }
}

// TestGitSourceUnpackingStopsAtTheDeadline serves a pack that arrives at once
// and takes most of the Load to unpack. Timed with room to spare, it loads;
// with the deadline a quarter of the way into unpacking, the Load fails with
// the fixed timeout error soon after the deadline instead of unpacking to the
// end, caches nothing, and leaves the load lock free for the next Load.
func TestGitSourceUnpackingStopsAtTheDeadline(t *testing.T) {
	srv, head, sent := slowPackServer(t, 200000)
	newSrc := func() *engine.GitSource {
		src, err := engine.NewGitSource(engine.GitOptions{URL: srv.URL + "/org/repo.git", CABundle: string(certPEM(srv))})
		if err != nil {
			t.Fatal(err)
		}
		// The memory limit is not under test here; the pack must fit it.
		engine.SetGitLoadLimits(src, 64<<20, 1<<30)
		return src
	}

	// The pack is valid, and unpacking it is most of the Load.
	src := newSrc()
	start := time.Now()
	if _, err := src.Load(t.Context()); err != nil {
		t.Fatalf("with room to spare: %v", err)
	}
	total := time.Since(start)
	transfer := sent().Sub(start)
	unpack := total - transfer
	if unpack < 200*time.Millisecond || unpack < 4*transfer {
		t.Fatalf("fixture: unpacking took %s after a %s transfer; the test needs unpacking to dominate", unpack, transfer)
	}

	deadline := (transfer + unpack/4).Round(time.Millisecond)
	src = newSrc()
	engine.SetGitLoadTimeout(src, deadline)
	start = time.Now()
	_, err := src.Load(t.Context())
	elapsed := time.Since(start)
	if arrived := sent().Sub(start); arrived <= 0 || arrived >= deadline {
		t.Fatalf("fixture: the pack arrived %s into a %s deadline; the deadline must fall while unpacking", arrived, deadline)
	}
	want := "git source: timed out after " + deadline.String()
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v after %s, want %q", err, elapsed, want)
	}
	if limit := deadline + unpack/4; elapsed > limit {
		t.Fatalf("Load returned %s into a %s deadline, past %s: unpacking ran on (it takes %s)", elapsed, deadline, limit, unpack)
	}
	if src.Revision() != "" {
		t.Fatal("a load cut short by its deadline set a revision")
	}

	// The load lock is free: the next Load on the same Source runs to the end.
	engine.SetGitLoadTimeout(src, 2*time.Minute)
	done := make(chan error, 1)
	go func() {
		_, err := src.Load(t.Context())
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the Load after the timeout: %v", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("the Load after the timeout never ran: the load lock was not released")
	}
	if src.Revision() != head.String() {
		t.Fatalf("revision = %q, want %s", src.Revision(), head)
	}
}
