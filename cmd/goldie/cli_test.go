package main

import (
	"bytes"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/platform"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

const cliToken = "cli-test-token-0123456789abcdef"

// cliServer is a real in-process server over the example content.
func cliServer(t *testing.T) *httptest.Server {
	t.Helper()
	h, err := app.PublicHandler(app.Options{ContentDir: "../../internal/app/testdata/content", APIToken: cliToken})
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// golden compares got with testdata/golden/<name>.golden. The server address
// varies per run, so it is replaced by <addr> first.
func golden(t *testing.T, name, got, addr string) {
	t.Helper()
	got = strings.ReplaceAll(got, addr, "<addr>")
	path := filepath.Join("testdata", "golden", name+".golden")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
	}
}

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	for _, name := range []string{"GOLDIE_ADDR", "GOLDIE_TOKEN", "GOLDIE_TOKEN_FILE"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), err
}

func TestCLIGolden(t *testing.T) {
	srv := cliServer(t)
	base := []string{"--addr", srv.URL, "--token", cliToken}
	cases := []struct {
		name string
		args []string
	}{
		{"search", []string{"search", "kubernetes"}},
		{"search_json", []string{"search", "agent loops", "--json"}},
		{"search_none", []string{"search", "zzzznothing"}},
		{"lookup", []string{"lookup", "platform/deploy"}},
		{"lookup_json", []string{"lookup", "platform/deploy", "--json"}},
		{"status", []string{"status"}},
		{"status_json", []string{"status", "--json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := runCLI(t, append(c.args, base...)...)
			if err != nil {
				t.Fatalf("run %v: %v", c.args, err)
			}
			golden(t, c.name, out, srv.URL)
		})
	}
}

func TestCLISearchEndsWithNextStepHints(t *testing.T) {
	srv := cliServer(t)
	out, err := runCLI(t, "search", "kubernetes", "--addr", srv.URL, "--token", cliToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, hint := range []string{"Read one with: goldie lookup <id>", "Try other words"} {
		if !strings.Contains(out, hint) {
			t.Errorf("search output lacks hint %q:\n%s", hint, out)
		}
	}
}

func TestCLIConfigFromEnvAndTokenFile(t *testing.T) {
	srv := cliServer(t)
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(cliToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOLDIE_ADDR", srv.URL)
	t.Setenv("GOLDIE_TOKEN_FILE", tokenFile)
	_ = os.Unsetenv("GOLDIE_TOKEN")
	var out bytes.Buffer
	if err := run([]string{"search", "agent"}, &out); err != nil {
		t.Fatalf("search with env config: %v", err)
	}
	if !strings.Contains(out.String(), "agent-loops") {
		t.Errorf("no hit in %q", out.String())
	}
}

func TestCLIErrors(t *testing.T) {
	srv := cliServer(t)
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"wrong token", []string{"search", "agent", "--addr", srv.URL, "--token", "wrong-token-value"}, []string{"401", "Hint:"}},
		{"not found", []string{"lookup", "no/such", "--addr", srv.URL, "--token", cliToken}, []string{"404", "Hint:", "search"}},
		{"no addr", []string{"search", "agent", "--token", cliToken}, []string{"GOLDIE_ADDR"}},
		{"no token", []string{"search", "agent", "--addr", srv.URL}, []string{"GOLDIE_TOKEN"}},
		{"bad addr", []string{"search", "agent", "--addr", "ftp://x", "--token", cliToken}, []string{"http"}},
		{"unreachable", []string{"search", "agent", "--addr", "http://127.0.0.1:1", "--token", cliToken}, []string{"cannot reach"}},
		{"no words", []string{"search", "--addr", srv.URL, "--token", cliToken}, []string{"usage"}},
		{"status spec not served", []string{"status", "--addr", statusStub(t, http.StatusServiceUnavailable, http.StatusOK).URL}, []string{"503"}},
		{"status token check fails", []string{"status", "--addr", statusStub(t, http.StatusOK, http.StatusInternalServerError).URL, "--token", cliToken}, []string{"500"}},
		{"unknown flag", []string{"search", "agent", "--bogus", "--addr", srv.URL, "--token", cliToken}, []string{"usage"}},
		{"lookup two ids", []string{"lookup", "a", "b", "--addr", srv.URL, "--token", cliToken}, []string{"usage"}},
		{"status extra arg", []string{"status", "extra", "--addr", srv.URL}, []string{"usage"}},
		{"status bad addr", []string{"status", "--addr", "ftp://x"}, []string{"http"}},
		{"status unreachable", []string{"status", "--addr", "http://127.0.0.1:1"}, []string{"cannot reach"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := runCLI(t, c.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
			if strings.Contains(err.Error(), cliToken) || strings.Contains(err.Error(), "wrong-token-value") || strings.Contains(err.Error(), "127.0.0.1") {
				t.Errorf("error leaks a token or address: %q", err)
			}
		})
	}
}

// A token file that cannot be read stops search with an error that names the
// variable, not the path.
func TestCLISearchMissingTokenFile(t *testing.T) {
	srv := cliServer(t)
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("GOLDIE_TOKEN_FILE", missing)
	_ = os.Unsetenv("GOLDIE_TOKEN")
	var out bytes.Buffer
	err := run([]string{"search", "agent", "--addr", srv.URL}, &out)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "GOLDIE_TOKEN_FILE") || strings.Contains(err.Error(), missing) {
		t.Errorf("error = %q, want it to name GOLDIE_TOKEN_FILE and not the path", err)
	}
}

func TestCLIStatusRejectedToken(t *testing.T) {
	srv := cliServer(t)
	out, err := runCLI(t, "status", "--addr", srv.URL, "--token", "wrong-token-value")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "token rejected") {
		t.Errorf("status = %q, want it to say the token was rejected", out)
	}
}

// statusStub answers /openapi.json with specCode (a minimal document when 200)
// and every other path, the token check, with searchCode.
func statusStub(t *testing.T, specCode, searchCode int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/openapi.json" {
			w.WriteHeader(specCode)
			if specCode == http.StatusOK {
				_, _ = w.Write([]byte(`{"info":{"title":"Stub","version":"1"}}`))
			} else {
				_, _ = w.Write([]byte(`{}`))
			}
			return
		}
		w.WriteHeader(searchCode)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCLIStatusWithoutToken(t *testing.T) {
	srv := cliServer(t)
	out, err := runCLI(t, "status", "--addr", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no token given") {
		t.Errorf("status = %q, want it to say no token was given", out)
	}
	out, err = runCLI(t, "status", "--addr", srv.URL, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"tokenAccepted": false`) && !strings.Contains(out, `"tokenAccepted":false`) {
		t.Errorf("status --json = %q, want tokenAccepted false", out)
	}
}

func TestCLIVersionAgainstServer(t *testing.T) {
	srv := cliServer(t)
	out, err := runCLI(t, "version", "--addr", srv.URL, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"serverApiVersion"`) || !strings.Contains(out, `"version"`) {
		t.Errorf("version --json = %q", out)
	}
}

// GOLDIE_ADDR is also the server's listen address, so `goldie version` must
// still print the client version where it holds ":8080", or where the token
// settings are broken; version needs neither.
func TestCLIVersionIgnoresUnusableClientSettings(t *testing.T) {
	for _, env := range [][2]string{
		{"GOLDIE_ADDR", ":8080"},
		{"GOLDIE_TOKEN_FILE", filepath.Join(t.TempDir(), "missing")},
	} {
		t.Run(env[0], func(t *testing.T) {
			t.Setenv(env[0], env[1])
			var out bytes.Buffer
			if err := run([]string{"version"}, &out); err != nil {
				t.Fatalf("version with %s=%q: %v", env[0], env[1], err)
			}
			if got := strings.TrimSpace(out.String()); got != platform.Version {
				t.Errorf("version = %q, want %q", got, platform.Version)
			}
		})
	}
}

func TestCLIStripsTerminalEscapesFromServerText(t *testing.T) {
	evil := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","name":"N\u001b[31mRED","keywords":["k\u0007"],"content":"body\u001b]0;pwn\u0007"}`))
	})
	srv := httptest.NewServer(evil)
	defer srv.Close()
	out, err := runCLI(t, "lookup", "x", "--addr", srv.URL, "--token", cliToken)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out, 0x1b) || strings.ContainsRune(out, 0x07) {
		t.Errorf("output carries control characters: %q", out)
	}
}
