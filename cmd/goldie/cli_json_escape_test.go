package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// --json escapes format characters instead of dropping them, so the value
// survives: a tag character, outside the Basic Multilingual Plane, must be
// written as a UTF-16 surrogate pair, or it decodes to a different string.
func TestCLIJSONEscapesFormatCharactersWithoutChangingTheValue(t *testing.T) {
	h := jsonString(t, hiddenCf)
	body := `{"id":"x","name":` + h + `,"keywords":[],"content":"c"}`
	out, err := runCLI(t, "lookup", "x", "--addr", hostileServer(t, http.StatusOK, body), "--token", cliToken, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if leaked := formatChars(out); len(leaked) > 0 {
		t.Errorf("--json output carries raw format characters %v:\n%q", leaked, out)
	}
	var got struct{ Name string }
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%q", err, out)
	}
	if got.Name != hiddenCf {
		t.Errorf("--json name decodes to %q, want %q", got.Name, hiddenCf)
	}
}

// Acceptance tests for #249: goldie status --json never writes the runes the
// contract's one predicate names as raw characters. They are escaped (the
// value survives) or dropped. Visible neighbours stay raw and unchanged.

var hiddenRunesInStatusJSON = []struct {
	name string
	r    rune
}{
	{"U+034F combining grapheme joiner", 0x034f},
	{"U+180B", 0x180b},
	{"U+180C", 0x180c},
	{"U+180D", 0x180d},
	{"U+180F", 0x180f},
	{"U+2800 Braille blank", 0x2800},
	// Passed raw by printJSON before #249: the old predicate missed them.
	{"U+115F Hangul filler", 0x115f},
	{"U+3164 Hangul filler", 0x3164},
	{"U+FE0F variation selector", 0xfe0f},
	{"U+E0100 variation selector supplement", 0xe0100},
}

func statusJSONFromServer(t *testing.T, title, version string) (string, map[string]any) {
	t.Helper()
	body := `{"info":{"title":` + jsonString(t, title) + `,"version":` + jsonString(t, version) + `}}`
	out, err := runCLI(t, "status", "--json", "--addr", hostileServer(t, http.StatusOK, body))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("status --json is not JSON: %v\n%q", err, out)
	}
	return out, m
}

func TestCLIStatusJSONEscapesOrDropsHiddenRunes(t *testing.T) {
	for _, c := range hiddenRunesInStatusJSON {
		t.Run(c.name, func(t *testing.T) {
			title := "a" + string(c.r) + "b"
			version := "v" + string(c.r) + "1"
			out, m := statusJSONFromServer(t, title, version)
			if strings.ContainsRune(out, c.r) {
				t.Errorf("--json output carries the raw rune %U:\n%q", c.r, out)
			}
			if api := m["api"]; api != title && api != "ab" {
				t.Errorf("api = %q, want %q (escaped) or %q (dropped)", api, title, "ab")
			}
			if v := m["apiVersion"]; v != version && v != "v1" {
				t.Errorf("apiVersion = %q, want %q (escaped) or %q (dropped)", v, version, "v1")
			}
		})
	}
}

func TestCLIStatusJSONKeepsVisibleNeighbours(t *testing.T) {
	for _, r := range []rune{0x034e, 0x0350, 0x180a, 0x1810, 0x27ff, 0x2801} {
		t.Run(fmt.Sprintf("U+%04X", r), func(t *testing.T) {
			title := "a" + string(r) + "b"
			out, m := statusJSONFromServer(t, title, "1")
			if !strings.ContainsRune(out, r) {
				t.Errorf("--json output lost the visible rune %U:\n%q", r, out)
			}
			if m["api"] != title {
				t.Errorf("api = %q, want %q", m["api"], title)
			}
		})
	}
}
