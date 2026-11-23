//go:build unix

package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #389, criterion 4, as #367 for the base content: whoever can write the
// repository must not be able to block a load with a FIFO in .goldie. A FIFO
// as config.yaml, as an article, behind a symlink, or in place of .goldie or
// its articles directory is never opened for a blocking read: Load returns
// within fifoLoadBound, and when it fails, the error names the path relative
// to the repository root.

func TestLocalSourceDoesNotBlockOnAFIFO(t *testing.T) {
	cases := map[string]struct {
		place   string // where the FIFO, or a symlink to one, goes
		symlink bool
		mustErr bool
	}{
		"article":             {place: localFile("pipe")},
		"nested article":      {place: localFile("team/pipe")},
		"article symlink":     {place: localFile("pipe"), symlink: true},
		"config.yaml":         {place: localConfigFile, mustErr: true},
		"config.yaml symlink": {place: localConfigFile, symlink: true, mustErr: true},
		"articles directory":  {place: ".goldie/articles"},
		".goldie":             {place: ".goldie", mustErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{globalFile("kept"): article("Kept")}
			if tc.place != ".goldie" && tc.place != ".goldie/articles" {
				files[localFile("mine")] = article("Mine")
			}
			src, dirs := newLocalSource(t, files)
			fifo := filepath.Join(dirs.repo, filepath.FromSlash(tc.place))
			if tc.symlink {
				fifo = filepath.Join(dirs.repo, ".goldie", "pipe-target")
			}
			if err := os.MkdirAll(filepath.Dir(fifo), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(fifo, 0o644); err != nil {
				t.Skipf("mkfifo: %v", err)
			}
			if tc.symlink {
				link := filepath.Join(dirs.repo, filepath.FromSlash(tc.place))
				if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
					t.Fatal(err)
				}
				rel, err := filepath.Rel(filepath.Dir(link), fifo)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(rel, link); err != nil {
					t.Skipf("symlink: %v", err)
				}
			}

			type result struct {
				snap *engine.Snapshot
				err  error
			}
			done := make(chan result, 1)
			go func() {
				snap, err := src.Load(t.Context())
				done <- result{snap, err}
			}()
			var r result
			select {
			case r = <-done:
			case <-time.After(fifoLoadBound):
				// Unblock the reader so the test process can exit.
				if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
					_ = f.Close()
				}
				<-done
				t.Fatalf("Load blocked on a FIFO at %s for %v", tc.place, fifoLoadBound)
			}
			if r.err != nil {
				msg := r.err.Error()
				if !strings.Contains(msg, tc.place) {
					t.Errorf("error %q does not name %s", msg, tc.place)
				}
				assertNoHostPath(t, msg, dirs)
				return
			}
			if tc.mustErr {
				t.Fatalf("Load accepted a FIFO at %s", tc.place)
			}
			for _, proj := range localViews {
				if a, ok := r.snap.ArticleIn(proj, "pipe", withDrafts); ok {
					t.Errorf("ArticleIn(%q, pipe) = %+v; a FIFO is not an article", proj, a)
				}
			}
		})
	}
}
