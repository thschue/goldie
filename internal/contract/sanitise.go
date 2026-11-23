package contract

import (
	"strings"
	"unicode"
)

// Text an API client receives from the server is untrusted: a Source wrote
// it. Every client that shows it to a person or an agent cleans it here, so
// the clients cannot drift apart on what they let through.

// HiddenRune reports the runes no client passes on, because a reader cannot
// see them or they drive a terminal: C0 controls, DEL, C1 controls, every
// format character (Unicode category Cf: bidirectional controls, zero-width
// characters, the byte-order mark, tag characters), and the invisible fillers
// outside Cf: the Hangul fillers U+115F, U+1160, U+3164 and U+FFA0, the
// combining grapheme joiner U+034F, the Mongolian free variation selectors
// U+180B to U+180D and U+180F, the blank Braille pattern U+2800, and the
// variation selectors U+FE00 to U+FE0F and U+E0100 to U+E01EF. Dropping a
// variation selector only changes how its neighbour is drawn, such as an
// emoji's text or colour form; the neighbour stays.
//
// Newline and tab are C0 controls, so they count as hidden here; CleanText
// keeps them and CleanLine makes them spaces. U+2028, U+2029 and U+00A0 are
// not hidden.
func HiddenRune(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x115f, r == 0x1160, r == 0x3164, r == 0xffa0:
		return true
	case r == 0x034f, r >= 0x180b && r <= 0x180d, r == 0x180f, r == 0x2800:
		return true
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef:
		return true
	}
	return unicode.Is(unicode.Cf, r)
}

// CleanText drops every hidden rune other than newline and tab, keeping the
// line and paragraph separators U+2028 and U+2029. It is for text that may
// span lines, such as an article's content. Invalid UTF-8 becomes U+FFFD, so
// the result is always valid UTF-8 and no stray byte reaches a terminal as an
// 8-bit control.
func CleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && HiddenRune(r) {
			return -1
		}
		return r
	}, s)
}

// CleanList joins l for one line: each entry cleaned with CleanLine, so it
// cannot forge extra fields or entries, joined with ", ". An empty list is
// "(none)".
func CleanList(l []string) string {
	if len(l) == 0 {
		return "(none)"
	}
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = CleanLine(s)
	}
	return strings.Join(out, ", ")
}

// CleanLine is CleanText for a single-line field: newline, tab, U+2028 and
// U+2029 become spaces, so server text cannot forge extra lines, fields or
// entries.
func CleanLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n', r == '\t', r == 0x2028, r == 0x2029:
			return ' '
		case HiddenRune(r):
			return -1
		}
		return r
	}, s)
}
