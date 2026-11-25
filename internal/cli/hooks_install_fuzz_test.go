package cli

import (
	"bytes"
	"encoding/json"
	"testing"
)

// FuzzInstallSessionStartHook checks the splice for any input. A refusal
// returns no changed bytes. An accepted file is valid JSON, holds goldie's two
// commands exactly once each, keeps every byte of an input that was not
// goldie's to change, and a second run changes nothing.
func FuzzInstallSessionStartHook(f *testing.F) {
	for _, s := range []string{
		"", "{}", "{}\n", "\ufeff{}", `{"a":1,"a":2}`, `{"hooks":[]}`, `{"hooks":{"SessionStart":{}}}`,
		`{"hooks":{"SessionStart":[]}}`, `{"hooks":{}}`, "{\r\n\t\"a\": 1\r\n}\r\n", `{"a": 1,}`, "// c\n{}",
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"goldie context --hook"}]}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"goldie context --hook"},{"type":"command","command":"goldie pull --quiet --if-pulled"}]}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"x"}]}],"Stop":[]},"z":"\u0068ooks"}`,
		`{"hooks":{"\u0053essionStart":[]}}`, `{"a":"\u202e"}`, "[]", "null", `"s"`, "{", `{"hooks":null}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		orig := append([]byte(nil), in...)
		out, st, err := InstallSessionStartHook(in)
		if !bytes.Equal(in, orig) {
			t.Fatalf("the input slice was modified")
		}
		if err != nil {
			if len(out) != 0 && !bytes.Equal(out, orig) {
				t.Fatalf("a refusal returned changed bytes: %q", out)
			}
			return
		}
		if !json.Valid(out) {
			t.Fatalf("accepted %q but wrote invalid JSON %q", orig, out)
		}
		var v struct {
			Hooks struct {
				SessionStart []struct {
					Hooks []struct {
						Command string `json:"command"`
					} `json:"hooks"`
				} `json:"SessionStart"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(out, &v); err != nil {
			t.Fatalf("accepted %q but wrote a shape that does not parse: %v\n%q", orig, err, out)
		}
		n := map[string]int{}
		for _, g := range v.Hooks.SessionStart {
			for _, h := range g.Hooks {
				n[h.Command]++
			}
		}
		if n[HookContextCommand] != 1 || n[HookPullCommand] != 1 {
			t.Fatalf("want each goldie command once, got %v in %q", n, out)
		}
		if st == HookCurrent && !bytes.Equal(out, orig) {
			t.Fatalf("a current file changed: %q -> %q", orig, out)
		}
		if st == HookAdded {
			i := 0
			for _, b := range out {
				if i < len(orig) && b == orig[i] {
					i++
				}
			}
			if i != len(orig) {
				t.Fatalf("a byte of the input was changed or dropped:\nin  %q\nout %q", orig, out)
			}
		}
		again, st2, err := InstallSessionStartHook(out)
		if err != nil || st2 != HookCurrent || !bytes.Equal(again, out) {
			t.Fatalf("a second run was not a no-op: %v %v\n%q\n%q", err, st2, out, again)
		}
	})
}
