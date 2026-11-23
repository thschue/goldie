package app

import (
	"testing"
	"unicode"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// Issue #317: the engine may not import contract (depguard), so it keeps its
// own copy of contract.HiddenRune (#249), exported as engine.HiddenRune for
// Item references. app imports both, so this is where the two copies are held
// together: they agree on every code point.
func TestEngineHiddenRuneAgreesWithContractOnEveryRune(t *testing.T) {
	var diffs int
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if got, want := engine.HiddenRune(r), contract.HiddenRune(r); got != want {
			if diffs++; diffs <= 20 {
				t.Errorf("engine.HiddenRune(%U) = %v, contract.HiddenRune = %v", r, got, want)
			}
		}
	}
	if diffs > 20 {
		t.Errorf("... %d runes differ in all", diffs)
	}
}

// The agreement above would also hold if both copies said false for
// everything; the copies must name the hidden runes and spare the visible
// ones.
func TestEngineHiddenRuneNamesHiddenRunesAndSparesVisibleOnes(t *testing.T) {
	for _, r := range []rune{0x00, 0x1f, 0x7f, 0x85, 0x200b, 0x202e, 0xfeff, 0xe0041, 0x034f, 0x115f, 0x3164, 0xffa0, 0x180b, 0x180f, 0x2800, 0xfe0f, 0xe0100} {
		if !engine.HiddenRune(r) {
			t.Errorf("engine.HiddenRune(%U) = false, want true", r)
		}
	}
	for _, r := range []rune{'a', 'Z', '0', '-', '/', '.', 0x00a0, 0x2028, 0x00e9, 0x034e, 0x2801, 0xfe10} {
		if engine.HiddenRune(r) {
			t.Errorf("engine.HiddenRune(%U) = true, want false", r)
		}
	}
}
