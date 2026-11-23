//go:build unix

package engine

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Issue #367: DirSource checks a file's type when it walks (fs.WalkDir's entry
// type, readProjectFile's fs.Lstat) and opens it later. Whoever can write the
// content directory can swap a FIFO in between, and a blocking open of a FIFO
// with no writer waits forever, holding the poll. The fix follows
// internal/contract/project/open_unix.go: open non-blocking, and refuse
// anything but a regular file.
//
// To make the swap deterministic, the test wraps the fs.FS DirSource loads
// from: the wrapper's Open of one file replaces it with a FIFO (or a symlink to
// one) and then opens it through DirSource's own FS. DirSource names that FS
// with the method contentFS(*os.Root) fs.FS: the fs.FS Load passes to loadFS.
// Until it exists, the test uses os.Root.FS, as Load on main does, and fails by
// timing out.

// contentFSOf returns the fs.FS DirSource loads root through.
func contentFSOf(t *testing.T, root *os.Root) fs.FS {
	t.Helper()
	if c, ok := any(DirSource{}).(interface{ contentFS(*os.Root) fs.FS }); ok {
		return c.contentFS(root)
	}
	t.Log("DirSource has no method contentFS(*os.Root) fs.FS; using os.Root.FS, as Load on main does")
	return root.FS()
}

// swapFS forwards everything to inner, except that the first Open of target
// first swaps target on disk: for a FIFO at dir/target, or a symlink to the
// FIFO at dir/pipe.
type swapFS struct {
	inner   fs.FS
	dir     string
	target  string
	symlink bool
	swapped bool
	swapErr error
}

func (s *swapFS) Open(name string) (fs.File, error) {
	if name == s.target && !s.swapped {
		s.swapped = true
		s.swapErr = s.swap()
	}
	return s.inner.Open(name)
}

func (s *swapFS) swap() error {
	p := filepath.Join(s.dir, filepath.FromSlash(s.target))
	if err := os.Remove(p); err != nil {
		return err
	}
	if !s.symlink {
		return syscall.Mkfifo(p, 0o644)
	}
	rel, err := filepath.Rel(filepath.Dir(p), filepath.Join(s.dir, "pipe"))
	if err != nil {
		return err
	}
	return os.Symlink(rel, p)
}

func (s *swapFS) ReadDir(name string) ([]fs.DirEntry, error) { return fs.ReadDir(s.inner, name) }
func (s *swapFS) Stat(name string) (fs.FileInfo, error)      { return fs.Stat(s.inner, name) }
func (s *swapFS) Lstat(name string) (fs.FileInfo, error)     { return fs.Lstat(s.inner, name) }
func (s *swapFS) ReadLink(name string) (string, error)       { return fs.ReadLink(s.inner, name) }

func TestDirSourceDoesNotBlockOnAFIFOSwappedInAfterTheWalk(t *testing.T) {
	if err := syscall.Mkfifo(filepath.Join(t.TempDir(), "probe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	const (
		shop    = "projects/github.com/acme/shop"
		article = "---\nname: A\n---\nBody.\n"
	)
	files := map[string]string{
		"global/articles/kept.md":            article,
		"global/articles/platform/deploy.md": article,
		shop + "/articles/guide.md":          article,
		shop + "/project.yaml":               "suppress: [article:platform/deploy]\n",
	}
	for name, tc := range map[string]struct {
		target  string
		symlink bool
	}{
		"global article":               {target: "global/articles/platform/deploy.md"},
		"project article":              {target: shop + "/articles/guide.md"},
		"project.yaml":                 {target: shop + "/project.yaml"},
		"global article symlink":       {target: "global/articles/kept.md", symlink: true},
		"project.yaml symlink to FIFO": {target: shop + "/project.yaml", symlink: true},
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
			sw := &swapFS{inner: contentFSOf(t, root), dir: dir, target: tc.target, symlink: tc.symlink}

			done := make(chan error, 1)
			go func() {
				_, err := loadFS(sw)
				done <- err
			}()
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				// Unblock the reader so the test process can exit.
				for _, p := range []string{filepath.Join(dir, filepath.FromSlash(tc.target)), pipe} {
					if f, err := os.OpenFile(p, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
						_ = f.Close()
					}
				}
				t.Fatalf("the load blocked on a FIFO swapped in at %s after the walk (once unblocked: %v)", tc.target, <-done)
			}
			if !sw.swapped || sw.swapErr != nil {
				t.Fatalf("the swap at %s did not happen (opened %v, error %v)", tc.target, sw.swapped, sw.swapErr)
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

// dirSwapFS is swapFS for a directory: the first ReadDir of target first
// replaces the directory on disk with a FIFO, or a symlink to the FIFO at
// dir/pipe, after the parent's listing (or the walk's Stat) saw a directory.
type dirSwapFS struct {
	swapFS
}

func (s *dirSwapFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == s.target && !s.swapped {
		s.swapped = true
		s.swapErr = s.swapDir()
	}
	return fs.ReadDir(s.inner, name)
}

func (s *dirSwapFS) Open(name string) (fs.File, error) { return s.inner.Open(name) }

func (s *dirSwapFS) swapDir() error {
	p := filepath.Join(s.dir, filepath.FromSlash(s.target))
	if err := os.RemoveAll(p); err != nil {
		return err
	}
	if !s.symlink {
		return syscall.Mkfifo(p, 0o644)
	}
	rel, err := filepath.Rel(filepath.Dir(p), filepath.Join(s.dir, "pipe"))
	if err != nil {
		return err
	}
	return os.Symlink(rel, p)
}

// A directory read through os.Root's FS is opened with a blocking O_RDONLY, so
// a FIFO swapped in for a directory between the parent's listing (or the
// walk's Stat) and the directory's own ReadDir blocks the poll as a swapped
// file does. Both walks reach it: loadArticles' fs.WalkDir and projectDirs. A
// repo directory is never read (projectDirs stops at it, and the loader only
// Stats paths below it), so it has no case here.
func TestDirSourceDoesNotBlockOnAFIFOSwappedInForADirectory(t *testing.T) {
	if err := syscall.Mkfifo(filepath.Join(t.TempDir(), "probe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	const (
		shop    = "projects/github.com/acme/shop"
		article = "---\nname: A\n---\nBody.\n"
	)
	files := map[string]string{
		"global/articles/kept.md":            article,
		"global/articles/team/deploy.md":     article,
		"global/articles/team/sub/nested.md": article,
		shop + "/articles/guide.md":          article,
		shop + "/articles/area/more.md":      article,
		shop + "/project.yaml":               "suppress: [article:kept]\n",
	}
	for name, tc := range map[string]struct {
		target  string
		symlink bool
	}{
		"global articles directory":        {target: "global/articles"},
		"nested global article directory":  {target: "global/articles/team"},
		"deeper nested article directory":  {target: "global/articles/team/sub"},
		"nested directory symlink to FIFO": {target: "global/articles/team", symlink: true},
		"project articles directory":       {target: shop + "/articles"},
		"project nested article directory": {target: shop + "/articles/area"},
		"project articles symlink to FIFO": {target: shop + "/articles", symlink: true},
		"projects directory":               {target: "projects"},
		"host directory":                   {target: "projects/github.com"},
		"org directory":                    {target: "projects/github.com/acme"},
		"org directory symlink to FIFO":    {target: "projects/github.com/acme", symlink: true},
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
			sw := &dirSwapFS{swapFS{inner: contentFSOf(t, root), dir: dir, target: tc.target, symlink: tc.symlink}}

			done := make(chan error, 1)
			go func() {
				_, err := loadFS(sw)
				done <- err
			}()
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				// Unblock the reader so the test process can exit.
				for _, p := range []string{filepath.Join(dir, filepath.FromSlash(tc.target)), pipe} {
					if f, err := os.OpenFile(p, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
						_ = f.Close()
					}
				}
				t.Fatalf("the load blocked on a FIFO swapped in for the directory %s (once unblocked: %v)", tc.target, <-done)
			}
			if !sw.swapped || sw.swapErr != nil {
				t.Fatalf("the swap at %s did not happen (read %v, error %v)", tc.target, sw.swapped, sw.swapErr)
			}
			if err == nil {
				t.Fatalf("the load accepted a FIFO swapped in for the directory %s; want it refused", tc.target)
			}
			if msg := err.Error(); !strings.Contains(msg, tc.target) || strings.Contains(msg, dir) {
				t.Fatalf("error %q; want it to name %s and no host path", msg, tc.target)
			}
		})
	}
}
