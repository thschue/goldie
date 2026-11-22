//go:build unix

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestResolveProjectSkipsNonRegularFiles checks that a FIFO (or a symlink to
// one, or to a device such as /dev/tty) planted as .goldie/config.yaml, .git
// or .git/config cannot make detection block on open or read.
func TestResolveProjectSkipsNonRegularFiles(t *testing.T) {
	fifo := func(t *testing.T, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(path, 0o644); err != nil {
			t.Skipf("mkfifo: %v", err)
		}
	}
	cases := map[string]func(t *testing.T) string{
		"config.yaml fifo": func(t *testing.T) string {
			dir := t.TempDir()
			fifo(t, filepath.Join(dir, ".goldie", "config.yaml"))
			return dir
		},
		"config.yaml symlink to fifo": func(t *testing.T) string {
			dir := t.TempDir()
			target := filepath.Join(t.TempDir(), "pipe")
			fifo(t, target)
			if err := os.MkdirAll(filepath.Join(dir, ".goldie"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(dir, ".goldie", "config.yaml")); err != nil {
				t.Fatal(err)
			}
			return dir
		},
		".git fifo": func(t *testing.T) string {
			dir := t.TempDir()
			fifo(t, filepath.Join(dir, ".git"))
			return dir
		},
		".git/config fifo": func(t *testing.T) string {
			dir := t.TempDir()
			fifo(t, filepath.Join(dir, ".git", "config"))
			return dir
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			dir := mk(t)
			done := make(chan struct{})
			go func() {
				_, _ = resolveProject(dir, "", "") // an error is fine; blocking is not
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				// Unblock the reader so the test process can exit.
				for _, p := range []string{".goldie/config.yaml", ".git", ".git/config"} {
					if f, err := os.OpenFile(filepath.Join(dir, p), os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
						_ = f.Close()
					}
				}
				t.Fatal("resolveProject blocked on a non-regular file")
			}
		})
	}
}

// TestOpenCappedRefusesFIFO checks the open-then-check step on its own: a FIFO
// that replaces a file between readCapped's type check and its open must not
// block the open, and must be refused.
func TestOpenCappedRefusesFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := openCapped(path)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errNotRegular) {
			t.Fatalf("openCapped(fifo) error = %v, want errNotRegular", err)
		}
	case <-time.After(5 * time.Second):
		// Unblock the reader so the test process can exit.
		if f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		t.Fatal("openCapped blocked on a FIFO")
	}
}
