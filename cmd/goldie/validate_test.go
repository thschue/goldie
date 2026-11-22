package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #285: goldie validate [dir] runs the load goldie server runs over the dir
// Source (the content() path in internal/app), so its rules are the server's
// by construction and grow when the server's do.
//
// Decisions fixed here:
//   - Which dir: the one argument; else GOLDIE_CONTENT_DIR; else the working
//     directory. This is the order goldie server -dev uses, so an author who
//     knows one knows the other.
//   - Valid content: exit 0, nothing on stderr.
//   - Invalid content: exit 1, nothing on stdout, and exactly one line on
//     stderr: "goldie: content does not load: <reason>", where the reason is
//     the loader's own (a file relative to the dir and the rule, or the fixed
//     "content directory is not readable"). The line is the same one
//     goldie server -dev ends with, so the tests compare the two.
//   - Never a stack trace, goroutine dump or absolute path.
//   - A second positional argument is a usage error (exit 1, the usage on
//     stderr, which names "validate [dir]").
//   - An empty directory is valid, as for the server (no articles, no error).

const validateLoadPrefix = "goldie: content does not load: "

// validateEnv is a minimal environment: no token, no address, which validate
// must not need.
func validateEnv(extra ...string) []string {
	return append([]string{"PATH=" + os.Getenv("PATH")}, extra...)
}

// runValidate runs goldie args in cwd and returns its output and exit code.
func runValidate(t *testing.T, cwd string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goldieBinary(t), args...)
	cmd.Env = env
	cmd.Dir = cwd
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("goldie %v kept running\nstdout:\n%s\nstderr:\n%s", args, o.String(), e.String())
	}
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("goldie %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return o.String(), e.String(), code
}

func writeArticle(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const goodArticle = "---\nname: Good\nkeywords: [good]\n---\nBody.\n"

// noCrashOutput fails when out looks like a Go panic, stack trace or
// goroutine dump.
func noCrashOutput(t *testing.T, out string) {
	t.Helper()
	for _, bad := range []string{"goroutine ", "panic:", ".go:", "runtime.", "stack trace"} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains %q, which looks like a crash trace:\n%s", bad, out)
		}
	}
}

// wantInvalid asserts the invalid-content contract and returns the reason.
// Neither the given paths nor the temp directory may appear in the output.
func wantInvalid(t *testing.T, stdout, stderr string, code int, absPaths ...string) string {
	t.Helper()
	if code != 1 {
		t.Errorf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty: errors go to stderr", stdout)
	}
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], validateLoadPrefix) {
		t.Fatalf("stderr = %q, want exactly one line starting %q", stderr, validateLoadPrefix)
	}
	noCrashOutput(t, stdout+stderr)
	for _, p := range append(absPaths, os.TempDir()) {
		if p != "" && strings.Contains(stdout+stderr, p) {
			t.Errorf("output echoes the path %q:\n%s", p, stderr)
		}
	}
	return strings.TrimPrefix(lines[0], validateLoadPrefix)
}

func TestValidateAcceptsValidContent(t *testing.T) {
	project := t.TempDir()
	writeArticle(t, project, "global/articles/a.md", goodArticle)
	writeArticle(t, project, "projects/github.com/acme/app/articles/b.md", goodArticle)
	for name, dir := range map[string]string{
		"the examples":       contentDir(t),
		"a Project":          project,
		"an empty directory": t.TempDir(), // the server loads it too
	} {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "validate", dir)
			if code != 0 || stderr != "" {
				t.Errorf("exit %d, stderr %q, want exit 0 and no stderr\nstdout:\n%s", code, stderr, stdout)
			}
			noCrashOutput(t, stdout)
		})
	}
}

func TestValidateNeedsNoTokenOrAddress(t *testing.T) {
	// A shell holding the -dev export line, or a broken server setting, does
	// not change what validate checks.
	env := validateEnv("GOLDIE_ADDR=not-an-address", "GOLDIE_ADMIN_ADDR=also-bad", "GOLDIE_TOKEN=x")
	if _, stderr, code := runValidate(t, t.TempDir(), env, "validate", contentDir(t)); code != 0 {
		t.Errorf("exit %d, want 0; stderr:\n%s", code, stderr)
	}
}

func TestValidateNamesTheFileAndTheRule(t *testing.T) {
	cases := []struct {
		name, rel, body, wantFile, wantRule string
	}{
		{"no frontmatter", "global/articles/plain.md", "# Just a heading\n", "global/articles/plain.md", "missing frontmatter"},
		{"frontmatter not closed", "global/articles/open.md", "---\nname: Open\n", "global/articles/open.md", "frontmatter not closed"},
		{"no name", "global/articles/anon.md", "---\nkeywords: [a]\n---\nx\n", "global/articles/anon.md", "frontmatter has no name"},
		{"malformed yaml", "global/articles/bad.md", "---\nname: [unclosed\n---\nx\n", "global/articles/bad.md", "malformed frontmatter"},
		{"a Project article", "projects/github.com/acme/app/articles/p.md", "no frontmatter\n", "projects/github.com/acme/app/articles/p.md", "missing frontmatter"},
		{"nested article", "global/articles/sub/deep.md", "no frontmatter\n", "global/articles/sub/deep.md", "missing frontmatter"},
		{"too large", "global/articles/huge.md", "---\nname: Huge\n---\n" + strings.Repeat("x", 1<<20), "global/articles/huge.md", "larger than"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeArticle(t, dir, "global/articles/good.md", goodArticle)
			writeArticle(t, dir, c.rel, c.body)
			stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "validate", dir)
			reason := wantInvalid(t, stdout, stderr, code, dir)
			if !strings.Contains(reason, c.wantFile) || !strings.Contains(reason, c.wantRule) {
				t.Errorf("reason = %q, want it to name %q and the rule %q", reason, c.wantFile, c.wantRule)
			}
		})
	}
}

func TestValidateRejectsArticleIDsThatDifferOnlyInCase(t *testing.T) {
	dir := t.TempDir()
	writeArticle(t, dir, "global/articles/Alpha.md", goodArticle)
	writeArticle(t, dir, "global/articles/alpha.md", goodArticle)
	stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "validate", dir)
	reason := wantInvalid(t, stdout, stderr, code, dir)
	if !strings.Contains(reason, "differ only in case") || !strings.Contains(reason, "global/articles/") {
		t.Errorf("reason = %q, want both files and the rule", reason)
	}
}

func TestValidateUnusableDirectories(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "s3cret-no-such-dir")
	file := filepath.Join(base, "s3cret-a-file")
	if err := os.WriteFile(file, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{"missing directory": missing, "a file instead of a directory": file}
	if os.Geteuid() != 0 {
		locked := filepath.Join(base, "s3cret-locked")
		if err := os.Mkdir(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
		dirs["unreadable directory"] = locked
	}
	for name, dir := range dirs {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "validate", dir)
			reason := wantInvalid(t, stdout, stderr, code, dir, "s3cret", base)
			if reason != "content directory is not readable" {
				t.Errorf("reason = %q, want %q", reason, "content directory is not readable")
			}
		})
	}
}

func TestValidateUnreadableArticleNamesTheFileNotThePath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	dir := t.TempDir()
	writeArticle(t, dir, "global/articles/locked.md", goodArticle)
	p := filepath.Join(dir, "global", "articles", "locked.md")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "validate", dir)
	reason := wantInvalid(t, stdout, stderr, code, dir)
	if !strings.Contains(reason, "global/articles/locked.md") {
		t.Errorf("reason = %q, want it to name global/articles/locked.md", reason)
	}
}

// Several bad files still give one line: the first problem, as the server
// stops at.
func TestValidateReportsOneProblemOnOneLine(t *testing.T) {
	dir := t.TempDir()
	writeArticle(t, dir, "global/articles/a.md", "no frontmatter\n")
	writeArticle(t, dir, "global/articles/b.md", "no frontmatter\n")
	stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "validate", dir)
	wantInvalid(t, stdout, stderr, code, dir)
}

// Which directory: the argument, else GOLDIE_CONTENT_DIR, else the working
// directory (the order of goldie server -dev).
func TestValidateChoosesTheDirectory(t *testing.T) {
	good := t.TempDir()
	writeArticle(t, good, "global/articles/a.md", goodArticle)
	bad := t.TempDir()
	writeArticle(t, bad, "global/articles/a.md", "no frontmatter\n")

	t.Run("working directory when nothing is given", func(t *testing.T) {
		if _, stderr, code := runValidate(t, good, validateEnv(), "validate"); code != 0 {
			t.Errorf("valid working directory: exit %d, stderr %q", code, stderr)
		}
		stdout, stderr, code := runValidate(t, bad, validateEnv(), "validate")
		wantInvalid(t, stdout, stderr, code, bad)
	})
	t.Run("GOLDIE_CONTENT_DIR beats the working directory", func(t *testing.T) {
		if _, stderr, code := runValidate(t, bad, validateEnv("GOLDIE_CONTENT_DIR="+good), "validate"); code != 0 {
			t.Errorf("exit %d, stderr %q, want 0: the setting names valid content", code, stderr)
		}
		stdout, stderr, code := runValidate(t, good, validateEnv("GOLDIE_CONTENT_DIR="+bad), "validate")
		wantInvalid(t, stdout, stderr, code, bad)
	})
	t.Run("the argument beats GOLDIE_CONTENT_DIR", func(t *testing.T) {
		if _, stderr, code := runValidate(t, bad, validateEnv("GOLDIE_CONTENT_DIR="+bad), "validate", good); code != 0 {
			t.Errorf("exit %d, stderr %q, want 0", code, stderr)
		}
		stdout, stderr, code := runValidate(t, good, validateEnv("GOLDIE_CONTENT_DIR="+good), "validate", bad)
		wantInvalid(t, stdout, stderr, code, bad)
	})
	t.Run("a relative argument", func(t *testing.T) {
		parent := filepath.Dir(good)
		if _, stderr, code := runValidate(t, parent, validateEnv(), "validate", filepath.Base(good)); code != 0 {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
	})
}

func TestValidateTakesAtMostOneDir(t *testing.T) {
	d := contentDir(t)
	stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "validate", d, d)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "usage: goldie") || !strings.Contains(stderr, "validate [dir]") {
		t.Errorf("stderr = %q, want the usage naming %q", stderr, "validate [dir]")
	}
	noCrashOutput(t, stderr)
}

func TestUsageNamesValidate(t *testing.T) {
	_, stderr, code := runValidate(t, t.TempDir(), validateEnv())
	if code != 1 || !strings.Contains(stderr, "validate [dir]") {
		t.Errorf("exit %d, stderr %q, want exit 1 and a usage naming %q", code, stderr, "validate [dir]")
	}
}

// Grows by construction: validate has no rules of its own. What the server's
// load rejects, validate rejects with the same words, and when a file changes
// from valid to invalid (or back) validate follows. Any rule added to the
// loader (#269, #271, #272) is covered by the same comparison.
func TestValidateFollowsTheServersLoad(t *testing.T) {
	dir := t.TempDir()
	art := "global/articles/a.md"
	writeArticle(t, dir, "global/articles/b.md", goodArticle)

	states := []struct {
		name, body string
		valid      bool
	}{
		{"valid", goodArticle, true},
		{"frontmatter removed", "# no frontmatter\n", false},
		{"valid again", goodArticle, true},
		{"nameless", "---\nkeywords: [a]\n---\nx\n", false},
		{"unclosed", "---\nname: A\n", false},
	}
	for _, s := range states {
		writeArticle(t, dir, art, s.body)
		vOut, vErr, vCode := runValidate(t, t.TempDir(), validateEnv(), "validate", dir)
		if s.valid {
			if vCode != 0 || vErr != "" {
				t.Errorf("%s: validate exit %d, stderr %q, want 0", s.name, vCode, vErr)
			}
			continue
		}
		wantInvalid(t, vOut, vErr, vCode, dir)
		// server -dev stops at the same load error; it would run on valid content.
		_, sErr, sExit := runDevToExit(t, devEnv(), "server", "-dev", dir)
		if sExit == nil {
			t.Fatalf("%s: server -dev exited 0 on content validate rejects", s.name)
		}
		sLines := strings.Split(strings.TrimRight(sErr, "\n"), "\n")
		if last := sLines[len(sLines)-1]; last != strings.TrimRight(vErr, "\n") {
			t.Errorf("%s: validate says %q, the server's load says %q: they must be the same", s.name, strings.TrimRight(vErr, "\n"), last)
		}
	}
}
