package cli_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/journeytest"
)

// Journey J5: a developer runs the CLI for the first time, on the built
// binary. The test starts `goldie server --dev` on examples/knowledge and runs
// the goldie client commands against it as a user would. A red step names what
// the developer could not do.

// journeyRun runs the built goldie in dir with a clean environment (no
// config, no token) plus env, and returns its output and exit code.
func journeyRun(t *testing.T, bin, dir string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, env...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("goldie %v did not finish", args)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("goldie %v: %v", args, err)
	}
	return o.String(), e.String(), code
}

func TestJ5CLIFirstRun(t *testing.T) {
	bin, addr, token := journeytest.Server(t)
	withAddr := []string{"GOLDIE_ADDR=" + addr}
	withToken := []string{"GOLDIE_ADDR=" + addr, "GOLDIE_TOKEN=" + token}

	t.Run("first-run", func(t *testing.T) {
		stdout, stderr, code := journeyRun(t, bin, t.TempDir(), withAddr, "search", "loops")
		if code == 0 {
			t.Errorf("search without a token exited 0, want non-zero\nstdout: %s", stdout)
		}
		if !strings.Contains(stdout+stderr, "GOLDIE_TOKEN") {
			t.Errorf("search without a token does not name the next step (where to set a token):\nstdout: %s\nstderr: %s", stdout, stderr)
		}
		if strings.Contains(stdout, "Autonomous agent loops") {
			t.Errorf("search without a token printed hits:\n%s", stdout)
		}
	})

	t.Run("search", func(t *testing.T) {
		stdout, stderr, code := journeyRun(t, bin, t.TempDir(), withToken, "search", "loops")
		if code != 0 {
			t.Fatalf("search with GOLDIE_TOKEN exited %d\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stdout, "Autonomous agent loops") {
			t.Errorf("search printed no hit title:\n%s", stdout)
		}
	})

	t.Run("pull", func(t *testing.T) {
		repo := t.TempDir()
		if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		stdout, stderr, code := journeyRun(t, bin, repo, withToken, "pull")
		if code != 0 {
			t.Fatalf("pull exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		agents, _ := filepath.Glob(filepath.Join(repo, ".claude", "agents", "goldie-*.md"))
		skills, _ := filepath.Glob(filepath.Join(repo, ".claude", "skills", "goldie-*", "SKILL.md"))
		if len(agents) == 0 || len(skills) == 0 {
			t.Fatalf("pull wrote %d agents and %d skills into the Goldie namespace, want some of each\n%s", len(agents), len(skills), stdout)
		}
		if strings.Contains(stdout, "up to date") {
			t.Errorf("the first pull reports up to date:\n%s", stdout)
		}
		before, err := os.ReadFile(agents[0])
		if err != nil {
			t.Fatal(err)
		}

		stdout, stderr, code = journeyRun(t, bin, repo, withToken, "pull")
		if code != 0 {
			t.Fatalf("second pull exited %d\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stdout, "up to date") {
			t.Errorf("second pull does not report up to date:\n%s", stdout)
		}
		after, err := os.ReadFile(agents[0])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("second pull changed %s", agents[0])
		}
	})
}
