package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Acceptance tests for #670: goldie hooks install puts one matcher group with
// goldie's two command hooks under hooks.SessionStart in .claude/settings.json
// at the repository root. It keeps every other byte, is idempotent, never
// touches settings.local.json, and refuses what it cannot edit safely with one
// line and a next step (exit 1, nothing written). Any other verb is misuse
// (exit 2). The splice itself is tested in internal/cli/hooks_install_test.go.

const (
	hooksAdded   = "Added the goldie SessionStart hook to .claude/settings.json.\n"
	hooksCurrent = "The goldie SessionStart hook in .claude/settings.json is already up to date.\n"
	hooksUpdated = "Updated the goldie SessionStart hook in .claude/settings.json.\n"
	hooksOrigin  = "[remote \"origin\"]\n\turl = git@github.com:acme/shop.git\n"
)

// installHooks runs goldie hooks install in dir and returns stdout, the exit
// code and what main would print to stderr.
func installHooks(t *testing.T, dir string, args ...string) (stdout string, code int, stderr string) {
	t.Helper()
	stdout, err := runCLIIn(t, dir, append([]string{"hooks", "install"}, args...)...)
	var errBuf bytes.Buffer
	code = exitCode(err, &errBuf)
	return stdout, code, errBuf.String()
}

// hookCommands returns the commands under hooks.SessionStart of doc, group by group.
func hookCommands(t *testing.T, doc string) [][]string {
	t.Helper()
	var v struct {
		Hooks struct {
			SessionStart []struct {
				Matcher *string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v\n%s", err, doc)
	}
	var groups [][]string
	for _, g := range v.Hooks.SessionStart {
		var cmds []string
		for _, h := range g.Hooks {
			if h.Type != "command" {
				t.Errorf("hook type = %q, want command", h.Type)
			}
			cmds = append(cmds, h.Command)
		}
		groups = append(groups, cmds)
	}
	return groups
}

func wantOnlyGoldieHooks(t *testing.T, doc string) {
	t.Helper()
	got := hookCommands(t, doc)
	if len(got) != 1 || len(got[0]) != 2 || got[0][0] != "goldie context --hook" || got[0][1] != "goldie pull --quiet --if-pulled" {
		t.Errorf("hooks.SessionStart = %q, want one group of goldie context --hook and goldie pull --quiet --if-pulled", got)
	}
}

func TestCLIHooksInstallCreatesSettingsWithOnlyGoldiesEntry(t *testing.T) {
	root := gitRepo(t, hooksOrigin)
	stdout, code, stderr := installHooks(t, root)
	if code != 0 || stdout != hooksAdded || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q, want 0, %q and empty", code, stdout, stderr, hooksAdded)
	}
	doc := fileContent(t, filepath.Join(root, ".claude", "settings.json"))
	wantOnlyGoldieHooks(t, doc)
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &top); err != nil || len(top) != 1 {
		t.Errorf("want a JSON object with only the hooks key, got %v (%v)", top, err)
	}
	if _, err := os.Lstat(filepath.Join(root, ".claude", "settings.local.json")); err == nil {
		t.Error("settings.local.json was created")
	}
}

func TestCLIHooksInstallSecondRunChangesNoBytes(t *testing.T) {
	root := gitRepo(t, hooksOrigin)
	path := filepath.Join(root, ".claude", "settings.json")
	writeFile(t, path, "{\r\n\t\"model\": \"opus\"\r\n}\r\n")
	if stdout, code, _ := installHooks(t, root); code != 0 || stdout != hooksAdded {
		t.Fatalf("first run: exit %d, stdout %q", code, stdout)
	}
	first := fileContent(t, path)
	stdout, code, stderr := installHooks(t, root)
	if code != 0 || stdout != hooksCurrent || stderr != "" {
		t.Errorf("second run: exit %d, stdout %q, stderr %q, want 0 and %q", code, stdout, stderr, hooksCurrent)
	}
	if got := fileContent(t, path); got != first {
		t.Errorf("the second run changed the file:\n%q\n%q", first, got)
	}
}

func TestCLIHooksInstallKeepsTheUsersBytesTheModeAndSettingsLocal(t *testing.T) {
	root := gitRepo(t, hooksOrigin)
	path := filepath.Join(root, ".claude", "settings.json")
	user := "{\n    \"permissions\": {\"allow\": [\"Bash(ls)\"]},\n    \"hooks\": {\n        \"SessionStart\": [{\"hooks\": [{\"type\": \"command\", \"command\": \"echo my own start\"}]}]\n    }\n}\n"
	writeFile(t, path, user)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(root, ".claude", "settings.local.json")
	const localDoc = "{ \"hooks\":{} }\n"
	writeFile(t, local, localDoc)

	// From a subdirectory: the file is at the repository root.
	sub := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, code, stderr := installHooks(t, sub)
	if code != 0 || stdout != hooksAdded || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	doc := fileContent(t, path)
	i := 0
	for j := 0; j < len(doc) && i < len(user); j++ {
		if doc[j] == user[i] {
			i++
		}
	}
	if i != len(user) {
		t.Errorf("a byte of the user's file changed or was dropped:\n%s", doc)
	}
	got := hookCommands(t, doc)
	if len(got) != 2 || len(got[0]) != 1 || got[0][0] != "echo my own start" ||
		len(got[1]) != 2 || got[1][0] != "goldie context --hook" || got[1][1] != "goldie pull --quiet --if-pulled" {
		t.Errorf("hooks.SessionStart = %q, want the user's group, then goldie's", got)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v (%v), want 0600 kept", fi.Mode().Perm(), err)
	}
	if got := fileContent(t, local); got != localDoc {
		t.Errorf("settings.local.json changed: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(sub, ".claude")); err == nil {
		t.Error("a .claude was created in the working directory, not the repository root")
	}
}

func TestCLIHooksInstallUpdatesAGoldieGroupThatLacksAHook(t *testing.T) {
	root := gitRepo(t, hooksOrigin)
	path := filepath.Join(root, ".claude", "settings.json")
	writeFile(t, path, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"goldie context --hook"}]}]}}`)
	stdout, code, stderr := installHooks(t, root)
	if code != 0 || stdout != hooksUpdated || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q, want 0 and %q", code, stdout, stderr, hooksUpdated)
	}
	wantOnlyGoldieHooks(t, fileContent(t, path))
}

func TestCLIHooksInstallRefusesWhatItCannotEditSafely(t *testing.T) {
	cases := []struct{ name, content string }{
		{"invalid JSON", `{"a": `},
		{"a byte order mark", "\ufeff{}\n"},
		{"a comment", "{\n// note\n}\n"},
		{"a trailing comma", `{"a": 1,}`},
		{"a duplicate key", `{"a": 1, "a": 2}`},
		{"hooks of the wrong type", `{"hooks": []}`},
		{"SessionStart of the wrong type", `{"hooks": {"SessionStart": {}}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := gitRepo(t, hooksOrigin)
			path := filepath.Join(root, ".claude", "settings.json")
			writeFile(t, path, c.content)
			wantRefusal(t, root, path, c.content)
		})
	}

	t.Run("a file over the read cap", func(t *testing.T) {
		root := gitRepo(t, hooksOrigin)
		path := filepath.Join(root, ".claude", "settings.json")
		// Valid JSON, so the size is the only fault.
		big := "{" + strings.Repeat(" ", 64<<20) + "}"
		writeFile(t, path, big)
		wantRefusal(t, root, path, big)
	})

	t.Run("settings.json is a symlink", func(t *testing.T) {
		root := gitRepo(t, hooksOrigin)
		target := filepath.Join(root, "elsewhere.json")
		writeFile(t, target, "{}\n")
		path := filepath.Join(root, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("no symlinks: %v", err)
		}
		wantRefusal(t, root, "", "")
		if got := fileContent(t, target); got != "{}\n" {
			t.Errorf("the link's target was written: %q", got)
		}
		if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("settings.json is no longer a symlink (%v)", err)
		}
	})

	t.Run("settings.json is a directory", func(t *testing.T) {
		root := gitRepo(t, hooksOrigin)
		if err := os.MkdirAll(filepath.Join(root, ".claude", "settings.json"), 0o755); err != nil {
			t.Fatal(err)
		}
		wantRefusal(t, root, "", "")
	})

	t.Run(".claude is a symlink", func(t *testing.T) {
		root := gitRepo(t, hooksOrigin)
		elsewhere := filepath.Join(root, "elsewhere")
		if err := os.MkdirAll(elsewhere, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(root, ".claude")); err != nil {
			t.Skipf("no symlinks: %v", err)
		}
		wantRefusal(t, root, "", "")
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("something was written through the .claude link: %v", entries)
		}
	})

	t.Run("not inside a git repository", func(t *testing.T) {
		dir := t.TempDir()
		wantRefusal(t, dir, "", "")
		if _, err := os.Lstat(filepath.Join(dir, ".claude")); err == nil {
			t.Error(".claude was created outside a repository")
		}
	})
}

// wantRefusal runs install in dir and checks the refusal: exit 1, nothing on
// stdout, one line that says what is wrong plus a next-step line, no parser
// text, and the file at path (when given) holds want, with no temporary file
// left beside it.
func wantRefusal(t *testing.T, dir, path, want string) {
	t.Helper()
	stdout, code, stderr := installHooks(t, dir)
	if code != 1 || stdout != "" {
		t.Errorf("exit %d, stdout %q, want 1 and nothing", code, stdout)
	}
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "goldie: ") || strings.TrimSpace(lines[1]) == "" {
		t.Errorf("stderr = %q, want one line that says what is wrong and a next-step line", stderr)
	}
	for _, w := range []string{"invalid character", "unexpected", "looking for", "offset", "cannot unmarshal", "EOF", dir} {
		if strings.Contains(stderr, w) {
			t.Errorf("stderr %q echoes %q", stderr, w)
		}
	}
	if path == "" {
		return
	}
	if got := fileContent(t, path); got != want {
		t.Errorf("a refusal changed the file (%d bytes, want %d)", len(got), len(want))
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(path) {
			t.Errorf("a refusal left %q beside settings.json", e.Name())
		}
	}
}

func TestCLIHooksMisuseExitsTwo(t *testing.T) {
	root := gitRepo(t, hooksOrigin)
	for _, args := range [][]string{{"hooks"}, {"hooks", "uninstall"}, {"hooks", "install", "extra"}, {"hooks", "list"}, {"hooks", "install", "--no-such-flag"}} {
		stdout, err := runCLIIn(t, root, args...)
		var errBuf bytes.Buffer
		if code := exitCode(err, &errBuf); code != 2 || stdout != "" {
			t.Errorf("%v: exit %d, stdout %q, want 2 and nothing", args, code, stdout)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, ".claude")); err == nil {
		t.Error("a misused command wrote .claude")
	}
}

func TestCLIHooksHelpSaysItRunsAtEverySessionStartAndSharesWhenCommitted(t *testing.T) {
	stdout, err := runCLI(t, "hooks", "--help")
	if err != nil {
		t.Fatal(err)
	}
	low := strings.ToLower(strings.Join(strings.Fields(stdout), " "))
	for _, want := range []string{"every session start", "committing .claude/settings.json", "team", "install"} {
		if !strings.Contains(low, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
}
