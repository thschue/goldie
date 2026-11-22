package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests cover --dir and GOLDIE_DIR: search, lookup, status and mcp start
// an embedded server over a directory of content, so goldie works without a
// server started by hand. The CLI never resolves anything itself, so the
// output must be the output of a remote server over the same content.

// dirContent is the content the remote test server (cliServer) also serves,
// as an absolute path because the runners change the working directory.
func dirContent(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs(filepath.Join("..", "..", "internal", "app", "testdata", "content"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// runWithDirEnv runs the CLI in an empty directory with the other client
// variables cleared and GOLDIE_DIR set to dirEnv.
func runWithDirEnv(t *testing.T, dirEnv string, args ...string) (string, error) {
	t.Helper()
	for _, name := range []string{"GOLDIE_ADDR", "GOLDIE_TOKEN", "GOLDIE_TOKEN_FILE", "GOLDIE_PROJECT"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	t.Setenv("GOLDIE_DIR", dirEnv)
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), err
}

// The same command over the same content prints the same bytes in local and
// remote mode: text and --json, an answer and the empty result.
func TestCLIDirOutputMatchesRemoteMode(t *testing.T) {
	srv := cliServer(t)
	content := dirContent(t)
	cases := []struct {
		name string
		args []string
		want string // proof the output is a real answer, not two equal errors
	}{
		{"search", []string{"search", "kubernetes"}, "platform/deploy"},
		{"search json", []string{"search", "agent loops", "--json"}, "agent-loops"},
		{"search none", []string{"search", "zzzznothing"}, ""},
		{"lookup", []string{"lookup", "platform/deploy"}, "deploy"},
		{"lookup json", []string{"lookup", "platform/deploy", "--json"}, "platform/deploy"},
		{"status", []string{"status"}, "Server: reachable"},
		{"status json", []string{"status", "--json"}, "tokenAccepted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			remote, err := runCLI(t, append(append([]string{}, c.args...), "--addr", srv.URL, "--token", cliToken)...)
			if err != nil {
				t.Fatalf("remote %v: %v", c.args, err)
			}
			local, err := runCLI(t, append(append([]string{}, c.args...), "--dir", content)...)
			if err != nil {
				t.Fatalf("local %v: %v", c.args, err)
			}
			if local != strings.ReplaceAll(remote, srv.URL, "") {
				t.Errorf("local output differs from remote\n--- local ---\n%s\n--- remote ---\n%s", local, remote)
			}
			if !strings.Contains(local, c.want) {
				t.Errorf("local output lacks %q:\n%s", c.want, local)
			}
		})
	}
}

// GOLDIE_DIR does what --dir does, and the flag overrides the variable, as
// --addr overrides GOLDIE_ADDR.
func TestCLIDirFromTheEnvironment(t *testing.T) {
	content := dirContent(t)
	want, err := runCLI(t, "search", "kubernetes", "--dir", content)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(want, "platform/deploy") {
		t.Fatalf("--dir search lacks the article:\n%s", want)
	}
	got, err := runWithDirEnv(t, content, "search", "kubernetes")
	if err != nil || got != want {
		t.Errorf("GOLDIE_DIR: err = %v, output\n%s\nwant\n%s", err, got, want)
	}
	got, err = runWithDirEnv(t, filepath.Join(t.TempDir(), "does-not-exist"), "search", "kubernetes", "--dir", content)
	if err != nil || got != want {
		t.Errorf("--dir over GOLDIE_DIR: err = %v, output\n%s\nwant\n%s", err, got, want)
	}
}

// A token in the environment belongs to a remote server. The embedded server
// makes its own, so a wrong GOLDIE_TOKEN does not break local mode.
func TestCLIDirIgnoresTheRemoteToken(t *testing.T) {
	content := dirContent(t)
	want, err := runCLI(t, "status", "--dir", content)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(want, "Auth: token accepted") {
		t.Fatalf("status --dir lacks the accepted token:\n%s", want)
	}
	t.Setenv("GOLDIE_TOKEN", "wrong-token-0123456789abcdef0123456789")
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	err = run([]string{"status", "--dir", content}, &out)
	if err != nil || out.String() != want {
		t.Errorf("status --dir with a wrong GOLDIE_TOKEN: err = %v, output\n%s\nwant\n%s", err, out.String(), want)
	}
}

// Both --dir and --addr is an error that names both, for every command that
// takes them, before anything is printed.
func TestCLIDirWithAddrIsAnError(t *testing.T) {
	srv := cliServer(t)
	content := dirContent(t)
	for _, cmd := range [][]string{{"search", "kubernetes"}, {"lookup", "platform/deploy"}, {"status"}} {
		t.Run(cmd[0], func(t *testing.T) {
			out, err := runCLI(t, append(append([]string{}, cmd...), "--dir", content, "--addr", srv.URL, "--token", cliToken)...)
			if err == nil {
				t.Fatalf("--dir with --addr succeeded:\n%s", out)
			}
			for _, name := range []string{"--dir", "--addr"} {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error %q does not name %s", err, name)
				}
			}
			if out != "" {
				t.Errorf("printed output before failing:\n%s", out)
			}
			if strings.Contains(err.Error(), cliToken) || strings.Contains(err.Error(), srv.URL) {
				t.Errorf("error shows the token or the address: %v", err)
			}
		})
	}
}

// GOLDIE_DIR together with --addr is the same conflict, and it names the flag
// and the variable that clash.
func TestCLIDirFromTheEnvironmentWithAddrIsAnError(t *testing.T) {
	srv := cliServer(t)
	out, err := runWithDirEnv(t, dirContent(t), "search", "kubernetes", "--addr", srv.URL, "--token", cliToken)
	if err == nil {
		t.Fatalf("GOLDIE_DIR with --addr succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--addr") || !strings.Contains(err.Error(), "GOLDIE_DIR") {
		t.Errorf("error %q does not name --addr and GOLDIE_DIR", err)
	}
}

// Content that does not load stops the command with an error and no output.
// The error does not echo the directory, as the server's own error does not.
func TestCLIDirWithUnusableContentFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-content-dir")
	bad := t.TempDir()
	// Only global/articles/** and projects/<host>/<org>/<repo>/articles/** are
	// loaded, so the broken article must live there to be read at all.
	if err := os.MkdirAll(filepath.Join(bad, "global", "articles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "global", "articles", "broken.md"), []byte("---\ntitle: [unclosed\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"missing": missing, "invalid article": bad} {
		t.Run(name, func(t *testing.T) {
			out, err := runCLI(t, "search", "kubernetes", "--dir", dir)
			if err == nil {
				t.Fatalf("--dir %s succeeded:\n%s", name, out)
			}
			if out != "" {
				t.Errorf("printed output before failing:\n%s", out)
			}
			if !strings.Contains(err.Error(), "content does not load") {
				t.Errorf("error %q does not say the content does not load", err)
			}
			if strings.Contains(err.Error(), dir) {
				t.Errorf("error echoes the directory: %v", err)
			}
		})
	}
}

// The usage names --dir, so it is discoverable.
func TestCLIUsageNamesDir(t *testing.T) {
	err := run(nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--dir") {
		t.Errorf("usage = %v, want it to name --dir", err)
	}
}

// goldie mcp --transport stdio --dir starts the embedded server and serves both
// tools over stdio, without an address or a token.
func TestMCPStdioDirServesBothTools(t *testing.T) {
	content := dirContent(t)
	cases := []struct {
		name string
		env  []string
		args []string
	}{
		{"flag", nil, []string{"--dir", content}},
		{"GOLDIE_DIR", []string{"GOLDIE_DIR=" + content}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cs, stderr := stdioSession(t, c.env, c.args...)
			assertBothToolsAnswer(t, cs)
			if s := stderr.String(); strings.Contains(s, content) {
				t.Errorf("stderr shows the directory:\n%s", s)
			}
		})
	}
}

// mcp with both --dir and --addr fails at startup, naming both, on stderr.
func TestMCPStdioDirWithAddrFailsAtStartup(t *testing.T) {
	srv := cliServer(t)
	stdout, stderr := runMCPExpectingFailure(t, nil, []string{"--dir", dirContent(t), "--addr", srv.URL, "--token", cliToken})
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing: it carries only the protocol", stdout)
	}
	for _, name := range []string{"--dir", "--addr"} {
		if !strings.Contains(stderr, name) {
			t.Errorf("stderr does not name %s:\n%s", name, stderr)
		}
	}
	if strings.Contains(stderr, cliToken) {
		t.Errorf("stderr shows the token:\n%s", stderr)
	}
}

// The real binary, with only PATH and GOLDIE_DIR set, answers a lookup and
// exits: no token or address is needed and nothing is left running.
func TestBinaryDirAnswersAndExits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goldieBinary(t), "lookup", "platform/deploy")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GOLDIE_DIR=" + dirContent(t)}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("goldie lookup with GOLDIE_DIR: %v\nstderr:\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "platform/deploy") {
		t.Errorf("lookup output lacks the article:\n%s", stdout.String())
	}
}
