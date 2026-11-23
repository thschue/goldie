package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thschue/goldie/internal/cli"
)

// Tests for #282 after #300: goldie init checks registration only against a
// running server, so like version it ignores --dir and GOLDIE_DIR. It never
// starts the embedded server, never reports the --dir/--addr conflict, and
// still writes .goldie/config.yaml. They run at the cli.Run boundary because
// only Env.StartLocal shows whether the embedded server was started.

const initDirToken = "init-dir-test-token-0123456789abcdef0123"

// initDirAPI serves GET /api/v1/projects to initDirToken and counts requests.
func initDirAPI(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+initDirToken || r.URL.Path != "/api/v1/projects" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"no","details":{}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"projects":["github.com/Acme/Widgets"]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func TestInitIgnoresLocalMode(t *testing.T) {
	api, requests := initDirAPI(t)
	content := t.TempDir()
	const notChecked = "Registered: not checked (no server address: set --addr or GOLDIE_ADDR)\n"
	cases := []struct {
		name     string
		env      map[string]string
		args     []string
		line     string
		requests int
	}{
		{"--dir", nil, []string{"--dir", content}, notChecked, 0},
		{"GOLDIE_DIR", map[string]string{"GOLDIE_DIR": content}, nil, notChecked, 0},
		{"GOLDIE_DIR and GOLDIE_ADDR", map[string]string{"GOLDIE_DIR": content, "GOLDIE_ADDR": api.URL, "GOLDIE_TOKEN": initDirToken}, nil,
			"Registered: yes\n", 1},
		{"--dir and --addr", nil, []string{"--dir", content, "--addr", api.URL, "--token", initDirToken},
			"Registered: yes\n", 1},
		{"--dir and GOLDIE_ADDR", map[string]string{"GOLDIE_ADDR": api.URL, "GOLDIE_TOKEN": initDirToken}, []string{"--dir", content},
			"Registered: yes\n", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:acme/widgets.git\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(root)
			var out bytes.Buffer
			env := cli.Env{
				Lookup:   func(k string) (string, bool) { v, ok := c.env[k]; return v, ok },
				ReadFile: os.ReadFile,
				Out:      &out,
				StartLocal: func(context.Context, *os.Root) (string, string, func(), error) {
					t.Error("init started the embedded server")
					return "", "", func() {}, context.Canceled
				},
			}
			before := len(requests())
			err := cli.Run(context.Background(), append([]string{"init"}, c.args...), env)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			want := "Project: github.com/acme/widgets (from git remote origin)\n" +
				"Wrote: " + filepath.Join(root, ".goldie", "config.yaml") + "\n" + c.line
			if got := out.String(); got != want && strings.ReplaceAll(got, mustEval(t, root), root) != want {
				t.Errorf("output:\n%s\nwant:\n%s", got, want)
			}
			b, err := os.ReadFile(filepath.Join(root, ".goldie", "config.yaml"))
			if err != nil || string(b) != "project: github.com/acme/widgets\n" {
				t.Errorf("config.yaml = %q, %v", b, err)
			}
			if n := len(requests()) - before; n != c.requests {
				t.Errorf("requests to the --addr server = %d, want %d", n, c.requests)
			}
		})
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
