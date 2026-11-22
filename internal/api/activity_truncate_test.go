package api

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A query cut at maxQueryBytes never ends in half a rune: the result is the
// longest whole-rune prefix, in history and in live events.
func TestActivityTruncationKeepsWholeRunes(t *testing.T) {
	for _, r := range []string{"é", "€", "😀"} { // 2, 3 and 4 bytes
		for pad := maxQueryBytes - len(r) + 1; pad < maxQueryBytes; pad++ {
			// The rune starts at byte pad and straddles byte maxQueryBytes.
			q := strings.Repeat("a", pad) + r + "tail"
			want := q[:pad]
			a, _ := newTestActivity(4)
			_, live, cancel := a.Subscribe()
			a.Record(Event{Kind: KindSearch, Query: q})
			history, _, c2 := a.Subscribe()
			got := []string{history[0].Query, (<-live).Query}
			cancel()
			c2()
			for i, g := range got {
				if !utf8.ValidString(g) || len(g) > maxQueryBytes || g != want {
					t.Errorf("rune %q at byte %d, source %d: got %d bytes (valid %v), want the %d-byte whole-rune prefix", r, pad, i, len(g), utf8.ValidString(g), len(want))
				}
			}
		}
	}
}

func TestActivityTruncationLeavesARuneEndingAtTheCapWhole(t *testing.T) {
	q := strings.Repeat("a", maxQueryBytes-2) + "é" + "tail"
	a, _ := newTestActivity(4)
	a.Record(Event{Kind: KindSearch, Query: q})
	history, _, cancel := a.Subscribe()
	defer cancel()
	if got := history[0].Query; got != q[:maxQueryBytes] {
		t.Errorf("got %d bytes, want the rune ending at the cap kept (%d bytes)", len(got), maxQueryBytes)
	}
}

// Invalid UTF-8 is cut without panicking, backing off at most three bytes.
func TestActivityTruncationOfInvalidUTF8StaysBounded(t *testing.T) {
	for name, q := range map[string]string{
		"lone continuation bytes": strings.Repeat("\x80", maxQueryBytes+10),
		"lone lead bytes":         strings.Repeat("\xff", maxQueryBytes+10),
		"truncated lead":          strings.Repeat("a", maxQueryBytes-1) + "\xf0\x9f\x98",
	} {
		a, _ := newTestActivity(4)
		a.Record(Event{Kind: KindSearch, Query: q})
		history, _, cancel := a.Subscribe()
		cancel()
		got := history[0].Query
		if len(got) > maxQueryBytes || len(got) < maxQueryBytes-3 || !strings.HasPrefix(q, got) {
			t.Errorf("%s: got %d bytes, want a prefix of %d to %d bytes", name, len(got), maxQueryBytes-3, maxQueryBytes)
		}
	}
}
