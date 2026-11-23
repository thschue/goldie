package engine_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/thschue/goldie/internal/engine"
)

// Acceptance tests for #317: an Item reference rejects the same runes as
// contract.HiddenRune (#249). The engine may not import contract, so it keeps
// its own copy, exported as
//
//	func HiddenRune(r rune) bool
//
// so that internal/app can pin that the two agree. Here the rule is pinned
// where a Source author meets it: a replaced_by or a goldie lookup line that
// names an Item through a hidden rune fails the load, even when an Item with
// exactly that spelling exists.

// hiddenSpec restates contract.HiddenRune's documented set (C0, DEL, C1, Cf,
// the Hangul fillers, U+034F, U+180B to U+180D, U+180F, U+2800 and the
// variation selectors) independently of both implementations.
func hiddenSpec(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case unicode.Is(unicode.Cf, r):
		return true
	case r == 0x115f, r == 0x1160, r == 0x3164, r == 0xffa0:
		return true
	case r == 0x034f, r >= 0x180b && r <= 0x180d, r == 0x180f, r == 0x2800:
		return true
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef:
		return true
	}
	return false
}

// invisibleOutsideCf are the hidden runes unicode.IsPrint admits, so a
// reference check built on IsPrint alone lets them through.
var invisibleOutsideCf = []rune{
	0xfe00, 0xfe0f, 0xe0100, 0xe01ef, // variation selectors
	0x034f,                         // combining grapheme joiner
	0x115f, 0x1160, 0x3164, 0xffa0, // Hangul fillers
	0x180b, 0x180c, 0x180d, 0x180f, // Mongolian free variation selectors
	0x2800, // blank Braille pattern
}

// notPrintable are private-use and unassigned code points: no reader can tell
// what they are, so a name must not hold them either.
var notPrintable = []rune{0xe000, 0xf8ff, 0xf0000, 0x10fffd, 0x0378, 0xfffe, 0x10ffff}

// visibleNeighbours sit next to a hidden rune or range and render as
// something, so a name may hold them.
var visibleNeighbours = []rune{
	0x034e, 0x0350, 0x115e, 0x1161, 0x3163, 0x3165, 0xffa1,
	0x180a, 0x1810, 0x27ff, 0x2801, 0xfe10, 0x00e9,
}

// replacedByFiles is a target article named a<r>b and a deprecated article
// whose replaced_by names it under that exact spelling. NUL cannot be in a
// file name, so its target is left out; the reference is still read.
func replacedByFiles(r rune) map[string]string {
	target := "a" + string(r) + "b"
	files := map[string]string{
		globalFile("ref"): govArticle("Ref", "status: deprecated", "replaced_by: "+strconv.QuoteToASCII("article:"+target)),
	}
	if r != 0 {
		files[globalFile(target)] = article("Target")
	}
	return files
}

// loadFails reports whether the Source fails to load with an error that names
// field, so a load that fails for another reason (YAML, file names) does not
// count as the reference being rejected.
func loadFails(t *testing.T, files map[string]string, field string) (bool, string) {
	t.Helper()
	_, err := engine.NewDirSource(writeFiles(t, files)).Load(t.Context())
	if err == nil {
		return false, ""
	}
	return strings.Contains(err.Error(), field), err.Error()
}

// Every rune contract.HiddenRune names fails the load when a replaced_by
// holds it, across the whole code point range.
func TestReplacedByRejectsEveryHiddenRune(t *testing.T) {
	var accepted []string
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !hiddenSpec(r) {
			continue
		}
		if ok, msg := loadFails(t, replacedByFiles(r), "replaced_by"); !ok {
			accepted = append(accepted, fmt.Sprintf("%U (%q)", r, msg))
		}
	}
	if len(accepted) > 0 {
		show := accepted
		if len(show) > 20 {
			show = show[:20]
		}
		t.Errorf("%d hidden runes loaded in a replaced_by, want every one rejected; first: %s",
			len(accepted), strings.Join(show, ", "))
	}
}

// The runes outside Cf that IsPrint admits are named one by one, with the
// error held to the loader's usual rules: it names the field and the file and
// never carries the rune raw.
func TestReplacedByRejectsTheInvisibleRunesOutsideCf(t *testing.T) {
	for _, r := range invisibleOutsideCf {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			msg := loadErr(t, replacedByFiles(r), globalFile("ref"))
			if !strings.Contains(msg, "replaced_by") {
				t.Errorf("error %q does not name the field replaced_by", msg)
			}
			if strings.ContainsRune(msg, r) {
				t.Errorf("error %q carries %U unescaped", msg, r)
			}
		})
	}
}

// Private-use and unassigned code points stay rejected.
func TestReplacedByRejectsPrivateUseAndUnassignedRunes(t *testing.T) {
	for _, r := range notPrintable {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			msg := loadErr(t, replacedByFiles(r), globalFile("ref"))
			if !strings.Contains(msg, "replaced_by") {
				t.Errorf("error %q does not name the field replaced_by", msg)
			}
		})
	}
}

// The negative: the visible neighbours of the hidden runes still resolve, so
// the rule is no wider than contract.HiddenRune at its edges.
func TestReplacedByAcceptsTheVisibleNeighboursOfHiddenRunes(t *testing.T) {
	for _, r := range visibleNeighbours {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			mustLoad(t, replacedByFiles(r))
		})
	}
}

// lookupFiles is a target article named a<r>b and an article whose fenced
// block looks it up under that exact spelling.
func lookupFiles(r rune) map[string]string {
	target := "a" + string(r) + "b"
	return map[string]string{
		globalFile(target): article("Target"),
		globalFile("doc"):  docWith("Doc", "```\ngoldie lookup "+target+"\n```"),
	}
}

// A goldie lookup line is read with the same reference rule, so a lookup id
// with a hidden rune is dangling and fails the load, and the error quotes the
// id escaped.
func TestLookupOfANameWithAHiddenRuneFailsTheLoad(t *testing.T) {
	for _, r := range append(append([]rune{0x200b, 0x202e, 0xfeff}, invisibleOutsideCf...), notPrintable...) {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			msg := loadErr(t, lookupFiles(r), globalFile("doc"))
			if !strings.Contains(msg, "goldie lookup") {
				t.Errorf("error %q does not name the goldie lookup line", msg)
			}
			if strings.ContainsRune(msg, r) {
				t.Errorf("error %q carries %U unescaped", msg, r)
			}
		})
	}
}

// The negative: a lookup of a name with a visible neighbour resolves.
func TestLookupOfANameWithAVisibleNeighbourResolves(t *testing.T) {
	for _, r := range visibleNeighbours {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			mustLoad(t, lookupFiles(r))
		})
	}
}
