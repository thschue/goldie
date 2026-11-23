package main

import (
	"regexp"
	"strings"
	"testing"
)

// Command help at the CLI seam (#437). The real binary is run with an empty
// environment: help needs no server, no token and no configuration.

var (
	serverGroup = []string{"server", "validate", "health"}
	askGroup    = []string{"search", "lookup", "status", "init", "mcp", "version"}
	// After a bad flag the hint names the command (or the <command> placeholder at
	// the root); after an unknown command there is no command to name.
	nextStep        = regexp.MustCompile(`^Run 'goldie (<command>|[a-z]+) --help'\.$`)
	nextStepUnknown = regexp.MustCompile(`^Run 'goldie --help'\.$`)
)

const helpSecretToken = "help-secret-token-0123456789abcdef0123456789"

func runHelp(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runValidate(t, t.TempDir(), validateEnv("GOLDIE_TOKEN="+helpSecretToken), args...)
}

// section returns the text between two headings, or to the end when to is "".
func section(t *testing.T, text, from, to string) string {
	t.Helper()
	i := strings.Index(text, from)
	if i < 0 {
		t.Fatalf("help lacks the group %q:\n%s", from, text)
	}
	rest := text[i+len(from):]
	if to == "" {
		return rest
	}
	j := strings.Index(rest, to)
	if j < 0 {
		t.Fatalf("help lacks the group %q:\n%s", to, text)
	}
	return rest[:j]
}

func hasCommandLine(text, name string) bool {
	return regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(name) + `\s+\S`).MatchString(text)
}

func wantRootHelp(t *testing.T, stdout, stderr string, code int) {
	t.Helper()
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty: asked-for help goes to stdout", stderr)
	}
	if strings.Contains(stdout, "usage: goldie") {
		t.Errorf("stdout still carries the old one-line usage:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Run a server") || !strings.Contains(stdout, "Ask a server") {
		t.Fatalf("root help lacks the groups Run a server and Ask a server:\n%s", stdout)
	}
	run := section(t, stdout, "Run a server", "Ask a server")
	ask := section(t, stdout, "Ask a server", "")
	for _, c := range serverGroup {
		if !hasCommandLine(run, c) {
			t.Errorf("%q is not listed with a description under Run a server:\n%s", c, stdout)
		}
		if hasCommandLine(ask, c) {
			t.Errorf("%q is listed under Ask a server, want Run a server", c)
		}
	}
	for _, c := range askGroup {
		if !hasCommandLine(ask, c) {
			t.Errorf("%q is not listed with a description under Ask a server:\n%s", c, stdout)
		}
		if hasCommandLine(run, c) {
			t.Errorf("%q is listed under Run a server, want Ask a server", c)
		}
	}
	if !strings.Contains(stdout, "Run 'goldie <command> --help'") {
		t.Errorf("root help does not point at 'goldie <command> --help':\n%s", stdout)
	}
	// Notes that belong to one command stay out of the root help.
	for _, note := range []string{"process list", "random token", "GOLDIE_ADMIN_ADDR"} {
		if strings.Contains(stdout, note) {
			t.Errorf("root help carries the per-command note %q:\n%s", note, stdout)
		}
	}
}

func TestRootHelpListsEveryCommandInTwoGroups(t *testing.T) {
	for _, args := range [][]string{{}, {"help"}, {"--help"}} {
		t.Run(strings.Join(append([]string{"goldie"}, args...), " "), func(t *testing.T) {
			stdout, stderr, code := runHelp(t, args...)
			wantRootHelp(t, stdout, stderr, code)
		})
	}
}

func TestRootHelpShowsNoSecret(t *testing.T) {
	stdout, stderr, _ := runHelp(t, "--help")
	if strings.Contains(stdout+stderr, helpSecretToken) {
		t.Error("help shows the token from the environment")
	}
}

// What each command's help must name: its flags (GNU style) and the words of
// its own notes.
var commandHelp = []struct {
	name  string
	flags []string
	notes []string
}{
	{"server", []string{"--dev"}, []string{"GOLDIE_CONTENT_DIR", "127.0.0.1", "GOLDIE_ADDR"}},
	{"validate", nil, []string{"dir"}},
	{"health", []string{"--ready"}, nil},
	{"mcp", []string{"--transport", "--addr", "--dir", "--token"}, []string{"stdio", "process list"}},
	{"search", []string{"--addr", "--dir", "--token", "--json", "--project"}, []string{"process list"}},
	{"lookup", []string{"--addr", "--dir", "--token", "--json", "--project"}, []string{"process list"}},
	{"status", []string{"--addr", "--dir", "--token", "--json", "--project"}, []string{"process list"}},
	{"init", []string{"--addr", "--dir", "--token", "--project"}, []string{"process list"}},
	{"version", []string{"--addr", "--dir", "--token", "--json"}, []string{"process list"}},
}

func wantCommandHelp(t *testing.T, name, stdout, stderr string, code int, flags, notes []string) {
	t.Helper()
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty: asked-for help goes to stdout", stderr)
	}
	if !strings.Contains(stdout, "goldie "+name) {
		t.Errorf("help does not show the usage 'goldie %s':\n%s", name, stdout)
	}
	for _, f := range flags {
		if !strings.Contains(stdout, f) {
			t.Errorf("help does not list %s:\n%s", f, stdout)
		}
	}
	for _, n := range notes {
		if !strings.Contains(stdout, n) {
			t.Errorf("help lacks %q:\n%s", n, stdout)
		}
	}
	// At least one example that starts with the program name, after an Examples heading.
	i := strings.Index(strings.ToLower(stdout), "example")
	if i < 0 || !regexp.MustCompile(`(?m)^\s+goldie `+regexp.QuoteMeta(name)).MatchString(stdout[i:]) {
		t.Errorf("help has no example running 'goldie %s':\n%s", name, stdout)
	}
	for _, old := range []string{" -dev", " -addr", " -dir", " -json", " -project", " -transport", " -ready"} {
		if strings.Contains(stdout, old) {
			t.Errorf("help still spells a long flag with a single dash (%q):\n%s", old, stdout)
		}
	}
	if strings.Contains(stdout+stderr, helpSecretToken) {
		t.Error("help shows the token from the environment")
	}
}

func TestCommandHelpShowsUsageFlagsAndAnExample(t *testing.T) {
	for _, c := range commandHelp {
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr, code := runHelp(t, c.name, "--help")
			wantCommandHelp(t, c.name, stdout, stderr, code, c.flags, c.notes)
		})
	}
}

func TestHelpCommandShowsTheSameAsTheFlag(t *testing.T) {
	stdout, stderr, code := runHelp(t, "help", "search")
	if code != 0 || stderr != "" {
		t.Fatalf("goldie help search: exit %d, stderr %q, want 0 and empty", code, stderr)
	}
	viaFlag, _, _ := runHelp(t, "search", "--help")
	if stdout != viaFlag {
		t.Errorf("goldie help search differs from goldie search --help\n--- help ---\n%s\n--- flag ---\n%s", stdout, viaFlag)
	}
}

// A help request wins over arguments that would otherwise be misuse, and a
// flag may follow the positional words (parseInterleaved keeps working).
func TestHelpFlagWinsOverOtherArguments(t *testing.T) {
	for _, args := range [][]string{
		{"search", "agent", "--help"},
		{"lookup", "a", "b", "--help"},
		{"status", "extra", "--help"},
		{"validate", "a", "b", "--help"},
	} {
		stdout, stderr, code := runHelp(t, args...)
		if code != 0 || stderr != "" || !strings.Contains(stdout, "goldie "+args[0]) {
			t.Errorf("goldie %v: exit %d, stderr %q, stdout %q, want that command's help on stdout", args, code, stderr, stdout)
		}
	}
}

// wantMisuse checks the one-line error and the next step, on stderr, exit 2.
func wantMisuse(t *testing.T, args []string, stdout, stderr string, code int, named string, hint *regexp.Regexp) {
	t.Helper()
	if code != 2 {
		t.Errorf("goldie %v: exit code = %d, want 2 (stderr %q)", args, code, stderr)
	}
	if stdout != "" {
		t.Errorf("goldie %v: stdout = %q, want empty", args, stdout)
	}
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("goldie %v: stderr has %d lines, want the error and the next step:\n%s", args, len(lines), stderr)
		return
	}
	if named != "" && !strings.Contains(lines[0], named) {
		t.Errorf("goldie %v: error line %q does not name %q", args, lines[0], named)
	}
	if !hint.MatchString(lines[1]) {
		t.Errorf("goldie %v: last line = %q, want it to match %s", args, lines[1], hint)
	}
	for _, bad := range []string{"usage:", "Run a server", "Ask a server", "Usage:", "Flags:", "goroutine", ".go:", helpSecretToken} {
		if strings.Contains(stderr, bad) {
			t.Errorf("goldie %v: stderr carries %q, want only the error and the next step:\n%s", args, bad, stderr)
		}
	}
}

func TestUnknownCommandIsAOneLineErrorWithExit2(t *testing.T) {
	for _, args := range [][]string{{"frobnicate"}, {"frobnicate", "--help"}, {"help", "frobnicate"}, {"Server"}} {
		stdout, stderr, code := runHelp(t, args...)
		named := args[0]
		if named == "help" {
			named = args[1]
		}
		wantMisuse(t, args, stdout, stderr, code, named, nextStepUnknown)
	}
}

func TestUnknownFlagIsAOneLineErrorWithExit2(t *testing.T) {
	// One server-role and one client-role command.
	for _, args := range [][]string{
		{"server", "--bogus"},
		{"search", "agent", "--bogus"},
	} {
		stdout, stderr, code := runHelp(t, args...)
		wantMisuse(t, args, stdout, stderr, code, "bogus", nextStep)
	}
}

// Flags are GNU style with no compatibility shim: the single-dash long
// spelling is now misuse.
func TestSingleDashLongFlagsAreMisuse(t *testing.T) {
	for _, args := range [][]string{
		{"server", "-dev"},
		{"health", "-ready"},
		{"search", "agent", "-json"},
		{"status", "-addr", "http://127.0.0.1:1"},
	} {
		stdout, stderr, code := runHelp(t, args...)
		wantMisuse(t, args, stdout, stderr, code, "", nextStep)
	}
}
