//go:build unix

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Issue #389: the local Scope checks a file's type when it walks or Lstats it
// and opens it later, like DirSource (#367). Whoever can write the repository
// can swap a FIFO in between, so Open and ReadDir of the local content must
// not block. As in dirsource_fifo_unix_internal_test.go, the wrapper swaps the
// target on disk just before the loader opens it, through contentFS.
func TestLocalScopeDoesNotBlockOnAFIFOSwappedInAfterTheCheck(t *testing.T) {
	if err := syscall.Mkfifo(filepath.Join(t.TempDir(), "probe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	const article = "---\nname: A\n---\nBody.\n"
	files := map[string]string{
		".goldie/articles/kept.md":      article,
		".goldie/articles/team/deep.md": article,
		".goldie/config.yaml":           "suppress: [article:gone]\n",
	}
	for name, tc := range map[string]struct {
		target  string
		dir     bool
		symlink bool
	}{
		"article":                    {target: ".goldie/articles/team/deep.md"},
		"config.yaml":                {target: ".goldie/config.yaml"},
		"article symlink to FIFO":    {target: ".goldie/articles/kept.md", symlink: true},
		"config.yaml symlink":        {target: ".goldie/config.yaml", symlink: true},
		"articles directory":         {target: ".goldie/articles", dir: true},
		"nested articles directory":  {target: ".goldie/articles/team", dir: true},
		"articles directory symlink": {target: ".goldie/articles/team", dir: true, symlink: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, content := range files {
				p := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			pipe := filepath.Join(dir, "pipe")
			if err := syscall.Mkfifo(pipe, 0o644); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			inner := swapFS{inner: DirSource{}.contentFS(root), dir: dir, target: tc.target, symlink: tc.symlink}
			var swapped func() (bool, error)
			var load func() error
			if tc.dir {
				sw := &dirSwapFS{inner}
				swapped = func() (bool, error) { return sw.swapped, sw.swapErr }
				load = func() error { _, err := newSnapshot(nil).withLocal(sw, ".goldie"); return err }
			} else {
				sw := &inner
				swapped = func() (bool, error) { return sw.swapped, sw.swapErr }
				load = func() error { _, err := newSnapshot(nil).withLocal(sw, ".goldie"); return err }
			}

			done := make(chan error, 1)
			go func() { done <- load() }()
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				// Unblock the reader so the test process can exit.
				for _, p := range []string{filepath.Join(dir, filepath.FromSlash(tc.target)), pipe} {
					if f, err := os.OpenFile(p, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
						_ = f.Close()
					}
				}
				t.Fatalf("the load blocked on a FIFO swapped in at %s (once unblocked: %v)", tc.target, <-done)
			}
			if ok, swapErr := swapped(); !ok || swapErr != nil {
				t.Fatalf("the swap at %s did not happen (reached %v, error %v)", tc.target, ok, swapErr)
			}
			if err == nil {
				t.Fatalf("the load accepted a FIFO swapped in at %s; want it refused", tc.target)
			}
			if msg := err.Error(); !strings.Contains(msg, tc.target) || strings.Contains(msg, dir) {
				t.Fatalf("error %q; want it to name %s and no host path", msg, tc.target)
			}
		})
	}
}
