//go:build unix

package project

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Acceptance tests for #282: WriteConfig never follows a symlinked .goldie/ or
// a symlinked config.yaml (O_EXCL|O_NOFOLLOW), and leaves the target alone.

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// assertLink fails unless link is still a symlink to target.
func assertLink(t *testing.T, link, target string) {
	t.Helper()
	got, err := os.Readlink(link)
	if err != nil || got != target {
		t.Errorf("%s -> %q, %v; want the symlink to %q left alone", link, got, err, target)
	}
}

// assertEmptyDir fails unless dir exists and holds nothing.
func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%s holds %d entries, want the symlink target untouched", dir, len(entries))
	}
}

func TestWriteConfigNeverFollowsSymlinkedGoldieDir(t *testing.T) {
	root := repo(t, forkConfig)
	victim := t.TempDir()
	link := filepath.Join(root, ".goldie")
	symlink(t, victim, link)
	if err := WriteConfig(root, "github.com/acme/widgets"); !errors.Is(err, ErrSymlink) {
		t.Errorf("symlinked .goldie/: err = %v, want ErrSymlink", err)
	}
	assertEmptyDir(t, victim)
	assertLink(t, link, victim)

	// A symlink to a directory inside the repository is refused just the same.
	inside := repo(t, forkConfig)
	mkdir(t, filepath.Join(inside, "elsewhere"))
	symlink(t, "elsewhere", filepath.Join(inside, ".goldie"))
	if err := WriteConfig(inside, "github.com/acme/widgets"); !errors.Is(err, ErrSymlink) {
		t.Errorf("symlinked .goldie/ inside the repository: err = %v, want ErrSymlink", err)
	}
	assertEmptyDir(t, filepath.Join(inside, "elsewhere"))

	// A dangling .goldie symlink is refused, and its target is not created.
	dangling := repo(t, forkConfig)
	missing := filepath.Join(t.TempDir(), "missing")
	symlink(t, missing, filepath.Join(dangling, ".goldie"))
	if err := WriteConfig(dangling, "github.com/acme/widgets"); !errors.Is(err, ErrSymlink) {
		t.Errorf("dangling .goldie symlink: err = %v, want ErrSymlink", err)
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dangling .goldie target was created: %v", err)
	}
	assertLink(t, filepath.Join(dangling, ".goldie"), missing)
}

func TestWriteConfigNeverFollowsSymlinkedConfig(t *testing.T) {
	// A symlink to an existing file: the file stays as it was.
	root := repo(t, forkConfig)
	victim := filepath.Join(t.TempDir(), "victim")
	write(t, victim, "victim\n")
	link := filepath.Join(mkdir(t, filepath.Join(root, ".goldie")), "config.yaml")
	symlink(t, victim, link)
	if err := WriteConfig(root, "github.com/acme/widgets"); !errors.Is(err, ErrConfigExists) {
		t.Errorf("symlinked config.yaml: err = %v, want ErrConfigExists", err)
	}
	if got := readFile(t, victim); got != "victim\n" {
		t.Errorf("symlink target = %q, want it untouched", got)
	}
	assertLink(t, link, victim)

	// A dangling symlink: O_CREAT without O_EXCL would create its target.
	dangling := repo(t, forkConfig)
	missing := filepath.Join(t.TempDir(), "created-through-the-link")
	dlink := filepath.Join(mkdir(t, filepath.Join(dangling, ".goldie")), "config.yaml")
	symlink(t, missing, dlink)
	if err := WriteConfig(dangling, "github.com/acme/widgets"); !errors.Is(err, ErrConfigExists) {
		t.Errorf("dangling config.yaml symlink: err = %v, want ErrConfigExists", err)
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dangling config.yaml target was created: %v", err)
	}
	assertLink(t, dlink, missing)
}

// TestWriteConfigFifoAtConfigPath checks that a FIFO planted as config.yaml is
// refused as existing, without blocking.
func TestWriteConfigFifoAtConfigPath(t *testing.T) {
	root := repo(t, forkConfig)
	fifo := filepath.Join(mkdir(t, filepath.Join(root, ".goldie")), "config.yaml")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if err := WriteConfig(root, "github.com/acme/widgets"); !errors.Is(err, ErrConfigExists) {
		t.Errorf("FIFO at config.yaml: err = %v, want ErrConfigExists", err)
	}
	if fi, err := os.Lstat(fifo); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("FIFO at config.yaml replaced: %v, %v", fi, err)
	}
}
