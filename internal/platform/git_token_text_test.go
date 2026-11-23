package platform

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// #96 (folded in from #392): Setting drops every trailing CR and LF of a
// _FILE value (TrimRight "\r\n"), and the GOLDIE_GIT_TOKEN row and Setting's
// doc comment say so, as the SSH key text already does. The committed
// reference carries the row (./run regen). goldie server now serves the git
// Source, so the GOLDIE_GIT_URL text no longer says it only validates it.

// saysTrailingCRAndLF reports whether text says every trailing CR and LF is
// dropped.
func saysTrailingCRAndLF(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(text, "CR") && strings.Contains(text, "LF") &&
		strings.Contains(lower, "trailing") && strings.Contains(lower, "every")
}

func TestGitTokenTextSaysEveryTrailingCRAndLFIsDropped(t *testing.T) {
	meaning := settingRow(t, EnvGitToken).Meaning
	if !saysTrailingCRAndLF(meaning) {
		t.Errorf("%s description does not say every trailing CR and LF is dropped: %q", EnvGitToken, meaning)
	}
	var row string
	for _, line := range strings.Split(committedReference(t), "\n") {
		if strings.HasPrefix(line, "| `"+EnvGitToken+"` |") {
			row = line
		}
	}
	if !saysTrailingCRAndLF(row) {
		t.Errorf("committed reference row for %s does not say every trailing CR and LF is dropped (run: ./run regen): %q", EnvGitToken, row)
	}
}

func TestSettingDocSaysEveryTrailingCRAndLFIsDropped(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "config.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var doc string
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "Setting" {
			doc = fn.Doc.Text()
		}
	}
	if doc == "" {
		t.Fatal("config.go has no documented func Setting")
	}
	if !saysTrailingCRAndLF(doc) {
		t.Errorf("Setting's doc comment does not say every trailing CR and LF is dropped:\n%s", doc)
	}
}

// What the text says is what Setting does: every trailing CR and LF goes, in
// any order and number, and nothing before them.
func TestSettingDropsEveryTrailingCRAndLF(t *testing.T) {
	for raw, want := range map[string]string{
		"tok\n":           "tok",
		"tok\r\n":         "tok",
		"tok\r\n\r\n":     "tok",
		"tok\n\r":         "tok",
		"tok\r\r\n\n\r":   "tok",
		"t\r\nok\r\n":     "t\r\nok",
		" tok \t\n":       " tok \t",
		"\r\n\r\n":        "",
		"tok\u2028\n":     "tok\u2028",
		"tok\x00\r\n":     "tok\x00",
		"tok\r\n" + "x\n": "tok\r\nx",
	} {
		lookup := func(k string) (string, bool) {
			if k == EnvGitToken+"_FILE" {
				return "/secret/token", true
			}
			return "", false
		}
		read := func(string) ([]byte, error) { return []byte(raw), nil }
		got, err := Setting(lookup, read, EnvGitToken)
		if err != nil {
			t.Fatalf("Setting(%q): %v", raw, err)
		}
		if got != want {
			t.Errorf("Setting(%q) = %q, want %q", raw, got, want)
		}
	}
	_, err := Setting(func(k string) (string, bool) { return "/x", k == EnvGitToken+"_FILE" },
		func(string) ([]byte, error) { return nil, errors.New("no") }, EnvGitToken)
	if err == nil || !strings.Contains(err.Error(), EnvGitToken+"_FILE") || strings.Contains(err.Error(), "/x") {
		t.Errorf("unreadable file: err = %v, want it naming %s_FILE and not the path", err, EnvGitToken)
	}
}

func TestGitURLTextNoLongerSaysTheServerOnlyValidatesIt(t *testing.T) {
	for where, text := range map[string]string{
		"description":         settingRow(t, EnvGitURL).Meaning,
		"committed reference": committedReference(t),
	} {
		lower := strings.ToLower(text)
		for _, stale := range []string{"does not serve a git source yet", "only validated"} {
			if strings.Contains(lower, stale) {
				t.Errorf("%s still says %q: goldie server serves the git Source now (run: ./run regen)", where, stale)
			}
		}
	}
}
