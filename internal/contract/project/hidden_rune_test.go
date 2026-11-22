package project

import (
	"strings"
	"testing"
)

// Acceptance tests for #249: a configured Project id carrying a hidden rune is
// refused through the contract's one predicate, with the same non-echoing
// answer the other unreadable values get, not the generic shape message.
var hiddenInProject = []struct {
	name string
	r    rune
}{
	{"U+034F combining grapheme joiner", 0x034f},
	{"U+180B Mongolian free variation selector one", 0x180b},
	{"U+180C", 0x180c},
	{"U+180D", 0x180d},
	{"U+180F", 0x180f},
	{"U+2800 Braille blank", 0x2800},
	// Hidden before #249; they must keep their answer.
	{"U+200B zero-width space", 0x200b},
	{"NUL", 0},
	{"DEL", 0x7f},
	// Hidden by #235's cleaners but not by the old UnsafeRune.
	{"U+3164 Hangul filler", 0x3164},
	{"U+FE0F variation selector", 0xfe0f},
	{"U+E0100 variation selector supplement", 0xe0100},
}

func TestNormaliseConfiguredRejectsHiddenRunes(t *testing.T) {
	const want = "the Project is not a host/org/repo name or a git remote URL"
	for _, c := range hiddenInProject {
		for _, shape := range []string{"ex.com/org/re%sport", "ex.com/org%s/repo", "ex%s.com/org/repo", "%sex.com/org/repo", "ex.com/org/repo%s"} {
			in := strings.Replace(shape, "%s", string(c.r), 1)
			got, err := normaliseConfigured(in)
			if err == nil {
				t.Errorf("%s: normaliseConfigured(%q) = %q, want an error", c.name, in, got)
				continue
			}
			if err.Error() != want {
				t.Errorf("%s: normaliseConfigured(%q) error = %q, want %q", c.name, in, err, want)
			}
			if strings.ContainsRune(err.Error(), c.r) || strings.Contains(err.Error(), "ex.com") {
				t.Errorf("%s: error echoes the value: %q", c.name, err)
			}
		}
	}
}

func TestNormaliseConfiguredStillAcceptsAVisibleProject(t *testing.T) {
	if got, err := normaliseConfigured("Ex.com/Org/re-po"); err != nil || got != "ex.com/org/re-po" {
		t.Errorf("normaliseConfigured = %q, %v; want ex.com/org/re-po", got, err)
	}
}

// A hidden rune in a remote URL never becomes a Project either.
func TestNormaliseConfiguredRejectsHiddenRunesInARemote(t *testing.T) {
	for _, c := range hiddenInProject {
		for _, shape := range []string{"https://h.example/org/re%spo", "git@h.example:org/re%spo.git"} {
			in := strings.Replace(shape, "%s", string(c.r), 1)
			if got, err := normaliseConfigured(in); err == nil {
				t.Errorf("%s: normaliseConfigured(%q) = %q, want an error", c.name, in, got)
			}
		}
	}
}
