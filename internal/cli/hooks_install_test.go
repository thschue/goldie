package cli_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/cli"
)

// Tests for #670: cli.InstallSessionStartHook(file []byte) ([]byte,
// cli.HookStatus, error) is the pure, bytes-in bytes-out core of goldie hooks
// install. The status describes the input file: cli.HookAdded (no goldie hook
// yet), cli.HookCurrent (goldie's group is as wanted) or cli.HookUpdated
// (goldie's hooks were there but not as wanted). An empty or nil input is a
// missing file. cli.HookContextCommand and cli.HookPullCommand are the two
// command strings. The tests pin the JSON that results and which bytes
// survive, not the whitespace of the inserted text.

const userHooks = `{
  "permissions": {"allow": ["Bash(ls)"]},
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "echo mine"}]}],
    "SessionStart": [{"hooks": [{"type": "command", "command": "echo my own start"}]}]
  }
}
`

func hookInstall(t *testing.T, file string) (string, cli.HookStatus) {
	t.Helper()
	out, st, err := cli.InstallSessionStartHook([]byte(file))
	if err != nil {
		t.Fatalf("InstallSessionStartHook(%q): %v", file, err)
	}
	return string(out), st
}

// sessionStartCommands returns the command of every hook under
// hooks.SessionStart of the JSON document doc, group by group.
func sessionStartCommands(t *testing.T, doc string) [][]string {
	t.Helper()
	var v struct {
		Hooks struct {
			SessionStart []struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("not valid JSON: %v\n%q", err, doc)
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

// goldieGroups returns the groups that hold one of goldie's commands.
func goldieGroups(t *testing.T, doc string) [][]string {
	t.Helper()
	var out [][]string
	for _, g := range sessionStartCommands(t, doc) {
		for _, c := range g {
			if c == cli.HookContextCommand || c == cli.HookPullCommand {
				out = append(out, g)
				break
			}
		}
	}
	return out
}

// wantGoldieGroup fails unless doc has exactly one goldie group, holding the
// two commands once each.
func wantGoldieGroup(t *testing.T, doc string) {
	t.Helper()
	gs := goldieGroups(t, doc)
	if len(gs) != 1 {
		t.Fatalf("want exactly one goldie group, got %v in:\n%s", gs, doc)
	}
	if len(gs[0]) != 2 || gs[0][0] != cli.HookContextCommand || gs[0][1] != cli.HookPullCommand {
		t.Errorf("goldie group = %q, want [%q %q]", gs[0], cli.HookContextCommand, cli.HookPullCommand)
	}
}

// isSubsequence reports whether every byte of small appears in big, in order.
func isSubsequence(small, big string) bool {
	i := 0
	for j := 0; j < len(big) && i < len(small); j++ {
		if big[j] == small[i] {
			i++
		}
	}
	return i == len(small)
}

func TestHookCommandsParseAgainstTheRealFlagSets(t *testing.T) {
	if cli.HookContextCommand != "goldie context --hook" {
		t.Errorf("HookContextCommand = %q", cli.HookContextCommand)
	}
	if cli.HookPullCommand != "goldie pull --quiet --if-pulled" {
		t.Errorf("HookPullCommand = %q", cli.HookPullCommand)
	}
	for _, cmd := range []string{cli.HookContextCommand, cli.HookPullCommand} {
		words := strings.Fields(cmd)
		if len(words) < 2 || words[0] != "goldie" {
			t.Fatalf("%q does not start with goldie <command>", cmd)
		}
		fs := flag.NewFlagSet(words[1], flag.ContinueOnError)
		fs.SetOutput(&bytes.Buffer{})
		cli.Command(words[1], fs)
		if err := fs.Parse(words[2:]); err != nil {
			t.Errorf("%q does not parse against the flags of %s: %v", cmd, words[1], err)
			continue
		}
		if fs.NArg() != 0 {
			t.Errorf("%q leaves arguments %q after the flags", cmd, fs.Args())
		}
		for _, w := range words[2:] {
			if !strings.HasPrefix(w, "--") || fs.Lookup(strings.TrimPrefix(w, "--")) == nil {
				t.Errorf("%q: %q is not a flag of %s", cmd, w, words[1])
			}
		}
	}
}

func TestInstallSessionStartHook(t *testing.T) {
	t.Run("a missing file becomes only goldie's entry", func(t *testing.T) {
		for _, in := range []string{"", "\n"} {
			out, st := hookInstall(t, in)
			if st != cli.HookAdded {
				t.Errorf("%q: status = %v, want HookAdded", in, st)
			}
			wantGoldieGroup(t, out)
			var v map[string]any
			if err := json.Unmarshal([]byte(out), &v); err != nil || len(v) != 1 {
				t.Errorf("%q: want a JSON object with only the hooks key, got %v (%v)", in, v, err)
			}
			if got := sessionStartCommands(t, out); len(got) != 1 {
				t.Errorf("%q: want one group in total, got %v", in, got)
			}
		}
	})

	t.Run("an empty object gets the entry", func(t *testing.T) {
		out, st := hookInstall(t, "{}\n")
		if st != cli.HookAdded {
			t.Errorf("status = %v, want HookAdded", st)
		}
		wantGoldieGroup(t, out)
	})

	t.Run("every other byte of a file with a missing hooks key survives", func(t *testing.T) {
		for _, in := range []string{
			"{\n    \"model\": \"opus\",\n    \"env\": {\"A\": \"1\"}\n}\n",
			"{\r\n\t\"model\": \"opus\"\r\n}\r\n",
			"{\"model\":\"opus\"}",
			"  {  \"z\" : 1 ,\n \"a\" : [ 3 , 2 ]  }  \n\n",
		} {
			out, st := hookInstall(t, in)
			if st != cli.HookAdded {
				t.Errorf("%q: status = %v, want HookAdded", in, st)
			}
			wantGoldieGroup(t, out)
			if !isSubsequence(in, out) {
				t.Errorf("a byte of the input was changed or dropped:\nin  %q\nout %q", in, out)
			}
		}
	})

	t.Run("hooks without SessionStart gets the event and keeps the other events", func(t *testing.T) {
		in := "{\n  \"hooks\": {\n    \"PreToolUse\": [{\"hooks\": [{\"type\": \"command\", \"command\": \"echo mine\"}]}]\n  }\n}\n"
		out, st := hookInstall(t, in)
		if st != cli.HookAdded {
			t.Errorf("status = %v, want HookAdded", st)
		}
		wantGoldieGroup(t, out)
		if !isSubsequence(in, out) {
			t.Errorf("a byte of the input changed:\n%q", out)
		}
	})

	t.Run("a user's own SessionStart group stays and goldie's group is added beside it", func(t *testing.T) {
		out, st := hookInstall(t, userHooks)
		if st != cli.HookAdded {
			t.Errorf("status = %v, want HookAdded", st)
		}
		wantGoldieGroup(t, out)
		if !isSubsequence(userHooks, out) {
			t.Errorf("a byte of the user's file changed:\n%s", out)
		}
		if !strings.Contains(out, `"command": "echo my own start"`) || !strings.Contains(out, `"matcher": "Bash"`) {
			t.Errorf("the user's hooks were changed:\n%s", out)
		}
		if got := sessionStartCommands(t, out); len(got) != 2 {
			t.Errorf("want the user's group and goldie's, got %v", got)
		}
	})

	t.Run("a second run changes no bytes", func(t *testing.T) {
		for _, in := range []string{"", "{}", userHooks, "{\r\n\t\"a\": 1\r\n}\r\n"} {
			first, _ := hookInstall(t, in)
			second, st := hookInstall(t, first)
			if st != cli.HookCurrent {
				t.Errorf("%q: second status = %v, want HookCurrent", in, st)
			}
			if second != first {
				t.Errorf("%q: the second run changed bytes:\nfirst  %q\nsecond %q", in, first, second)
			}
		}
	})

	t.Run("a file with goldie's group as wanted is current and untouched", func(t *testing.T) {
		in := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"goldie context --hook"},{"type":"command","command":"goldie pull --quiet --if-pulled"}]}]}}`
		out, st := hookInstall(t, in)
		if st != cli.HookCurrent || out != in {
			t.Errorf("status = %v, bytes changed = %v", st, out != in)
		}
	})

	t.Run("a goldie group with one of the two hooks is updated and the user's hooks stay", func(t *testing.T) {
		in := "{\n  \"hooks\": {\n    \"SessionStart\": [\n      {\"hooks\": [{\"type\": \"command\", \"command\": \"echo my own start\"}]},\n      {\"hooks\": [{\"type\": \"command\", \"command\": \"goldie context --hook\"}]}\n    ]\n  }\n}\n"
		out, st := hookInstall(t, in)
		if st != cli.HookUpdated {
			t.Errorf("status = %v, want HookUpdated", st)
		}
		wantGoldieGroup(t, out)
		if !strings.Contains(out, `{"hooks": [{"type": "command", "command": "echo my own start"}]},`) {
			t.Errorf("the user's group changed:\n%s", out)
		}
		if got := sessionStartCommands(t, out); len(got) != 2 {
			t.Errorf("want the user's group and goldie's, got %v", got)
		}
		again, st := hookInstall(t, out)
		if st != cli.HookCurrent || again != out {
			t.Errorf("after the update: status = %v, changed = %v, want HookCurrent and no change", st, again != out)
		}
	})

	t.Run("a command that only resembles goldie's is the user's and is left alone", func(t *testing.T) {
		in := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"goldie context --hook --json"},{"type":"command","command":"/usr/bin/goldie pull --quiet --if-pulled"}]}]}}`
		out, st := hookInstall(t, in)
		if st != cli.HookAdded {
			t.Errorf("status = %v, want HookAdded", st)
		}
		if !isSubsequence(in, out) {
			t.Errorf("the user's hooks changed:\n%s", out)
		}
		wantGoldieGroup(t, out)
	})

	t.Run("the input slice is not modified", func(t *testing.T) {
		in := []byte(userHooks)
		if _, _, err := cli.InstallSessionStartHook(in); err != nil {
			t.Fatal(err)
		}
		if string(in) != userHooks {
			t.Error("the input slice was modified")
		}
	})
}

func TestInstallSessionStartHookRefusals(t *testing.T) {
	cases := []struct{ name, in string }{
		{"invalid JSON", `{"a": `},
		{"not JSON at all", "hello"},
		{"a byte order mark", "\ufeff{}"},
		{"a line comment", "{\n// note\n\"a\": 1}"},
		{"a block comment", `{"a": /* x */ 1}`},
		{"a trailing comma in an object", `{"a": 1,}`},
		{"a trailing comma in an array", `{"a": [1,]}`},
		{"a duplicate top-level key", `{"a": 1, "a": 2}`},
		{"a duplicate hooks key", `{"hooks": {}, "hooks": {}}`},
		{"a duplicate key deep inside", `{"hooks": {"SessionStart": [{"hooks": [], "hooks": []}]}}`},
		{"a duplicate key behind an escape", `{"a": 1, "\u0061": 2}`},
		{"hooks as an array", `{"hooks": []}`},
		{"hooks as a string", `{"hooks": "none"}`},
		{"hooks as a number", `{"hooks": 1}`},
		{"SessionStart as an object", `{"hooks": {"SessionStart": {}}}`},
		{"SessionStart as a string", `{"hooks": {"SessionStart": "x"}}`},
	}
	// What a parser says about a position in the file is never echoed.
	parserWords := []string{"invalid character", "unexpected", "looking for", "offset", "cannot unmarshal", "EOF"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := []byte(c.in)
			out, _, err := cli.InstallSessionStartHook(in)
			if err == nil {
				t.Fatalf("want an error, got output %q", out)
			}
			if len(out) != 0 && !bytes.Equal(out, in) {
				t.Errorf("a refusal must not return changed bytes: %q", out)
			}
			for _, w := range parserWords {
				if strings.Contains(err.Error(), w) {
					t.Errorf("error %q echoes the parser's text (%q)", err, w)
				}
			}
		})
	}
}
