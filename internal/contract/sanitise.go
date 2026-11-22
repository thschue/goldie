package contract

import (
	"strings"
	"unicode"
)

// Text an API client receives from the server is untrusted: a Source wrote
// it. Every client that shows it to a person or an agent cleans it here, so
// the clients cannot drift apart on what they let through.

// UnsafeRune reports the runes no client passes on: C0 controls, DEL, C1
// controls and every format character (Unicode category Cf: bidirectional
// controls, zero-width characters, the byte-order mark, tag characters).
// They can drive a terminal or hide and reorder text a reader sees.
// CleanText keeps newline and tab, which are C0 controls, and CleanText and
// CleanLine also drop the invisible fillers of invisibleFiller.
func UnsafeRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.Is(unicode.Cf, r)
}

// invisibleFiller reports the runes outside category Cf that render as
// nothing, so server text could hide characters behind them: the Hangul
// fillers U+115F, U+1160, U+3164 and U+FFA0, and the variation selectors
// U+FE00 to U+FE0F and U+E0100 to U+E01EF. Dropping a variation selector
// only changes how its neighbour is drawn, such as an emoji's text or colour
// form; the neighbour stays.
func invisibleFiller(r rune) bool {
	switch {
	case r == 0x115f, r == 0x1160, r == 0x3164, r == 0xffa0:
		return true
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef:
		return true
	}
	return false
}

// CleanText drops every unsafe rune other than newline and tab, and every
// invisible filler, keeping the line and paragraph separators U+2028 and
// U+2029. It is for text that may span lines, such as an article's content.
// Invalid UTF-8 becomes U+FFFD, so the result is always valid UTF-8 and no
// stray byte reaches a terminal as an 8-bit control.
func CleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if (r != '\n' && r != '\t' && UnsafeRune(r)) || invisibleFiller(r) {
			return -1
		}
		return r
	}, s)
}

// CleanLine is CleanText for a single-line field: newline, tab, U+2028 and
// U+2029 become spaces, so server text cannot forge extra lines, fields or
// entries.
func CleanLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n', r == '\t', r == 0x2028, r == 0x2029:
			return ' '
		case UnsafeRune(r), invisibleFiller(r):
			return -1
		}
		return r
	}, s)
}
