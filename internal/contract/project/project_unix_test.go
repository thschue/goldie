//go:build unix

package project

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestDetectProjectSkipsNonRegularFiles checks that a FIFO (or a symlink to
// one, or to a device such as /dev/tty) planted as .goldie/config.yaml, .git
// or .git/config cannot make detection block on open or read.
func TestDetectProjectSkipsNonRegularFiles(t *testing.T) {
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
				_, _ = Detect(dir, "", "") // an error is fine; blocking is not
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
				t.Fatal("Detect blocked on a non-regular file")
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

// A FIFO planted as .goldie/config.yaml is refused by Detect and by
// OpenConfigContentDir, and neither blocks on it (#484).
func TestConfigYAMLFIFOIsRefusedByDetectAndOpenConfigContentDir(t *testing.T) {
	root := localRepo(t, nil)
	if err := os.Mkdir(filepath.Join(root, ".goldie"), 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".goldie", "config.yaml")
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	type verdict struct {
		detectErr error
		id        string
		openErr   error
		handle    *os.Root
	}
	done := make(chan verdict, 1)
	go func() {
		var v verdict
		got, err := Detect(root, "", "")
		v.id, v.detectErr = got.ID, err
		v.handle, v.openErr = OpenConfigContentDir(root)
		done <- v
	}()
	select {
	case v := <-done:
		if v.handle != nil {
			_ = v.handle.Close()
		}
		if v.detectErr == nil || v.id != "" {
			t.Errorf("Detect on a FIFO config.yaml = %q, %v; want a refusal", v.id, v.detectErr)
		}
		if v.openErr == nil || v.handle != nil {
			t.Errorf("OpenConfigContentDir on a FIFO config.yaml = %v, %v; want a refusal", v.handle, v.openErr)
		}
	case <-time.After(5 * time.Second):
		// Unblock the reader so the test process can exit.
		if f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		t.Fatal("a reader blocked on a FIFO config.yaml")
	}
}
