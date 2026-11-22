package contract_test

import (
	"testing"
	"unicode"

	"github.com/thschue/goldie/internal/contract"
)

// Acceptance tests for #249: one exported predicate for hidden runes,
//
//	func HiddenRune(r rune) bool
//
// used by CleanText, CleanLine, the CLI's --json writer and the CLI's
// configured-Project check. It covers what the cleaners dropped after #163 and
// #235 (C0 controls, DEL, C1 controls, every Cf format character, the Hangul
// fillers and the variation selectors) plus the invisible runes U+034F,
// U+180B to U+180D, U+180F and U+2800. Like the old UnsafeRune it reports
// newline and tab as hidden: CleanText keeps those two itself.

// addedHidden is what #249 adds. U+180E is Cf and was already hidden.
var addedHidden = []rune{0x034f, 0x180b, 0x180c, 0x180d, 0x180f, 0x2800}

// visibleNeighbours sit on either side of each added rune or range and render
// as something, so they must survive: the combining marks U+034E and U+0350,
// the Mongolian NIRUGU U+180A and digit zero U+1810, U+27FF and the Braille
// pattern dots-1 U+2801.
var visibleNeighbours = []rune{0x034e, 0x0350, 0x180a, 0x1810, 0x27ff, 0x2801}

// hiddenOracle restates the spec independently of the implementation.
func hiddenOracle(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case unicode.Is(unicode.Cf, r):
		return true
	case r == 0x115f, r == 0x1160, r == 0x3164, r == 0xffa0:
		return true
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef:
		return true
	case r == 0x034f, r >= 0x180b && r <= 0x180d, r == 0x180f, r == 0x2800:
		return true
	}
	return false
}

func TestHiddenRuneCoversEverythingTheCleanersDropPlusTheAddedRunes(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue // surrogates are not runes in a Go string
		}
		if got, want := contract.HiddenRune(r), hiddenOracle(r); got != want {
			t.Errorf("HiddenRune(%U) = %v, want %v", r, got, want)
		}
	}
}

func TestHiddenRuneNamesTheAddedRunesAndSparesTheirNeighbours(t *testing.T) {
	for _, r := range addedHidden {
		if !contract.HiddenRune(r) {
			t.Errorf("HiddenRune(%U) = false, want true", r)
		}
	}
	for _, r := range visibleNeighbours {
		if contract.HiddenRune(r) {
			t.Errorf("HiddenRune(%U) = true, want false: it is visible", r)
		}
	}
	for _, r := range []rune{'a', ' ', 'é', '日', '👍', 0x2028, 0x2029, 0xa0} {
		if contract.HiddenRune(r) {
			t.Errorf("HiddenRune(%U) = true, want false", r)
		}
	}
}

// Every rune the predicate names is dropped by both cleaners, one at a time,
// with a visible neighbour on each side that survives; newline and tab are
// the only exceptions, kept (CleanText) or made a space (CleanLine).
func TestCleanersDropExactlyTheHiddenRunes(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue
		}
		in := "x" + string(r) + "y"
		text, line := "xy", "xy"
		switch {
		case r == '\n' || r == '\t', r == 0x2028 || r == 0x2029:
			text, line = in, "x y"
		case !hiddenOracle(r):
			text, line = in, in
		}
		if got := contract.CleanText(in); got != text {
			t.Errorf("CleanText(%q) = %q, want %q", in, got, text)
		}
		if got := contract.CleanLine(in); got != line {
			t.Errorf("CleanLine(%q) = %q, want %q", in, got, line)
		}
	}
}

func TestCleanersDropTheAddedRunesAndKeepTheirNeighbours(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"combining grapheme joiner", "a͏b", "ab"},
		{"Mongolian free variation selectors", "a᠋b᠌c᠍d", "abcd"},
		{"Mongolian free variation selector four", "a᠏b", "ab"},
		{"Braille blank", "a⠀b", "ab"},
		{"added runes at both ends", "͏᠋᠌᠍᠏⠀x⠀᠏", "x"},
		{"a blank Braille tail", "name⠀⠀⠀", "name"},
		{"visible neighbours", "͎͐᠊᠐⟿⠁", "͎͐᠊᠐⟿⠁"},
		{"ordinary Braille", "⠁⠂⠿", "⠁⠂⠿"},
		{"neighbours around the added runes", "͎͏͐᠊᠋᠍᠏᠐⟿⠀⠁", "͎͐᠊᠐⟿⠁"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := contract.CleanText(c.in); got != c.want {
				t.Errorf("CleanText(%q) = %q, want %q", c.in, got, c.want)
			}
			if got := contract.CleanLine(c.in); got != c.want {
				t.Errorf("CleanLine(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
