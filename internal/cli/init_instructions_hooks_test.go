package cli_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/cli"
)

// Acceptance tests for #671: goldie init --instructions and --hooks also run
// what goldie instructions apply and goldie hooks install run. They run at the
// cli.Run boundary and reuse the repository, servers and runner of
// init_guided_test.go and init_guided_instructions_test.go.

const (
	flagBlockMsg  = "Wrote the goldie block to CLAUDE.md (was missing).\n"
	flagHooksMsg  = "Added the goldie SessionStart hook to .claude/settings.json.\n"
	noServerLine  = "Registered: not checked (no server address: set --addr or GOLDIE_ADDR)\n"
	hooksBadInput = `{"hooks": {}, "hooks": {}}`
)

// initOwn is what init prints today, before any added step, for root with the
// Registered line given.
func initOwn(root, registered string) string {
	return guidedLine + "Wrote: " + filepath.Join(root, ".goldie", "config.yaml") + "\n" + registered
}

func settingsPath(root string) string { return filepath.Join(root, ".claude", "settings.json") }

func writeSettings(t *testing.T, root, doc string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath(root), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func noSettingsFile(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(settingsPath(root)); !os.IsNotExist(err) {
		t.Errorf(".claude/settings.json exists: %v", err)
	}
}

// hookCommandsOf lists every command under hooks.SessionStart of the file.
func hookCommandsOf(t *testing.T, root string) []string {
	t.Helper()
	b, err := os.ReadFile(settingsPath(root))
	if err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	var doc struct {
		Hooks struct {
			SessionStart []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("settings.json is not JSON: %v\n%s", err, b)
	}
	var cmds []string
	for _, g := range doc.Hooks.SessionStart {
		for _, h := range g.Hooks {
			cmds = append(cmds, h.Command)
		}
	}
	return cmds
}

// 1. --instructions runs goldie instructions apply and prints its message
// unchanged, after init's own lines.
func TestInitInstructionsFlagAppliesTheBlock(t *testing.T) {
	srv := instructionsServer(t, false)
	root := guidedRepo(t)
	stdout, stderr, err := runInit(nil, map[string]string{"GOLDIE_TOKEN": guidedToken}, "--addr", srv.URL, "--instructions")
	if err != nil {
		t.Fatalf("init: %v\nstderr: %s", err, stderr)
	}
	if want := initOwn(root, "Registered: yes\n") + flagBlockMsg; stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
	if got := claudeFile(root); !strings.Contains(got, "Ask goldie first.") {
		t.Errorf("CLAUDE.md lacks the block:\n%s", got)
	}
	noSettingsFile(t, root) // --instructions asks for the block only
}

// 1. --hooks runs goldie hooks install and prints its message unchanged,
// after init's own lines. It needs no server and no token.
func TestInitHooksFlagInstallsTheSessionStartHook(t *testing.T) {
	root := guidedRepo(t)
	stdout, stderr, err := runInit(nil, nil, "--hooks")
	if err != nil {
		t.Fatalf("init: %v\nstderr: %s", err, stderr)
	}
	if want := initOwn(root, noServerLine) + flagHooksMsg; stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
	cmds := hookCommandsOf(t, root)
	if len(cmds) != 2 || !strings.Contains(cmds[0], "goldie context --hook") || !strings.Contains(cmds[1], "goldie pull --quiet --if-pulled") {
		t.Errorf("SessionStart commands = %q", cmds)
	}
	noClaudeFile(t, root) // --hooks asks for the hook only
}

func TestInitWithBothFlagsRunsBothAfterItsOwnLines(t *testing.T) {
	srv := instructionsServer(t, false)
	root := guidedRepo(t)
	stdout, stderr, err := runInit(nil, map[string]string{"GOLDIE_TOKEN": guidedToken}, "--addr", srv.URL, "--instructions", "--hooks")
	if err != nil {
		t.Fatalf("init: %v\nstderr: %s", err, stderr)
	}
	own := initOwn(root, "Registered: yes\n")
	if !strings.HasPrefix(stdout, own) {
		t.Fatalf("stdout does not start with init's own lines:\n%s", stdout)
	}
	rest := strings.TrimPrefix(stdout, own)
	if !hasLine(rest, strings.TrimSuffix(flagBlockMsg, "\n")) || !hasLine(rest, strings.TrimSuffix(flagHooksMsg, "\n")) || len(strings.Split(strings.TrimSpace(rest), "\n")) != 2 {
		t.Errorf("after init's lines:\n%s", rest)
	}
	if !strings.Contains(claudeFile(root), "Ask goldie first.") || len(hookCommandsOf(t, root)) != 2 {
		t.Error("the block or the hook is missing")
	}
}

// 2. Without the flags init's output and files are what they were, and
// nothing is installed. 4. Nothing is installed unless the user asks.
func TestInitWithoutTheFlagsInstallsNothing(t *testing.T) {
	srv := instructionsServer(t, false)
	root := guidedRepo(t)
	stdout, stderr, err := runInit(nil, map[string]string{"GOLDIE_TOKEN": guidedToken}, "--addr", srv.URL)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if want := initOwn(root, "Registered: yes\n"); stdout != want || stderr != "" {
		t.Errorf("output changed:\nstdout: %q\nstderr: %q\nwant stdout: %q", stdout, stderr, want)
	}
	noClaudeFile(t, root)
	if _, err := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(err) {
		t.Errorf(".claude exists: %v", err)
	}
	if fetchedBlock(srv) {
		t.Errorf("the block was fetched: %v", srv.requests())
	}
}

func TestInitFlagsAreOffByDefaultOnATerminalToo(t *testing.T) {
	srv := instructionsServer(t, false)
	root, _, _, err := guidedInstructions(t, srv, "", "n\n")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	noSettingsFile(t, root)
	noClaudeFile(t, root)
}

// 3. A failing step keeps the config, the error goes to stderr, the other
// step still runs, the exit is 1.
func TestInitKeepsTheConfigAndRunsTheHookWhenInstructionsFail(t *testing.T) {
	srv := instructionsServer(t, true)
	root := guidedRepo(t)
	stdout, stderr, err := runInit(nil, map[string]string{"GOLDIE_TOKEN": guidedToken}, "--addr", srv.URL, "--instructions", "--hooks")
	if err == nil {
		t.Fatal("init succeeded; want exit 1")
	}
	var usage *cli.UsageError
	if errors.As(err, &usage) {
		t.Errorf("the failure is a usage error (exit 2): %v", err)
	}
	if !strings.Contains(err.Error()+stderr, "the block could not be rendered") {
		t.Errorf("the error does not say why:\nerr: %v\nstderr: %s", err, stderr)
	}
	if strings.Contains(stdout, "the block could not be rendered") {
		t.Errorf("the error is on stdout:\n%s", stdout)
	}
	if !wroteConfig(t, root) {
		t.Error("config.yaml is gone")
	}
	noClaudeFile(t, root)
	if !strings.HasSuffix(stdout, flagHooksMsg) || len(hookCommandsOf(t, root)) != 2 {
		t.Errorf("the hook step did not run:\n%s", stdout)
	}
}

func TestInitKeepsTheConfigAndAppliesInstructionsWhenTheHookFails(t *testing.T) {
	srv := instructionsServer(t, false)
	root := guidedRepo(t)
	writeSettings(t, root, hooksBadInput)
	var refusal strings.Builder
	want := cli.HooksInstall([]string{"install"}, &refusal)
	if want == nil {
		t.Fatal("goldie hooks install accepts the fixture")
	}
	first, _, _ := strings.Cut(want.Error(), "\n")

	stdout, stderr, err := runInit(nil, map[string]string{"GOLDIE_TOKEN": guidedToken}, "--addr", srv.URL, "--instructions", "--hooks")
	if err == nil {
		t.Fatal("init succeeded; want exit 1")
	}
	var usage *cli.UsageError
	if errors.As(err, &usage) {
		t.Errorf("the failure is a usage error (exit 2): %v", err)
	}
	if !strings.Contains(err.Error()+stderr, first) {
		t.Errorf("the error is not the command's (%q):\nerr: %v\nstderr: %s", first, err, stderr)
	}
	if strings.Contains(stdout, first) {
		t.Errorf("the error is on stdout:\n%s", stdout)
	}
	if !wroteConfig(t, root) {
		t.Error("config.yaml is gone")
	}
	if b, _ := os.ReadFile(settingsPath(root)); string(b) != hooksBadInput {
		t.Errorf("settings.json changed on a refusal:\n%s", b)
	}
	if !strings.HasSuffix(stdout, flagBlockMsg) || !strings.Contains(claudeFile(root), "Ask goldie first.") {
		t.Errorf("the instructions step did not run:\n%s", stdout)
	}
}

func TestInitReportsBothErrorsWhenBothStepsFail(t *testing.T) {
	srv := instructionsServer(t, true)
	root := guidedRepo(t)
	writeSettings(t, root, hooksBadInput)
	stdout, stderr, err := runInit(nil, map[string]string{"GOLDIE_TOKEN": guidedToken}, "--addr", srv.URL, "--instructions", "--hooks")
	if err == nil {
		t.Fatal("init succeeded; want exit 1")
	}
	all := err.Error() + stderr
	if !strings.Contains(all, "the block could not be rendered") || !strings.Contains(all, "settings.json") {
		t.Errorf("an error is lost:\nerr: %v\nstderr: %s", err, stderr)
	}
	if strings.Contains(stdout, "could not be rendered") {
		t.Errorf("an error is on stdout:\n%s", stdout)
	}
	if !wroteConfig(t, root) {
		t.Error("config.yaml is gone")
	}
}

// The new flags fit init's token mode: --instructions without a token fails
// the way the command does, and the config and the hook still happen.
func TestInitInstructionsWithoutATokenFailsButKeepsTheRest(t *testing.T) {
	srv := instructionsServer(t, false)
	root := guidedRepo(t)
	_, stderr, err := runInit(nil, nil, "--addr", srv.URL, "--instructions", "--hooks")
	if err == nil {
		t.Fatal("init succeeded; want exit 1")
	}
	var usage *cli.UsageError
	if errors.As(err, &usage) {
		t.Errorf("the failure is a usage error (exit 2): %v", err)
	}
	if err.Error()+stderr == "" || fetchedBlock(srv) {
		t.Errorf("no error, or the block was fetched without a token: %v", srv.requests())
	}
	noClaudeFile(t, root)
	if !wroteConfig(t, root) || len(hookCommandsOf(t, root)) != 2 {
		t.Error("the config or the hook is missing")
	}
}
