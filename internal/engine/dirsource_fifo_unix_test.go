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

// Issue #367: whoever can write the content directory must not be able to
// block a poll with a FIFO. A FIFO that is already there when the walk runs
// (as an article, behind a symlinked article, or as project.yaml) is never
// opened for a blocking read: Load returns, and when it fails, the error names
// the file relative to the root. The swap between the walk and the open is in
// dirsource_fifo_unix_internal_test.go.

// fifoLoadBound is how long a Load of a handful of files may take before the
// test calls it blocked.
const fifoLoadBound = 3 * time.Second

func TestDirSourceDoesNotBlockOnAFIFOInTheContent(t *testing.T) {
	cases := map[string]struct {
		place   string // where the FIFO, or a symlink to one, goes
		symlink bool
		mustErr bool
	}{
		"global article":             {place: globalFile("pipe")},
		"nested global article":      {place: globalFile("team/pipe")},
		"project article":            {place: projectFile("pipe")},
		"global article symlink":     {place: globalFile("pipe"), symlink: true},
		"project article symlink":    {place: projectFile("pipe"), symlink: true},
		"project.yaml":               {place: supFile, mustErr: true},
		"project.yaml symlink":       {place: supFile, symlink: true, mustErr: true},
		"other project.yaml symlink": {place: otherSupFile, symlink: true, mustErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := writeFiles(t, map[string]string{
				globalFile("kept"):      article("Kept"),
				projectFile("own"):      article("Own"),
				otherProjectFile("own"): article("Other own"),
			})
			fifo := filepath.Join(root, filepath.FromSlash(tc.place))
			if tc.symlink {
				fifo = filepath.Join(root, "global", "pipe")
			}
			if err := os.MkdirAll(filepath.Dir(fifo), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(fifo, 0o644); err != nil {
				t.Skipf("mkfifo: %v", err)
			}
			if tc.symlink {
				link := filepath.Join(root, filepath.FromSlash(tc.place))
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
				snap, err := engine.NewDirSource(root).Load(t.Context())
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
				if strings.Contains(msg, root) {
					t.Errorf("error %q leaks the root path", msg)
				}
				return
			}
			if tc.mustErr {
				t.Fatalf("Load accepted a FIFO at %s; project.yaml must be a regular file", tc.place)
			}
			for _, proj := range []string{"", govProject} {
				if a, ok := r.snap.ArticleIn(proj, "pipe"); ok {
					t.Errorf("ArticleIn(%q, pipe) = %+v; a FIFO is not an article", proj, a)
				}
			}
		})
	}
}
