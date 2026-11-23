package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// #512: a first-time user who runs search, lookup or mcp with no address is
// told both ways forward, not only the setting they do not have yet. The
// error comes first and the next step is on its own line (the pattern of the
// server's first-run hint).

const (
	noAddressReason = "no server address: set --addr or GOLDIE_ADDR, or read a content directory with --dir"
	firstRunHint    = "To try goldie on this machine: goldie server --dev"
)

func TestFirstRunHintHasNoTrailingPunctuation(t *testing.T) {
	if platform.FirstRunHint != firstRunHint {
		t.Errorf("platform.FirstRunHint = %q, want %q", platform.FirstRunHint, firstRunHint)
	}
}

func TestCLISearchAndLookupWithNoAddressNameBothWaysForward(t *testing.T) {
	for _, args := range [][]string{{"search", "foo"}, {"lookup", "team/some-key"}} {
		t.Run(args[0], func(t *testing.T) {
			stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), args...)
			if code == 0 {
				t.Errorf("exit code = 0, want non-zero (stdout %q)", stdout)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			want := "goldie: " + noAddressReason + "\n" + firstRunHint + "\n"
			if stderr != want {
				t.Errorf("stderr =\n%q\nwant\n%q", stderr, want)
			}
		})
	}
}

func TestMCPStdioWithNoAddressNamesBothWaysForward(t *testing.T) {
	stdout, stderr := runMCPExpectingFailure(t, nil, nil)
	if stdout != "" {
		t.Errorf("stdout = %q, want empty: it carries only the protocol", stdout)
	}
	want := "goldie: invalid client settings: " + noAddressReason + "\n" + firstRunHint + "\n"
	if stderr != want {
		t.Errorf("stderr =\n%q\nwant\n%q", stderr, want)
	}
}

// The ways forward are for a missing address only. Another client setting
// that is wrong, or an address that is set, does not get them.
func TestCLIWithAnAddressOrABadSettingDoesNotGetTheFirstRunHint(t *testing.T) {
	for name, args := range map[string][]string{
		"address set, no token": {"search", "foo", "--addr", "http://127.0.0.1:1"},
		"address not http":      {"search", "foo", "--addr", "ftp://example.invalid", "--token", cliToken},
	} {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := runValidate(t, t.TempDir(), validateEnv(), args...)
			if code == 0 {
				t.Error("exit code = 0, want non-zero")
			}
			if strings.Contains(stderr, firstRunHint) || strings.Contains(stderr, "--dir") {
				t.Errorf("stderr = %q, want no first-run hint", stderr)
			}
		})
	}
}

// status and the server share the hint text, again with no trailing " .".
func TestCLIStatusWithNoServerEndsWithTheFirstRunHint(t *testing.T) {
	_, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "status")
	if code == 0 {
		t.Error("exit code = 0, want non-zero")
	}
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if last := lines[len(lines)-1]; last != firstRunHint {
		t.Errorf("last stderr line = %q, want %q\n%s", last, firstRunHint, stderr)
	}
	if n := strings.Count(stderr, firstRunHint); n != 1 {
		t.Errorf("stderr carries the hint %d times, want once:\n%s", n, stderr)
	}
}

func TestServerWithNothingConfiguredEndsWithTheFirstRunHint(t *testing.T) {
	_, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "server")
	if code == 0 {
		t.Error("exit code = 0, want non-zero")
	}
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if last := lines[len(lines)-1]; last != firstRunHint {
		t.Errorf("last stderr line = %q, want %q\n%s", last, firstRunHint, stderr)
	}
}

// The README says, right after the goldie server --dev example, that the
// export line it prints is what the MCP config in the next section reads, and
// no longer describes the hint with a trailing " .".
func TestREADMEExplainsTheExportLineFeedsTheMCPConfig(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	_, after, ok := strings.Cut(readme, "goldie server --dev examples/knowledge\n# export GOLDIE_ADDR=")
	if !ok {
		t.Fatal("README lacks the goldie server --dev example")
	}
	_, after, _ = strings.Cut(after, "```\n")
	para, _, _ := strings.Cut(strings.TrimLeft(after, "\n"), "\n\n")
	// The existing "Paste the printed line" paragraph comes first or the new
	// sentence extends it; either way it sits before the next paragraph break
	// that follows.
	next, _, _ := strings.Cut(strings.TrimLeft(strings.TrimPrefix(strings.TrimLeft(after, "\n"), para), "\n"), "\n\n")
	block := para + "\n" + next
	if !strings.Contains(block, "GOLDIE_ADDR") || !strings.Contains(block, "MCP") {
		t.Errorf("README does not say, after the example, that the export GOLDIE_ADDR line is what the MCP config reads:\n%s", block)
	}
	if strings.Contains(readme, "goldie server --dev .") {
		t.Error("README still quotes the hint with a trailing \" .\"")
	}
	if !strings.Contains(readme, "`"+firstRunHint+"`") {
		t.Errorf("README does not quote %q", firstRunHint)
	}
}
