package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/cli"
)

// Acceptance tests for #424: local mode opens the content directory once, as an
// *os.Root, right after it is validated, and the embedded server reads only
// through that handle. The fake Env.StartLocal is the point between validation
// and server start: a swap of the path there must not redirect the server.

const (
	swapValidated = "---\nname: Validated\n---\nValidated body.\n"
	swapOutside   = "---\nname: Outside\n---\nOutside body.\n"
)

// swapFixture writes a content directory with one article and an outside
// directory with another, under base.
func swapFixture(t *testing.T, base string) (content, outside string) {
	t.Helper()
	content = filepath.Join(base, "content")
	outside = filepath.Join(base, "outside")
	for dir, body := range map[string]string{content: swapValidated, outside: swapOutside} {
		if err := os.MkdirAll(filepath.Join(dir, "global", "articles"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "global", "articles", "a.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return content, outside
}

// swapPathForSymlink replaces the path content with a symlink to outside, the
// way someone with write access to the checkout would.
func swapPathForSymlink(t *testing.T, content, outside string) {
	t.Helper()
	if err := os.Rename(content, content+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, content); err != nil {
		t.Fatal(err)
	}
}

// requireRootStillSeesValidated checks that root reads the validated content,
// twice, as later loads through the same handle would.
func requireRootStillSeesValidated(t *testing.T, root *os.Root) {
	t.Helper()
	if root == nil {
		t.Error("the start hook received a nil *os.Root for a validated content directory")
		return
	}
	for range 2 {
		b, err := root.ReadFile("global/articles/a.md")
		if err != nil {
			t.Errorf("read through the root after the swap: %v", err)
			return
		}
		if string(b) != swapValidated {
			t.Errorf("root read %q after the swap, want the validated content %q", b, swapValidated)
		}
	}
}

// requireClosed checks that root was closed: a Stat on it fails.
func requireClosed(t *testing.T, root *os.Root) {
	t.Helper()
	if root == nil {
		return
	}
	if _, err := root.Stat("."); err == nil {
		t.Error("the root is still open after stop")
	}
}

func TestLocalModeExplicitDirIsOpenedOnceAndSurvivesASwap(t *testing.T) {
	content, outside := swapFixture(t, t.TempDir())
	var out bytes.Buffer
	env := localEnv(nil, &out)
	var got *os.Root
	called := false
	env.StartLocal = func(_ context.Context, root *os.Root) (string, string, func(), error) {
		called = true
		got = root
		swapPathForSymlink(t, content, outside)
		requireRootStillSeesValidated(t, root)
		// The stop is the hook's own and does nothing: closing the root is the
		// CLI's job, in the stop that Open hands back.
		return closedAddr(t), localTestToken, func() {}, nil
	}
	_ = cli.Run(context.Background(), []string{"search", "x", "--dir", content}, env)
	if !called {
		t.Fatal("StartLocal was not called")
	}
	requireClosed(t, got)
}

func TestLocalModeGoldieDirEnvIsOpenedOnceAndSurvivesASwap(t *testing.T) {
	content, outside := swapFixture(t, t.TempDir())
	var out bytes.Buffer
	env := localEnv(map[string]string{"GOLDIE_DIR": content}, &out)
	var got *os.Root
	called := false
	env.StartLocal = func(_ context.Context, root *os.Root) (string, string, func(), error) {
		called = true
		got = root
		swapPathForSymlink(t, content, outside)
		requireRootStillSeesValidated(t, root)
		return closedAddr(t), localTestToken, func() {}, nil
	}
	_ = cli.Run(context.Background(), []string{"search", "x"}, env)
	if !called {
		t.Fatal("StartLocal was not called")
	}
	requireClosed(t, got)
}

func TestLocalModeConfigContentDirIsOpenedOnceAndSurvivesASwap(t *testing.T) {
	repo := t.TempDir()
	// The content directory the config names lives inside the repository.
	content, outside := swapFixture(t, repo)
	if err := os.Rename(content, filepath.Join(repo, "kb")); err != nil {
		t.Fatal(err)
	}
	content = filepath.Join(repo, "kb")
	files := map[string]string{
		filepath.Join(repo, ".goldie", "config.yaml"):          "dir: kb\n",
		filepath.Join(repo, ".goldie", "articles", "local.md"): "---\nname: Local\n---\nLocal.\n",
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(repo)
	var out bytes.Buffer
	env := localEnv(nil, &out)
	var got *os.Root
	called := false
	env.StartLocalScope = func(_ context.Context, root *os.Root, goldieDir string) (string, string, func(), error) {
		called = true
		got = root
		if goldieDir == "" {
			t.Error("StartLocalScope received no .goldie directory")
		}
		swapPathForSymlink(t, content, outside)
		requireRootStillSeesValidated(t, root)
		return closedAddr(t), localTestToken, func() {}, nil
	}
	env.StartLocal = func(context.Context, *os.Root) (string, string, func(), error) {
		t.Error("StartLocal called for a repository with a local Scope")
		return "", "", func() {}, context.Canceled
	}
	_ = cli.Run(context.Background(), []string{"search", "x"}, env)
	if !called {
		t.Fatal("StartLocalScope was not called")
	}
	requireClosed(t, got)
}

// With no content directory the embedded server gets a nil root.
func TestLocalModeWithoutContentDirPassesNoRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".goldie", "articles"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	var out bytes.Buffer
	env := localEnv(nil, &out)
	called := false
	env.StartLocalScope = func(_ context.Context, root *os.Root, _ string) (string, string, func(), error) {
		called = true
		if root != nil {
			t.Error("StartLocalScope received a root although no content directory is configured")
		}
		return closedAddr(t), localTestToken, func() {}, nil
	}
	_ = cli.Run(context.Background(), []string{"search", "x"}, env)
	if !called {
		t.Fatal("StartLocalScope was not called")
	}
}

// A directory that cannot be opened fails the command before any server
// starts, and the error holds no path.
func TestLocalModeMissingDirDoesNotStartTheServer(t *testing.T) {
	var out bytes.Buffer
	env := localEnv(nil, &out)
	env.StartLocal = func(context.Context, *os.Root) (string, string, func(), error) {
		t.Error("StartLocal called for a content directory that does not exist")
		return "", "", func() {}, context.Canceled
	}
	missing := filepath.Join(t.TempDir(), "nope")
	err := cli.Run(context.Background(), []string{"search", "x", "--dir", missing}, env)
	if err == nil {
		t.Fatal("no error for a missing content directory")
	}
	if strings.Contains(err.Error(), missing) {
		t.Errorf("error carries the path: %q", err)
	}
}
