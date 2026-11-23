//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Acceptance tests for #282 that need symlinks or control bytes in a path:
// goldie init never follows a symlinked .goldie/ or config.yaml, and a parent
// directory's symlinked .goldie/ is never touched.

func TestCLIInitNeverFollowsSymlinkedGoldieDir(t *testing.T) {
	root := gitRepo(t, widgetsOrigin)
	victim := t.TempDir()
	link := filepath.Join(root, ".goldie")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	out, err := runInit(t, root, nil, "--project", "github.com/acme/widgets")
	if err == nil || err.Error() != initErrSymlink {
		t.Errorf("symlinked .goldie/: err = %v, want %q", err, initErrSymlink)
	}
	if out != "" {
		t.Errorf("printed %q, want nothing", out)
	}
	if entries, err := os.ReadDir(victim); err != nil || len(entries) != 0 {
		t.Errorf("symlink target holds %d entries (%v), want it untouched", len(entries), err)
	}
	if got, err := os.Readlink(link); err != nil || got != victim {
		t.Errorf(".goldie -> %q, %v; want the symlink left alone", got, err)
	}

	// Dangling: the target is not created.
	dangling := gitRepo(t, widgetsOrigin)
	missing := filepath.Join(t.TempDir(), "missing")
	if err := os.Symlink(missing, filepath.Join(dangling, ".goldie")); err != nil {
		t.Fatal(err)
	}
	if _, err := runInit(t, dangling, nil); err == nil || err.Error() != initErrSymlink {
		t.Errorf("dangling .goldie symlink: err = %v, want %q", err, initErrSymlink)
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dangling .goldie target was created: %v", err)
	}
}

func TestCLIInitNeverFollowsSymlinkedConfig(t *testing.T) {
	root := gitRepo(t, widgetsOrigin)
	victim := filepath.Join(t.TempDir(), "victim")
	writeFile(t, victim, "victim\n")
	link := filepath.Join(subdir(t, root, ".goldie"), "config.yaml")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	out, err := runInit(t, root, nil, "--project", "github.com/acme/widgets")
	if err == nil || err.Error() != initErrExists {
		t.Errorf("symlinked config.yaml: err = %v, want %q", err, initErrExists)
	}
	if out != "" {
		t.Errorf("printed %q, want nothing", out)
	}
	if got := fileContent(t, victim); got != "victim\n" {
		t.Errorf("symlink target = %q, want it untouched", got)
	}

	// Dangling: O_CREAT without O_EXCL would create the target.
	dangling := gitRepo(t, widgetsOrigin)
	missing := filepath.Join(t.TempDir(), "created-through-the-link")
	if err := os.Symlink(missing, filepath.Join(subdir(t, dangling, ".goldie"), "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := runInit(t, dangling, nil, "--project", "github.com/acme/widgets"); err == nil || err.Error() != initErrExists {
		t.Errorf("dangling config.yaml symlink: err = %v, want %q", err, initErrExists)
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dangling config.yaml target was created: %v", err)
	}
}

// TestCLIInitIgnoresHostileParentSymlink checks that a parent repository's
// symlinked .goldie/ is neither followed nor a reason to refuse: the nested
// repository gets its own config.
func TestCLIInitIgnoresHostileParentSymlink(t *testing.T) {
	outer := gitRepo(t, "[remote \"origin\"]\n\turl = https://evil.example/other/outer\n")
	victim := t.TempDir()
	if err := os.Symlink(victim, filepath.Join(outer, ".goldie")); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "vendor", "inner")
	writeFile(t, filepath.Join(inner, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/inner.git\n")
	out, err := runInit(t, subdir(t, inner, "pkg"), nil)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if got := fileContent(t, filepath.Join(inner, ".goldie", "config.yaml")); got != "project: github.com/acme/inner\n" {
		t.Errorf("inner config.yaml = %q", got)
	}
	if entries, err := os.ReadDir(victim); err != nil || len(entries) != 0 {
		t.Errorf("parent's symlink target holds %d entries (%v), want it untouched", len(entries), err)
	}
}

// TestCLIInitCleansThePathItPrints checks that a repository path holding
// terminal escapes reaches the output without them.
func TestCLIInitCleansThePathItPrints(t *testing.T) {
	root := filepath.Join(t.TempDir(), "re\x1b]0;pwn\x07po\x1b[31m")
	writeFile(t, filepath.Join(root, ".git", "config"), widgetsOrigin)
	out, err := runInit(t, root, nil)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("output holds terminal escapes: %q", out)
	}
	if !strings.Contains(out, "Wrote: ") || strings.Count(out, "\n") != 3 {
		t.Errorf("output = %q, want the three init lines", out)
	}
	if got := fileContent(t, filepath.Join(root, ".goldie", "config.yaml")); got != widgetsConfig {
		t.Errorf("config.yaml = %q", got)
	}
}
