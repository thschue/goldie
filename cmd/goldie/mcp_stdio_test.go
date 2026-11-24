package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These tests run the real binary as `goldie mcp --transport stdio`, the way
// an agent's MCP client starts it, against a real in-process server
// (cliServer), and speak MCP to it over the process's stdin and stdout.

// syncBuffer collects the subprocess's stderr while the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// mcpCommand is `goldie mcp --transport stdio` with extra args, an empty
// working directory, and only PATH plus env as its environment, so the
// settings under test are the only ones it sees.
func mcpCommand(t *testing.T, env []string, args ...string) (*exec.Cmd, *syncBuffer) {
	t.Helper()
	cmd := exec.Command(goldieBinary(t), append([]string{"mcp", "--transport", "stdio"}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	return cmd, stderr
}

// stdioSession starts the subcommand and connects an MCP client to it over
// stdio. The session is closed, and the process stopped, at cleanup.
func stdioSession(t *testing.T, env []string, args ...string) (*mcp.ClientSession, *syncBuffer) {
	t.Helper()
	cmd, stderr := mcpCommand(t, env, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		t.Fatalf("connect to goldie mcp --transport stdio: %v\nstderr:\n%s", err, stderr)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, stderr
}

// stdioCall calls a tool and returns its text and whether it is a tool error.
func stdioCall(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s over stdio: %v", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		tc, ok := c.(*mcp.TextContent)
		if !ok {
			t.Fatalf("content is %T, want text", c)
		}
		b.WriteString(tc.Text)
	}
	return b.String(), res.IsError
}

// assertBothToolsAnswer calls search_knowledge and get_article and checks
// that each returns the article from the test content.
func assertBothToolsAnswer(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	text, isErr := stdioCall(t, cs, "search_knowledge", map[string]any{"query": "agents loops"})
	if isErr || !strings.Contains(text, "agent-loops") || !strings.Contains(text, "Autonomous agent loops") {
		t.Errorf("search_knowledge over stdio: error=%v, want the agent-loops hit:\n%s", isErr, text)
	}
	text, isErr = stdioCall(t, cs, "get_article", map[string]any{"id": "agent-loops"})
	if isErr || !strings.Contains(text, "Autonomous agent loops") ||
		!strings.Contains(text, "Three loops turn issues into merged pull requests.") {
		t.Errorf("get_article over stdio: error=%v, want the full article:\n%s", isErr, text)
	}
}

// Criterion: a CLI-boundary test runs the subcommand against a real server and
// calls both tools over stdio.
func TestMCPStdioAdvertisesBothTools(t *testing.T) {
	srv := cliServer(t)
	cs, _ := stdioSession(t, nil, "--addr", srv.URL, "--token", cliToken)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools over stdio: %v", err)
	}
	got := map[string]bool{}
	for _, tl := range res.Tools {
		got[tl.Name] = true
	}
	if len(got) != 11 || !got["find_decisions"] || !got["search_knowledge"] || !got["get_article"] || !got["list_projects"] || !got["current_context"] || !got["find_skills"] || !got["get_skill"] || !got["list_skills"] || !got["list_agents"] || !got["list_loops"] || !got["get_loop"] {
		t.Errorf("tools over stdio = %v, want exactly search_knowledge, get_article, list_projects, current_context, find_skills, get_skill, list_skills, list_agents, list_loops, get_loop and find_decisions", got)
	}
}

// Criterion: the subcommand uses the same client settings as the CLI. Each
// way the CLI takes the address and the token works for the MCP server too.
func TestMCPStdioUsesTheCLIClientSettings(t *testing.T) {
	srv := cliServer(t)
	cases := []struct {
		name string
		env  []string
		args []string
	}{
		{"flags", nil, []string{"--addr", srv.URL, "--token", cliToken}},
		{"GOLDIE_ADDR and GOLDIE_TOKEN", []string{"GOLDIE_ADDR=" + srv.URL, "GOLDIE_TOKEN=" + cliToken}, nil},
		{"GOLDIE_TOKEN_FILE", []string{"GOLDIE_ADDR=" + srv.URL, "GOLDIE_TOKEN_FILE=" + tokenFile(t, cliToken)}, nil},
		{"address with a trailing slash", nil, []string{"--addr", srv.URL + "/", "--token", cliToken}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cs, stderr := stdioSession(t, c.env, c.args...)
			assertBothToolsAnswer(t, cs)
			if strings.Contains(stderr.String(), cliToken) {
				t.Errorf("stderr shows the token:\n%s", stderr)
			}
		})
	}
}

// As in the CLI, a flag overrides its environment variable.
func TestMCPStdioFlagsOverrideTheEnvironment(t *testing.T) {
	srv := cliServer(t)
	env := []string{"GOLDIE_ADDR=http://127.0.0.1:1", "GOLDIE_TOKEN=wrong-token-0123456789abcdef"}
	cs, _ := stdioSession(t, env, "--addr", srv.URL, "--token", cliToken)
	assertBothToolsAnswer(t, cs)
}

// Every call sends the configured token: a wrong one is a tool error with the
// server's 401 message and hint, and the error shows neither token nor address.
func TestMCPStdioWrongTokenIsAToolErrorThatLeaksNothing(t *testing.T) {
	srv := cliServer(t)
	const wrong = "wrong-token-0123456789abcdef"
	cs, stderr := stdioSession(t, nil, "--addr", srv.URL, "--token", wrong)
	for tool, args := range map[string]map[string]any{
		"search_knowledge": {"query": "agents"},
		"get_article":      {"id": "agent-loops"},
	} {
		text, isErr := stdioCall(t, cs, tool, args)
		if !isErr || !strings.Contains(text, "missing or invalid API token") {
			t.Errorf("%s: want the 401 message as a tool error, got error=%v %s", tool, isErr, text)
		}
		for _, bad := range []string{wrong, cliToken, srv.URL, "127.0.0.1"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: tool error leaks %q: %s", tool, bad, text)
			}
		}
	}
	if strings.Contains(stderr.String(), wrong) {
		t.Errorf("stderr shows the token:\n%s", stderr)
	}
}

// A server that is not running is a tool error that does not show the address;
// the MCP server itself keeps running.
func TestMCPStdioUnreachableServerIsAToolError(t *testing.T) {
	cs, _ := stdioSession(t, nil, "--addr", "http://127.0.0.1:1", "--token", cliToken)
	for range 2 {
		text, isErr := stdioCall(t, cs, "search_knowledge", map[string]any{"query": "agents"})
		if !isErr || strings.Contains(text, "127.0.0.1") {
			t.Errorf("want a tool error without the address, got error=%v %s", isErr, text)
		}
	}
}

// The CLI client caps every answer at 16 MiB; so does the MCP server built on
// the same settings. A hostile server's oversized answer is a tool error, not
// an unbounded tool result, and the MCP server keeps answering.
func TestMCPStdioOversizedAnswerIsAToolError(t *testing.T) {
	huge := `{"results":[{"id":"x","name":"` + strings.Repeat("A", 17<<20) + `","keywords":[],"matched":[],"score":1}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(huge))
	}))
	t.Cleanup(srv.Close)
	cs, _ := stdioSession(t, nil, "--addr", srv.URL, "--token", cliToken)
	for range 2 {
		text, isErr := stdioCall(t, cs, "search_knowledge", map[string]any{"query": "agents"})
		if !isErr || len(text) > 1<<20 {
			t.Errorf("want a short tool error for a 17 MiB answer, got error=%v and %d bytes", isErr, len(text))
		}
	}
}

// Closing stdin is how an MCP client ends a stdio server: it exits 0 on its
// own, without needing a signal.
func TestMCPStdioExitsCleanlyWhenStdinCloses(t *testing.T) {
	srv := cliServer(t)
	cmd, stderr := mcpCommand(t, nil, "--addr", srv.URL, "--token", cliToken)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const grace = 10 * time.Second
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: grace}, nil)
	if err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		t.Fatalf("connect: %v\nstderr:\n%s", err, stderr)
	}
	start := time.Now()
	if err := cs.Close(); err != nil {
		t.Fatalf("closing the session: %v (want exit 0 on end of stdin)\nstderr:\n%s", err, stderr)
	}
	if took := time.Since(start); took >= grace {
		t.Errorf("exited only after %v: it waited for SIGTERM instead of exiting at end of stdin", took)
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		t.Errorf("process state %v, want exit 0", cmd.ProcessState)
	}
}

// runMCPExpectingFailure runs the subcommand with stdin at end of file and
// returns its stdout and stderr. It must exit non-zero within the timeout.
func runMCPExpectingFailure(t *testing.T, env []string, args []string, transport ...string) (string, string) {
	t.Helper()
	if transport == nil {
		transport = []string{"--transport", "stdio"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goldieBinary(t), append(append([]string{"mcp"}, transport...), args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("goldie mcp did not fail fast: still running after 10s\nstderr:\n%s", stderr.String())
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("goldie mcp %v: err = %v, want a non-zero exit\nstderr:\n%s", args, err, stderr.String())
	}
	return stdout.String(), stderr.String()
}

// Settings the CLI rejects stop the MCP server at startup with the CLI's
// message naming the setting (PC-04). The message goes to stderr, never to
// stdout, which carries only the protocol, and it shows no token, path or
// address. There is no local mode yet (#10): without an address the
// subcommand fails instead of starting an embedded server.
func TestMCPStdioRejectsBadClientSettingsAtStartup(t *testing.T) {
	srv := cliServer(t)
	const secretAddr = "http://s3cret-user:s3cret-pass@127.0.0.1:1"
	missing := filepath.Join(t.TempDir(), "no-such-token-file")
	cases := []struct {
		name string
		env  []string
		args []string
		want []string
	}{
		{"no address (no local mode until #10)", nil, []string{"--token", cliToken}, []string{"GOLDIE_ADDR"}},
		{"no token", nil, []string{"--addr", srv.URL}, []string{"GOLDIE_TOKEN"}},
		{"address is a listen address, not a URL", []string{"GOLDIE_ADDR=:8080"}, []string{"--token", cliToken}, []string{"http"}},
		{"address is not http", nil, []string{"--addr", "ftp://example.invalid", "--token", cliToken}, []string{"http"}},
		{"token file cannot be read", []string{"GOLDIE_TOKEN_FILE=" + missing}, []string{"--addr", srv.URL}, []string{"GOLDIE_TOKEN_FILE"}},
		{"token and token file both set", []string{"GOLDIE_TOKEN=" + cliToken, "GOLDIE_TOKEN_FILE=" + tokenFile(t, cliToken)}, []string{"--addr", srv.URL}, []string{"GOLDIE_TOKEN"}},
		{"address with credentials and no token", nil, []string{"--addr", secretAddr}, []string{"GOLDIE_TOKEN"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr := runMCPExpectingFailure(t, c.env, c.args)
			for _, w := range c.want {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr lacks %q:\n%s", w, stderr)
				}
			}
			if strings.Contains(stderr, "usage:") {
				t.Errorf("a settings error must name the setting, not print usage:\n%s", stderr)
			}
			if stdout != "" {
				t.Errorf("stdout carries only MCP messages, got %q", stdout)
			}
			for _, bad := range []string{cliToken, missing, "s3cret", "127.0.0.1"} {
				if strings.Contains(stderr, bad) {
					t.Errorf("stderr leaks %q:\n%s", bad, stderr)
				}
			}
		})
	}
}

// stdio is the only transport this slice has. Another one, or a missing value,
// is a usage error that names stdio.
func TestMCPRejectsAnUnknownTransport(t *testing.T) {
	srv := cliServer(t)
	settings := []string{"--addr", srv.URL, "--token", cliToken}
	for name, transport := range map[string][]string{
		"unknown":       {"--transport", "carrier-pigeon"},
		"hostile value": {"--transport", "stdio\n--token=x\x1b[2J"},
		"empty":         {"--transport", ""},
	} {
		t.Run(name, func(t *testing.T) {
			stdout, stderr := runMCPExpectingFailure(t, nil, settings, transport...)
			if !strings.Contains(stderr, "stdio") {
				t.Errorf("stderr must name the supported transport stdio:\n%s", stderr)
			}
			if strings.Contains(stderr, "\x1b") || strings.Contains(stderr, cliToken) {
				t.Errorf("stderr echoes a control character or the token: %q", stderr)
			}
			if stdout != "" {
				t.Errorf("stdout carries only MCP messages, got %q", stdout)
			}
		})
	}
}

// The root help lists the subcommand and its help names --transport stdio.
func TestUsageListsMCPStdio(t *testing.T) {
	var root bytes.Buffer
	if err := run(nil, &root); err != nil || !hasCommandLine(root.String(), "mcp") {
		t.Errorf("root help = %q (err %v), want it to list mcp", root.String(), err)
	}
	var out bytes.Buffer
	if err := run([]string{"mcp", "--help"}, &out); err != nil ||
		!strings.Contains(out.String(), "--transport") || !strings.Contains(out.String(), "stdio") {
		t.Errorf("mcp help = %q (err %v), want it to name --transport and stdio", out.String(), err)
	}
}
