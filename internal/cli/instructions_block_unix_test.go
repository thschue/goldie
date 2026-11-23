//go:build unix

package cli_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/cli"
)

// Unix half of the #413 acceptance tests: symlinks, file modes and atomic
// writes. See instructions_block_test.go for the names under test.

func TestApplyBlockFileRefusesASymlinkedFile(t *testing.T) {
	content := blockFixture(t, "content.md")
	t.Run("to a file", func(t *testing.T) {
		dir := t.TempDir()
		victim := filepath.Join(t.TempDir(), "victim.md")
		orig := blockFixture(t, "outdated", "in.md")
		if err := os.WriteFile(victim, orig, 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "CLAUDE.md")
		if err := os.Symlink(victim, link); err != nil {
			t.Fatal(err)
		}
		if changed, err := cli.ApplyBlockFile(dir, "CLAUDE.md", content); !errors.Is(err, cli.ErrBlockSymlink) || changed {
			t.Errorf("apply: changed = %v, err = %v; want ErrBlockSymlink", changed, err)
		}
		if _, err := cli.CheckBlockFile(dir, "CLAUDE.md", content); !errors.Is(err, cli.ErrBlockSymlink) {
			t.Errorf("check: err = %v, want ErrBlockSymlink", err)
		}
		if got, _ := os.ReadFile(victim); !bytes.Equal(got, orig) {
			t.Errorf("the symlink target was written: %q", got)
		}
		if got, err := os.Readlink(link); err != nil || got != victim {
			t.Errorf("the symlink was replaced: %q, %v", got, err)
		}
		if es := blockEntries(t, dir); len(es) != 1 {
			t.Errorf("directory holds %v, want only the link", es)
		}
	})
	t.Run("dangling", func(t *testing.T) {
		dir := t.TempDir()
		missing := filepath.Join(t.TempDir(), "missing.md")
		if err := os.Symlink(missing, filepath.Join(dir, "AGENTS.md")); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.ApplyBlockFile(dir, "AGENTS.md", content); !errors.Is(err, cli.ErrBlockSymlink) {
			t.Errorf("err = %v, want ErrBlockSymlink", err)
		}
		if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the dangling target was created: %v", err)
		}
	})
	t.Run("message holds no path", func(t *testing.T) {
		dir := t.TempDir()
		victim := filepath.Join(t.TempDir(), "victim.md")
		if err := os.WriteFile(victim, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(dir, "CLAUDE.md")); err != nil {
			t.Fatal(err)
		}
		_, err := cli.ApplyBlockFile(dir, "CLAUDE.md", content)
		if err == nil {
			t.Fatal("no error")
		}
		for _, leak := range []string{dir, victim} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("error %q echoes a path", err)
			}
		}
	})
}

func TestApplyBlockFileKeepsTheFileMode(t *testing.T) {
	content := blockFixture(t, "content.md")
	for _, mode := range []os.FileMode{0o600, 0o640, 0o664, 0o755} {
		for _, fixture := range []string{"no_block", "outdated", "edited"} {
			dir := t.TempDir()
			path := filepath.Join(dir, "CLAUDE.md")
			if err := os.WriteFile(path, blockFixture(t, fixture, "in.md"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil { // past the umask
				t.Fatal(err)
			}
			changed, err := cli.ApplyBlockFile(dir, "CLAUDE.md", content)
			if err != nil || !changed {
				t.Fatalf("%v/%s: changed = %v, err = %v", mode, fixture, changed, err)
			}
			fi, err := os.Stat(path)
			if err != nil || fi.Mode().Perm() != mode {
				t.Errorf("%s: mode = %v, %v; want %v kept", fixture, fi.Mode().Perm(), err, mode)
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, blockFixture(t, fixture, "want.md")) {
				t.Errorf("%s: wrote %q", fixture, got)
			}
			if es := blockEntries(t, dir); len(es) != 1 {
				t.Errorf("%s: directory holds %v, want no temp file left", fixture, es)
			}
		}
	}
}

func TestApplyBlockFileWriteFailureLeavesTheOriginal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	orig := blockFixture(t, "outdated", "in.md")
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory that cannot take a new entry: the temp file cannot be made,
	// and a write that opened the file in place would still succeed.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	changed, err := cli.ApplyBlockFile(dir, "CLAUDE.md", blockFixture(t, "content.md"))
	if err == nil || changed {
		t.Fatalf("changed = %v, err = %v; want an error", changed, err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, orig) {
		t.Errorf("the original changed: %q", got)
	}
	if strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), "CLAUDE.md") {
		t.Errorf("error %q echoes a path", err)
	}
}

func TestApplyBlockFileDoesNotOpenAFifo(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "CLAUDE.md"), 0o644); err != nil {
		t.Skip("no fifo:", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := cli.ApplyBlockFile(dir, "CLAUDE.md", blockFixture(t, "content.md"))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a fifo was accepted as an instruction file")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ApplyBlockFile blocked opening a fifo")
	}
}
