package engine

import (
	"errors"
	"unicode"
)

// HiddenRune reports the runes an Item reference may not hold, because a
// reader cannot see them or they drive a terminal: C0 controls, DEL, C1
// controls, every format character (Unicode category Cf: bidirectional
// controls, zero-width characters, the byte-order mark, tag characters), and
// the invisible fillers outside Cf: the Hangul fillers U+115F, U+1160, U+3164
// and U+FFA0, the combining grapheme joiner U+034F, the Mongolian free
// variation selectors U+180B to U+180D and U+180F, the blank Braille pattern
// U+2800, and the variation selectors U+FE00 to U+FE0F and U+E0100 to
// U+E01EF. U+2028, U+2029 and U+00A0 are not hidden.
//
// It is the engine's own copy of contract.HiddenRune: the engine imports no
// other goldie module, so a test in internal/app holds the two copies to the
// same answer for every code point. Change both together.
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

// checkBody fails on a hidden rune in body text an author wrote, which an agent
// reads as instructions. Tabs and line breaks stay. The error never echoes the
// body or the rune.
func checkBody(body []byte) error {
	for _, r := range string(body) {
		if r != '\n' && r != '\r' && r != '\t' && HiddenRune(r) {
			return errors.New("body holds a hidden or control character")
		}
	}
	return nil
}
