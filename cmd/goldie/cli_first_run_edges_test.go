package main

import (
	"net"
	"strings"
	"testing"
)

// First-run edges at the CLI boundary (#516): the server example that returns,
// goldie --version, and the next step after an unreachable health probe.

const healthNextStep = "Check that goldie server is running and that GOLDIE_ADMIN_ADDR matches its admin address."

func TestServerHelpExampleDoesNotRunTheServerInsideACommandSubstitution(t *testing.T) {
	stdout, stderr, code := runHelp(t, "server", "--help")
	if code != 0 {
		t.Fatalf("server --help exit = %d, stderr: %s", code, stderr)
	}
	examples := section(t, stdout, "Examples:", "Flags:")
	if strings.Contains(examples, "$(") || strings.Contains(examples, "eval") || strings.Contains(examples, "`") {
		t.Errorf("an example runs server inside a substitution (it never returns):\n%s", examples)
	}
	found := false
	var comment string
	for _, l := range strings.Split(examples, "\n") {
		s := strings.TrimSpace(l)
		if s == "goldie server --dev ./content" {
			found = true
		}
		if strings.HasPrefix(s, "#") {
			comment = s
		}
	}
	if !found {
		t.Fatalf("examples lack the line `goldie server --dev ./content`:\n%s", examples)
	}
	if !strings.Contains(comment, "export") || !strings.Contains(comment, "another terminal") {
		t.Errorf("examples lack a # comment saying the export line goes in another terminal:\n%s", examples)
	}
}

func TestRootVersionFlagPrintsWhatTheVersionCommandPrints(t *testing.T) {
	wantOut, _, wantCode := runHelp(t, "version")
	if wantCode != 0 || strings.TrimSpace(wantOut) == "" {
		t.Fatalf("version: exit %d, stdout %q", wantCode, wantOut)
	}
	stdout, stderr, code := runHelp(t, "--version")
	if code != 0 {
		t.Fatalf("--version exit = %d, want 0; stderr: %s", code, stderr)
	}
	if stdout != wantOut {
		t.Errorf("--version stdout = %q, want %q (same as goldie version)", stdout, wantOut)
	}
	if stderr != "" {
		t.Errorf("--version wrote to stderr: %q", stderr)
	}
}

func TestVersionFlagOnASubcommandStaysAnUnknownFlag(t *testing.T) {
	_, stderr, code := runHelp(t, "search", "--version")
	if code != 2 || !strings.Contains(stderr, "--version") {
		t.Errorf("search --version: exit %d, stderr %q, want exit 2 naming the flag", code, stderr)
	}
}

func TestHealthWithNothingListeningNamesTheNextStep(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv("GOLDIE_ADMIN_ADDR="+addr), "health")
	if code == 0 {
		t.Fatalf("health with nothing listening exited 0; stdout %q stderr %q", stdout, stderr)
	}
	if !strings.Contains(stderr, "unhealthy: admin port unreachable") {
		t.Errorf("stderr = %q, want the unhealthy line kept", stderr)
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if got := lines[len(lines)-1]; got != healthNextStep {
		t.Errorf("last stderr line = %q, want %q on its own line", got, healthNextStep)
	}
	if strings.Contains(stderr, addr) || strings.Contains(stdout, addr) {
		t.Errorf("health echoes the admin address: %q %q", stdout, stderr)
	}
}
