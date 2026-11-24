package cli_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/cli"
)

// Acceptance tests for #801: goldie server --dev offers to set up this
// repository. cmd/goldie calls cli.OfferInit with the dev address and token;
// Env.In is the terminal (cmd sets it only when stdin and stderr are
// terminals), Env.Out is where a result would go and must stay empty because
// the whole flow writes to Env.Err (PC-07: stdout of --dev is the export line).

const (
	offerQuestion = "Set up this repository for this dev server now? [Y/n]"
	offerStale    = "stale-token-from-the-shell"
)

// runOffer calls OfferInit with in as the terminal (nil: no terminal) and
// returns what it wrote to Out and Err.
func runOffer(ctx context.Context, in io.Reader, lookup map[string]string, addr, token string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	e := cli.Env{
		Lookup:   func(k string) (string, bool) { v, ok := lookup[k]; return v, ok },
		ReadFile: os.ReadFile,
		Out:      &out,
		Err:      &errOut,
	}
	if in != nil {
		e.In = in
	}
	err = cli.OfferInit(ctx, e, addr, token)
	return out.String(), errOut.String(), err
}

func offerConfigPath(root string) string { return filepath.Join(root, ".goldie", "config.yaml") }

func assertNothingWritten(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, ".goldie")); !os.IsNotExist(err) {
		t.Errorf(".goldie exists: %v", err)
	}
}

// 2. The offer appears only on a terminal, in a project repository without a
// config; otherwise nothing is printed, read or written.
func TestOfferInitIsSilentWhereItDoesNotApply(t *testing.T) {
	const answers = "y\n\n\nn\n"
	const otherConfig = "project: github.com/other/repo\n"
	cases := []struct {
		name  string
		setup func(t *testing.T) string
		term  bool
	}{
		{"no terminal", func(t *testing.T) string { return guidedRepo(t) }, false},
		{"not a git repository", func(t *testing.T) string {
			d := t.TempDir()
			t.Chdir(d)
			return d
		}, true},
		{"Source repository", func(t *testing.T) string {
			d := guidedRepo(t)
			if err := os.MkdirAll(filepath.Join(d, "global", "articles"), 0o755); err != nil {
				t.Fatal(err)
			}
			return d
		}, true},
		{"config exists", func(t *testing.T) string {
			d := guidedRepo(t)
			if err := os.MkdirAll(filepath.Join(d, ".goldie"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(offerConfigPath(d), []byte(otherConfig), 0o644); err != nil {
				t.Fatal(err)
			}
			return d
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := c.setup(t)
			srv := newGuidedServer(t, `"`+guidedID+`"`, false)
			in := strings.NewReader(answers)
			var r io.Reader
			if c.term {
				r = in
			}
			stdout, stderr, err := runOffer(context.Background(), r, nil, srv.URL, guidedToken)
			if err != nil {
				t.Fatalf("OfferInit: %v", err)
			}
			if stdout != "" || stderr != "" {
				t.Errorf("printed where no offer applies:\nstdout: %q\nstderr: %q", stdout, stderr)
			}
			if in.Len() != len(answers) {
				t.Error("OfferInit read an answer where no offer applies")
			}
			if n := len(srv.requests()); n != 0 {
				t.Errorf("the server got requests: %v", srv.requests())
			}
			if c.name == "config exists" {
				if b, _ := os.ReadFile(offerConfigPath(root)); string(b) != otherConfig {
					t.Errorf("config.yaml changed: %q", b)
				}
				return
			}
			assertNothingWritten(t, root)
		})
	}
}

// 2 and 3. The offer is on stderr; yes runs the guided init with the dev
// address and token, all on stderr, and the token is shown nowhere and set in
// no environment.
func TestOfferInitYesRunsTheGuidedInitOnStderr(t *testing.T) {
	for _, k := range []string{"GOLDIE_ADDR", "GOLDIE_TOKEN"} {
		t.Setenv(k, "") // restores the original value at the end
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
	root := guidedRepo(t)
	srv := newGuidedServer(t, `"`+guidedID+`"`, false)
	// The shell's own settings lose to the dev server's.
	shell := map[string]string{"GOLDIE_ADDR": "http://127.0.0.1:1", "GOLDIE_TOKEN": offerStale}
	// offer, Use this Project, server address (the default), instruction block.
	in := strings.NewReader("y\n\n\nn\n")

	stdout, stderr, err := runOffer(context.Background(), in, shell, srv.URL, guidedToken)
	if err != nil {
		t.Fatalf("OfferInit: %v\nstderr:\n%s", err, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty: --dev's stdout is the export line alone", stdout)
	}
	for _, want := range []string{
		offerQuestion,
		"Use this Project? [Y/n]",
		guidedLine,
		"Wrote: ",
		"goldie server address [" + srv.URL + "]:",
		"Server: " + srv.URL + " (reachable)",
		"Token: accepted",
		"Registered: yes",
		"\nNext:\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if !wroteConfig(t, root) {
		t.Error("config.yaml was not written")
	}
	for name, s := range map[string]string{"stdout": stdout, "stderr": stderr} {
		if strings.Contains(s, guidedToken) || strings.Contains(s, offerStale) {
			t.Errorf("a token is on %s:\n%s", name, s)
		}
	}
	for _, k := range []string{"GOLDIE_ADDR", "GOLDIE_TOKEN"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s was set in the process environment: %q", k, v)
		}
	}
	if !strings.Contains(strings.Join(srv.requests(), "\n"), "GET /api/v1/projects") {
		t.Errorf("the server was never asked for its Projects with the dev token: %v", srv.requests())
	}
}

// 3. A failing server does not make the init print the token either.
func TestOfferInitYesNeverPrintsTheTokenInAnError(t *testing.T) {
	guidedRepo(t)
	srv := newGuidedServer(t, `"`+guidedID+`"`, true)
	_, stderr, err := runOffer(context.Background(), strings.NewReader("y\n\n\nn\n"), nil, srv.URL, guidedToken)
	all := stderr
	if err != nil {
		all += err.Error()
	}
	if strings.Contains(all, guidedToken) {
		t.Errorf("the token is in the output or the error:\n%s", all)
	}
}

// 4. No, and the end of input, write nothing and succeed, so the server keeps
// serving.
func TestOfferInitNoOrEndOfInputWritesNothing(t *testing.T) {
	for name, answer := range map[string]string{"no": "n\n", "capital no": "N\n", "end of input": ""} {
		t.Run(name, func(t *testing.T) {
			root := guidedRepo(t)
			srv := newGuidedServer(t, `"`+guidedID+`"`, false)
			stdout, stderr, err := runOffer(context.Background(), strings.NewReader(answer), nil, srv.URL, guidedToken)
			if err != nil {
				t.Fatalf("OfferInit: %v", err)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if strings.Contains(stderr, "Use this Project?") || strings.Contains(stderr, "Wrote:") {
				t.Errorf("the init ran after %q:\n%s", answer, stderr)
			}
			if answer != "" && !strings.Contains(stderr, offerQuestion) {
				t.Errorf("stderr lacks the offer:\n%s", stderr)
			}
			assertNothingWritten(t, root)
			if n := len(srv.requests()); n != 0 {
				t.Errorf("the server got requests after %q: %v", answer, srv.requests())
			}
		})
	}
}

// 5. A cancelled context (the first signal) ends the wait for an answer at
// once, with nothing written.
func TestOfferInitReturnsWhenTheContextIsCancelledAtThePrompt(t *testing.T) {
	root := guidedRepo(t)
	srv := newGuidedServer(t, `"`+guidedID+`"`, false)
	pr, pw := io.Pipe() // a terminal that never answers
	t.Cleanup(func() { _ = pw.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, _, err := runOffer(ctx, pr, nil, srv.URL, guidedToken)
		done <- err
	}()
	time.Sleep(400 * time.Millisecond) // past the first prompt's grace: it is waiting
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("OfferInit still waits on input after its context was cancelled")
	}
	assertNothingWritten(t, root)
}
