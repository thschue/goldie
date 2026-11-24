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
	"unicode"

	"github.com/thschue/goldie/internal/cli"
)

// Acceptance tests for #799: goldie init guides the user on a terminal. A
// terminal is an Env.In reader; without one init is what it was. They run at
// the cli.Run boundary, next to init_dir_test.go.

const (
	guidedToken = "s3cret"
	guidedID    = "github.com/acme/widgets"
	guidedLine  = "Project: " + guidedID + " (from git remote origin)\n"
)

// guidedRepo is a git repository whose origin names acme/widgets, entered with
// t.Chdir.
func guidedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:acme/widgets.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

// guidedServer accepts guidedToken. It answers /openapi.json to anyone,
// GET /api/v1/projects only to the token, and lists projects. hostile makes it
// answer every request with an error whose message carries control characters.
type guidedServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func (g *guidedServer) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.seen...)
}

func newGuidedServer(t *testing.T, projects string, hostile bool) *guidedServer {
	t.Helper()
	g := &guidedServer{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.seen = append(g.seen, r.Method+" "+r.URL.Path)
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if hostile {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"boom","message":"bad\u001b[2J\u0007\u0085\u202e\r\nForged: line","details":{}}}`))
			return
		}
		auth := r.Header.Get("Authorization")
		switch {
		case auth != "" && auth != "Bearer "+guidedToken,
			r.URL.Path == "/api/v1/projects" && auth == "":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"no","details":{}}}`))
		case r.URL.Path == "/openapi.json":
			_, _ = w.Write([]byte(`{"info":{"title":"Goldie","version":"1"}}`))
		case r.URL.Path == "/api/v1/projects":
			_, _ = w.Write([]byte(`{"projects":[` + projects + `]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no","details":{}}}`))
		}
	}))
	t.Cleanup(g.Close)
	return g
}

// runInit runs goldie init in the current directory with in as the terminal
// (nil: no terminal) and returns stdout, stderr and the error.
func runInit(in *strings.Reader, env map[string]string, args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	e := cli.Env{
		Lookup:   func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		ReadFile: os.ReadFile,
		Out:      &out,
		Err:      &errOut,
	}
	if in != nil {
		e.In = in
	}
	err = cli.Run(context.Background(), append([]string{"init"}, args...), e)
	return out.String(), errOut.String(), err
}

func wroteConfig(t *testing.T, root string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".goldie", "config.yaml"))
	if err != nil {
		return false
	}
	if string(b) != "project: "+guidedID+"\n" {
		t.Errorf("config.yaml = %q", b)
	}
	return true
}

func hasLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if l == line {
			return true
		}
	}
	return false
}

// 1. Interactive only on a terminal.
func TestInitWithoutAnswersIsUnchanged(t *testing.T) {
	for name, in := range map[string]*strings.Reader{"no terminal": nil, "EOF at the first prompt": strings.NewReader("")} {
		t.Run(name, func(t *testing.T) {
			root := guidedRepo(t)
			stdout, stderr, err := runInit(in, nil)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			want := guidedLine + "Wrote: " + filepath.Join(root, ".goldie", "config.yaml") + "\n" +
				"Registered: not checked (no server address: set --addr or GOLDIE_ADDR)\n"
			if stdout != want && strings.ReplaceAll(stdout, mustEval(t, root), root) != want {
				t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
			if !wroteConfig(t, root) {
				t.Error("config.yaml was not written")
			}
		})
	}
}

func TestDefaultEnvReadsStdinOnlyOnACharDevice(t *testing.T) {
	old := os.Stdin
	t.Cleanup(func() { os.Stdin = old })

	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	os.Stdin = null
	if cli.DefaultEnv(&bytes.Buffer{}).In == nil {
		t.Error("stdin is a char device: Env.In is nil, want a reader")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	os.Stdin = r
	if cli.DefaultEnv(&bytes.Buffer{}).In != nil {
		t.Error("stdin is a pipe: Env.In is set, want nil")
	}
}

// 2. Refusals first.
func TestInitRefusesBeforeAnyPrompt(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T)
		want  string
	}{
		{"not a repository", func(t *testing.T) { t.Chdir(t.TempDir()) }, "not inside a git repository"},
		{"Source repository", func(t *testing.T) {
			d := guidedRepo(t)
			if err := os.MkdirAll(filepath.Join(d, "global", "articles"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "this is a Source repository"},
		{"no Project", func(t *testing.T) {
			d := t.TempDir()
			if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(d)
		}, "no Project found"},
		{"config exists", func(t *testing.T) {
			d := guidedRepo(t)
			if err := os.MkdirAll(filepath.Join(d, ".goldie"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d, ".goldie", "config.yaml"), []byte("project: github.com/other/repo\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, ".goldie/config.yaml already exists"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.setup(t)
			const answers = "y\n\n\n"
			in := strings.NewReader(answers)
			stdout, stderr, err := runInit(in, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want one with %q", err, c.want)
			}
			if stdout != "" || stderr != "" {
				t.Errorf("a refusal printed before it refused:\nstdout: %q\nstderr: %q", stdout, stderr)
			}
			if in.Len() != len(answers) {
				t.Error("init read an answer before it refused")
			}
		})
	}
}

// 3. Confirm the Project.
func TestInitConfirmsTheProject(t *testing.T) {
	for name, answer := range map[string]string{"Enter": "\n", "y": "y\n"} {
		t.Run("accept with "+name, func(t *testing.T) {
			root := guidedRepo(t)
			stdout, stderr, err := runInit(strings.NewReader(answer+"\n"), nil)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			if !strings.Contains(stdout, guidedLine) || !strings.Contains(stdout, "Wrote: ") {
				t.Errorf("stdout:\n%s", stdout)
			}
			if !strings.Contains(stderr, "Use this Project? [Y/n]") {
				t.Errorf("stderr lacks the question:\n%s", stderr)
			}
			if !wroteConfig(t, root) {
				t.Error("config.yaml was not written")
			}
		})
	}
	t.Run("decline", func(t *testing.T) {
		root := guidedRepo(t)
		stdout, stderr, err := runInit(strings.NewReader("n\n"), nil)
		if err == nil || !strings.Contains(err.Error(), "Name it: goldie init --project github.com/<org>/<repo>") {
			t.Fatalf("error = %v, want the line that names the flag", err)
		}
		if !strings.Contains(stdout, guidedLine) || !strings.Contains(stderr, "Use this Project? [Y/n]") {
			t.Errorf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(stdout, "Wrote:") {
			t.Errorf("stdout says it wrote a file:\n%s", stdout)
		}
		if _, err := os.Stat(filepath.Join(root, ".goldie")); !os.IsNotExist(err) {
			t.Errorf(".goldie exists after n: %v", err)
		}
	})
}

// 4. Server address.
func TestInitServerAddress(t *testing.T) {
	srv := newGuidedServer(t, `"`+guidedID+`"`, false)
	other := newGuidedServer(t, `"`+guidedID+`"`, false)

	t.Run("default from --addr", func(t *testing.T) {
		guidedRepo(t)
		_, stderr, err := runInit(strings.NewReader("\n\n"), nil, "--addr", srv.URL)
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		if !strings.Contains(stderr, "goldie server address ["+srv.URL+"]:") ||
			!hasLine(stderr, "Server: "+srv.URL+" (reachable)") {
			t.Errorf("stderr:\n%s", stderr)
		}
	})
	t.Run("default from GOLDIE_ADDR", func(t *testing.T) {
		guidedRepo(t)
		_, stderr, err := runInit(strings.NewReader("\n\n"), map[string]string{"GOLDIE_ADDR": srv.URL})
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		if !strings.Contains(stderr, "goldie server address ["+srv.URL+"]:") ||
			!hasLine(stderr, "Server: "+srv.URL+" (reachable)") {
			t.Errorf("stderr:\n%s", stderr)
		}
	})
	t.Run("--addr wins over GOLDIE_ADDR", func(t *testing.T) {
		guidedRepo(t)
		_, stderr, err := runInit(strings.NewReader("\n\n"), map[string]string{"GOLDIE_ADDR": other.URL}, "--addr", srv.URL)
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		if !strings.Contains(stderr, "["+srv.URL+"]:") || strings.Contains(stderr, other.URL) {
			t.Errorf("stderr:\n%s", stderr)
		}
	})
	t.Run("typed address", func(t *testing.T) {
		guidedRepo(t)
		_, stderr, err := runInit(strings.NewReader("\n"+srv.URL+"\n\n"), nil)
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		if !hasLine(stderr, "Server: "+srv.URL+" (reachable)") {
			t.Errorf("stderr:\n%s", stderr)
		}
	})
	t.Run("no answer and no default", func(t *testing.T) {
		guidedRepo(t)
		_, stderr, err := runInit(strings.NewReader("\n\n"), nil)
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		if !hasLine(stderr, "Server: not set") ||
			!strings.Contains(stderr, "Hint: start one on this machine with goldie server --dev") {
			t.Errorf("stderr:\n%s", stderr)
		}
	})
	t.Run("not reachable", func(t *testing.T) {
		gone := httptest.NewServer(http.NotFoundHandler())
		addr := gone.URL
		gone.Close()
		guidedRepo(t)
		_, stderr, err := runInit(strings.NewReader("\n\n"), nil, "--addr", addr)
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		if !strings.Contains(stderr, "Server: "+addr+" (not reachable: ") || strings.Contains(stderr, "(reachable)") {
			t.Errorf("stderr:\n%s", stderr)
		}
	})
	t.Run("redirects are not followed", func(t *testing.T) {
		target := newGuidedServer(t, `"`+guidedID+`"`, false)
		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
		}))
		t.Cleanup(redirector.Close)
		guidedRepo(t)
		_, stderr, err := runInit(strings.NewReader("\n\n"), map[string]string{"GOLDIE_TOKEN": guidedToken}, "--addr", redirector.URL)
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		if n := len(target.requests()); n != 0 {
			t.Errorf("the redirect target got %d requests: %v", n, target.requests())
		}
		if strings.Contains(stderr, "(reachable)") {
			t.Errorf("stderr:\n%s", stderr)
		}
	})
	t.Run("probed once", func(t *testing.T) {
		s := newGuidedServer(t, `"`+guidedID+`"`, false)
		guidedRepo(t)
		if _, _, err := runInit(strings.NewReader("\n\n"), nil, "--addr", s.URL); err != nil {
			t.Fatalf("init: %v", err)
		}
		n := 0
		for _, r := range s.requests() {
			if r == "GET /openapi.json" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("GET /openapi.json sent %d times, want 1: %v", n, s.requests())
		}
	})
}

// 5. Token, from the environment only.
func TestInitTokenStates(t *testing.T) {
	srv := newGuidedServer(t, `"`+guidedID+`"`, false)
	cases := []struct {
		name  string
		env   map[string]string
		wants []string
	}{
		{"accepted", map[string]string{"GOLDIE_TOKEN": guidedToken}, []string{"Token: accepted (from GOLDIE_TOKEN)"}},
		{"rejected", map[string]string{"GOLDIE_TOKEN": "wrong-token"}, []string{"Token: rejected"}},
		{"not set", nil, []string{"Token: not set", "Hint: goldie server --dev prints one; otherwise ask your platform team"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			guidedRepo(t)
			stdout, stderr, err := runInit(strings.NewReader("\n"+srv.URL+"\n"), c.env)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			for _, w := range c.wants {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr lacks %q:\n%s", w, stderr)
				}
			}
			for _, tok := range c.env {
				if strings.Contains(stdout+stderr, tok) {
					t.Errorf("the token is in the output:\nstdout: %s\nstderr: %s", stdout, stderr)
				}
			}
		})
	}
}

func TestInitNeverPrintsTheToken(t *testing.T) {
	srv := newGuidedServer(t, `"`+guidedID+`"`, false)
	root := guidedRepo(t)
	stdout, stderr, err := runInit(strings.NewReader("\n"+srv.URL+"\n"), map[string]string{"GOLDIE_TOKEN": guidedToken})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if strings.Contains(stdout, guidedToken) || strings.Contains(stderr, guidedToken) {
		t.Errorf("s3cret is in the output:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".goldie", "config.yaml")); strings.Contains(string(b), guidedToken) {
		t.Error("s3cret is in config.yaml")
	}
}

// 6. Registered.
func TestInitRegisteredLines(t *testing.T) {
	env := map[string]string{"GOLDIE_TOKEN": guidedToken}
	t.Run("registered", func(t *testing.T) {
		srv := newGuidedServer(t, `"`+guidedID+`"`, false)
		guidedRepo(t)
		stdout, _, err := runInit(strings.NewReader("\n\n"), env, "--addr", srv.URL)
		if err != nil || !hasLine(stdout, "Registered: yes") {
			t.Errorf("err = %v, stdout:\n%s", err, stdout)
		}
	})
	t.Run("loopback hint", func(t *testing.T) {
		srv := newGuidedServer(t, `"github.com/other/repo"`, false)
		guidedRepo(t)
		stdout, _, err := runInit(strings.NewReader("\n\n"), env, "--addr", srv.URL)
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		if !hasLine(stdout, "Registered: no") ||
			!hasLine(stdout, "Hint: To register it, add a projects/"+guidedID+"/ directory to the content directory and restart goldie server --dev") {
			t.Errorf("stdout:\n%s", stdout)
		}
	})
}

// 7. Next block on stdout.
func TestInitNextBlock(t *testing.T) {
	srv := newGuidedServer(t, `"`+guidedID+`"`, false)
	tail := []string{
		`claude mcp add --transport http goldie ` + srv.URL + `/mcp --header "Authorization: Bearer $GOLDIE_TOKEN"`,
		"goldie instructions apply",
		"goldie status",
		`git add .goldie/config.yaml && git commit -m "Add goldie config"`,
	}
	run := func(t *testing.T, env map[string]string) (string, string) {
		t.Helper()
		guidedRepo(t)
		stdout, stderr, err := runInit(strings.NewReader("\n"+srv.URL+"\n"), env)
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		return stdout, stderr
	}
	inOrder := func(t *testing.T, stdout string, lines []string) {
		t.Helper()
		got := strings.Split(stdout, "\n")
		pos := -1
		for _, l := range lines {
			next := -1
			for n := pos + 1; n < len(got); n++ {
				if got[n] == l {
					next = n
					break
				}
			}
			if next < 0 {
				t.Errorf("stdout lacks the line %q after line %d:\n%s", l, pos, stdout)
				return
			}
			pos = next
		}
	}
	t.Run("token set", func(t *testing.T) {
		stdout, stderr := run(t, map[string]string{"GOLDIE_TOKEN": guidedToken})
		inOrder(t, stdout, append([]string{"export GOLDIE_ADDR=" + srv.URL}, tail...))
		if strings.Contains(stdout, "export GOLDIE_TOKEN") {
			t.Errorf("Next tells the user to export a token they have set:\n%s", stdout)
		}
		if strings.Contains(stderr, "claude mcp add") {
			t.Errorf("Next is on stderr:\n%s", stderr)
		}
	})
	t.Run("token not set", func(t *testing.T) {
		stdout, _ := run(t, nil)
		inOrder(t, stdout, append([]string{"export GOLDIE_ADDR=" + srv.URL, "export GOLDIE_TOKEN=<your token>"}, tail...))
	})
	t.Run("streams", func(t *testing.T) {
		stdout, stderr := run(t, map[string]string{"GOLDIE_TOKEN": guidedToken})
		for _, w := range []string{"Project: ", "Wrote: ", "Registered: ", "Next"} {
			if !strings.Contains(stdout, w) {
				t.Errorf("stdout lacks %q:\n%s", w, stdout)
			}
		}
		for _, w := range []string{"Server: ", "Token: ", "goldie server address", "Use this Project?"} {
			if strings.Contains(stdout, w) {
				t.Errorf("stdout has %q, which belongs on stderr:\n%s", w, stdout)
			}
			if !strings.Contains(stderr, w) {
				t.Errorf("stderr lacks %q:\n%s", w, stderr)
			}
		}
	})
}

// 8. Ctrl-D after the first prompt.
func TestInitStopsAtEOFAfterTheFirstPrompt(t *testing.T) {
	root := guidedRepo(t)
	stdout, _, err := runInit(strings.NewReader("\n"), nil)
	if err == nil || !strings.Contains(err.Error(), "init stopped; nothing more was changed") {
		t.Fatalf("error = %v, want %q", err, "init stopped; nothing more was changed")
	}
	if !wroteConfig(t, root) {
		t.Error("the config written before the stop is gone")
	}
	if !strings.Contains(stdout, "Wrote: ") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// 9. Server text.
func TestInitPrintsNoControlCharactersFromTheServer(t *testing.T) {
	srv := newGuidedServer(t, "", true)
	guidedRepo(t)
	stdout, stderr, _ := runInit(strings.NewReader("\n"+srv.URL+"\n"), map[string]string{"GOLDIE_TOKEN": guidedToken})
	if !strings.Contains(stderr, "Server: "+srv.URL+" (not reachable: ") {
		t.Errorf("stderr lacks the not-reachable line:\n%s", stderr)
	}
	for _, s := range []string{stdout, stderr} {
		for _, r := range s {
			if r != '\n' && (unicode.IsControl(r) || r == '\u202e') {
				t.Fatalf("output has the character %U:\n%q", r, s)
			}
		}
		if strings.Contains(s, "\nForged: line") {
			t.Errorf("server text forged a line:\n%q", s)
		}
	}
}
