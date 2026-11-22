package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// These tests cover how --dir and GOLDIE_DIR meet GOLDIE_ADDR from the
// environment, and that version never starts an embedded server.

// runWithEnv runs the CLI in an empty directory with the client variables
// cleared and then set to env, so a test controls every one of them.
func runWithEnv(t *testing.T, env map[string]string, args ...string) (string, error) {
	t.Helper()
	for _, name := range []string{"GOLDIE_ADDR", "GOLDIE_TOKEN", "GOLDIE_TOKEN_FILE", "GOLDIE_PROJECT", "GOLDIE_DIR"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	for name, v := range env {
		t.Setenv(name, v)
	}
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), err
}

// GOLDIE_DIR and GOLDIE_ADDR together, with no flags, is the same conflict as
// the flags: an error that names both variables, prints nothing and shows no
// token, address or directory.
func TestCLIDirAndAddrBothFromTheEnvironmentIsAnError(t *testing.T) {
	content := dirContent(t)
	const addr = "http://remote-secret-host.invalid:4711"
	env := map[string]string{"GOLDIE_DIR": content, "GOLDIE_ADDR": addr, "GOLDIE_TOKEN": cliToken}
	for _, cmd := range [][]string{{"search", "kubernetes"}, {"lookup", "platform/deploy"}, {"status"}} {
		t.Run(cmd[0], func(t *testing.T) {
			out, err := runWithEnv(t, env, cmd...)
			if err == nil {
				t.Fatalf("GOLDIE_DIR with GOLDIE_ADDR succeeded:\n%s", out)
			}
			for _, name := range []string{"GOLDIE_DIR", "GOLDIE_ADDR"} {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error %q does not name %s", err, name)
				}
			}
			if out != "" {
				t.Errorf("printed output before failing:\n%s", out)
			}
			for _, secret := range []string{cliToken, addr, "remote-secret-host", content} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error shows %q: %v", secret, err)
				}
			}
		})
	}
}

// An explicit --dir wins over GOLDIE_ADDR from the environment, as a flag
// overrides a variable: the command runs on local content and the remote server
// is never contacted.
func TestCLIDirFlagOverridesAddrFromTheEnvironment(t *testing.T) {
	content := dirContent(t)
	var hits atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "must not be called", http.StatusServiceUnavailable)
	}))
	t.Cleanup(remote.Close)

	for _, cmd := range [][]string{{"search", "kubernetes"}, {"status"}} {
		t.Run(cmd[0], func(t *testing.T) {
			args := append(append([]string{}, cmd...), "--dir", content)
			want, err := runWithEnv(t, nil, args...)
			if err != nil {
				t.Fatal(err)
			}
			if cmd[0] == "search" && !strings.Contains(want, "platform/deploy") {
				t.Fatalf("--dir search lacks the article:\n%s", want)
			}
			got, err := runWithEnv(t, map[string]string{"GOLDIE_ADDR": remote.URL, "GOLDIE_TOKEN": cliToken}, args...)
			if err != nil || got != want {
				t.Errorf("--dir with GOLDIE_ADDR: err = %v, output\n%s\nwant\n%s", err, got, want)
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the server at GOLDIE_ADDR received %d requests, want 0", n)
	}
}

// version has no use for local content: with --dir or GOLDIE_DIR set to a
// directory that does not load, it starts no server, does not fail and prints
// what it prints without them.
func TestCLIVersionIgnoresDir(t *testing.T) {
	want, err := runWithEnv(t, nil, "version")
	if err != nil || want == "" {
		t.Fatalf("version: err = %v, output %q", err, want)
	}
	missing := filepath.Join(t.TempDir(), "no-such-content-dir")
	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, "global", "articles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "global", "articles", "broken.md"), []byte("---\ntitle: [unclosed\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"missing": missing, "invalid article": bad} {
		for _, via := range []string{"flag", "env"} {
			t.Run(name+" "+via, func(t *testing.T) {
				var got string
				var err error
				if via == "flag" {
					got, err = runWithEnv(t, nil, "version", "--dir", dir)
				} else {
					got, err = runWithEnv(t, map[string]string{"GOLDIE_DIR": dir}, "version")
				}
				if err != nil {
					t.Fatalf("version with a bad directory failed: %v", err)
				}
				if got != want {
					t.Errorf("version output differs\n--- got ---\n%s\n--- want ---\n%s", got, want)
				}
				if strings.Contains(got, dir) {
					t.Errorf("output echoes the directory:\n%s", got)
				}
			})
		}
	}
}
