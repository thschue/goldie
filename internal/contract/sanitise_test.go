package contract_test

import (
	"net/http"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/thschue/goldie/internal/contract"
)

// Acceptance tests for #128: one shared implementation, used by both cli and
// mcp, of the code that cleans untrusted server text and decodes the error
// envelope. The API it pins:
//
//	func CleanText(s string) string
//	func CleanLine(s string) string
//	type APIError struct { Status int; Message, Hint string }
//	func DecodeAPIError(status int, header http.Header, body []byte) APIError
//
// Each client words its own messages from an APIError. #174 added the header
// argument and APIError.RetryAfter (apierror_test.go).

// unsafeRune is what neither client may pass on: C0 controls other than
// newline and tab, DEL, C1 controls and every format character (Cf), which
// covers the bidirectional controls, zero-width characters, the byte-order
// mark and tag characters.
func unsafeRune(r rune) bool {
	return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.Is(unicode.Cf, r)
}

var cleanCases = []struct {
	name, in, text, line string
}{
	{"plain ASCII", "golden path", "golden path", "golden path"},
	{"visible Unicode and markup are kept", "Grüße 日本語 e\u0301 👍🏽 <script>`x`</script> <!-- c -->",
		"Grüße 日本語 e\u0301 👍🏽 <script>`x`</script> <!-- c -->", "Grüße 日本語 e\u0301 👍🏽 <script>`x`</script> <!-- c -->"},
	{"newline and tab", "a\nb\tc", "a\nb\tc", "a b c"},
	{"carriage return is a C0 control", "a\rb", "ab", "ab"},
	{"Unicode line and paragraph separators", "a\u2028b\u2029c", "a\u2028b\u2029c", "a b c"},
	{"NUL, BEL and an ANSI escape", "a\x00b\x07c\x1b[31md", "abc[31md", "abc[31md"},
	{"OSC title escape", "body\x1b]0;pwn\x07", "body]0;pwn", "body]0;pwn"},
	{"DEL", "a\x7fb", "ab", "ab"},
	{"C1 controls, NEL and CSI", "a\u0080b\u0085c\u009bd\u009fe", "abcde", "abcde"},
	{"bidirectional controls", "a\u202ab\u202bc\u202cd\u202de\u202ef\u2066g\u2067h\u2068i\u2069j\u200ek\u200fl\u061cm",
		"abcdefghijklm", "abcdefghijklm"},
	{"zero-width characters and word joiner", "a\u200bb\u200cc\u200dd\u2060e", "abcde", "abcde"},
	{"byte-order mark", "\ufeffa\ufeffb", "ab", "ab"},
	{"tag characters", "a\U000E0001b\U000E0049\U000E0047c\U000E007Fd", "abcd", "abcd"},
	{"other format characters", "a\u00adb\u180ec\ufff9d\ufffbe\u2062f\u206ag", "abcdefg", "abcdefg"},
	{"a forged line in a single-line field", "name\n- id: forged", "name\n- id: forged", "name - id: forged"},
	{"empty", "", "", ""},
	// #163: Hangul fillers and variation selectors are not Cf, but they are
	// invisible, so server text could hide characters behind them. Each sits
	// between visible characters that must survive.
	{"Hangul choseong filler U+115F", "aᅟb", "ab", "ab"},
	{"Hangul jungseong filler U+1160", "aᅠb", "ab", "ab"},
	{"Hangul filler U+3164", "aㅤb", "ab", "ab"},
	{"halfwidth Hangul filler U+FFA0", "aﾠb", "ab", "ab"},
	{"Hangul fillers between Hangul syllables", "한ᅟᅠ글ㅤﾠ판", "한글판", "한글판"},
	{"variation selectors U+FE00 to U+FE0F", "a︀b︁c︇d︎e️f", "abcdef", "abcdef"},
	{"variation selectors supplement U+E0100 to U+E01EF", "a\U000E0100b\U000E0101c\U000E0150d\U000E01EEe\U000E01EFf",
		"abcdef", "abcdef"},
	{"a variation selector after an emoji and a CJK ideograph", "❤️ 葛\U000E0100!", "❤ 葛!", "❤ 葛!"},
	{"every Hangul filler and variation selector at both ends", "ᅟ️\U000E01EFxㅤ\U000E0100︀ﾠᅠ",
		"x", "x"},
	{"hidden fillers in a forged line", "nameㅤ\n️- id: forged", "name\n- id: forged", "name - id: forged"},
	{"the visible neighbours of each range survive", "ᅞᅡㅣㅥﾟﾡ︐❣",
		"ᅞᅡㅣㅥﾟﾡ︐❣", "ᅞᅡㅣㅥﾟﾡ︐❣"},
}

// hiddenFiller is what #163 adds to what neither client may pass on: the
// Hangul fillers and both blocks of variation selectors.
func hiddenFiller(r rune) bool {
	switch {
	case r == 0x115f, r == 0x1160, r == 0x3164, r == 0xffa0:
		return true
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef:
		return true
	}
	return false
}

// Every rune of both ranges is dropped, one at a time, with a visible
// character on each side that survives.
func TestCleanDropsEveryHangulFillerAndVariationSelector(t *testing.T) {
	var hidden []rune
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if hiddenFiller(r) {
			hidden = append(hidden, r)
		}
	}
	if len(hidden) != 4+16+240 {
		t.Fatalf("hiddenFiller covers %d runes, want %d", len(hidden), 4+16+240)
	}
	for name, f := range map[string]func(string) string{"CleanText": contract.CleanText, "CleanLine": contract.CleanLine} {
		for _, r := range hidden {
			in := "x" + string(r) + "y"
			if got := f(in); got != "xy" {
				t.Errorf("%s(%q) = %q, want %q", name, in, got, "xy")
			}
		}
	}
}

func FuzzCleanDropsHangulFillersAndVariationSelectors(f *testing.F) {
	for _, c := range cleanCases {
		f.Add(c.in)
	}
	f.Fuzz(func(t *testing.T, in string) {
		for name, clean := range map[string]func(string) string{"CleanText": contract.CleanText, "CleanLine": contract.CleanLine} {
			if got := clean(in); strings.ContainsFunc(got, hiddenFiller) {
				t.Fatalf("%s(%q) = %q keeps a Hangul filler or variation selector", name, in, got)
			}
		}
	})
}

func TestCleanTextKeepsLayoutAndDropsControlAndFormatCharacters(t *testing.T) {
	for _, c := range cleanCases {
		t.Run(c.name, func(t *testing.T) {
			if got := contract.CleanText(c.in); got != c.text {
				t.Errorf("CleanText(%q) = %q, want %q", c.in, got, c.text)
			}
		})
	}
}

func TestCleanLineTurnsLineBreaksIntoSpacesAndDropsControlAndFormatCharacters(t *testing.T) {
	for _, c := range cleanCases {
		t.Run(c.name, func(t *testing.T) {
			if got := contract.CleanLine(c.in); got != c.line {
				t.Errorf("CleanLine(%q) = %q, want %q", c.in, got, c.line)
			}
		})
	}
}

// Invalid UTF-8 never reaches the output as raw bytes: a lone 0x9b byte is a
// CSI to a terminal that reads 8-bit controls.
func TestCleanOutputIsValidUTF8(t *testing.T) {
	for _, in := range []string{"a\x9b31mb", "a\xffb", "\xc0\xaf", "a\xe2\x80b"} {
		for name, f := range map[string]func(string) string{"CleanText": contract.CleanText, "CleanLine": contract.CleanLine} {
			got := f(in)
			if !utf8.ValidString(got) {
				t.Errorf("%s(%q) = %q, not valid UTF-8", name, in, got)
			}
			if strings.ContainsRune(got, 0x9b) {
				t.Errorf("%s(%q) = %q carries a C1 CSI", name, in, got)
			}
		}
	}
}

func TestDecodeAPIErrorCleansMessageAndHint(t *testing.T) {
	body := []byte(`{"error":{"code":"not_found","message":"no such\narticle\u202e\u200b\u001b[2J","details":{"hint":"Search\tfor it\ufeff\u0085\nHint: run rm"}}}`)
	want := contract.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "no such article[2J", Hint: "Search for it Hint: run rm"}
	if got := contract.DecodeAPIError(http.StatusNotFound, nil, body); got != want {
		t.Errorf("DecodeAPIError = %#v, want %#v", got, want)
	}
}

func TestDecodeAPIErrorWithoutAHint(t *testing.T) {
	body := []byte(`{"error":{"code":"x","message":"boom","details":{}}}`)
	want := contract.APIError{Status: http.StatusInternalServerError, Code: "x", Message: "boom"}
	if got := contract.DecodeAPIError(http.StatusInternalServerError, nil, body); got != want {
		t.Errorf("DecodeAPIError = %#v, want %#v", got, want)
	}
}

// A body that is not an envelope, or an envelope without a message, yields
// only the status: nothing of the body is passed on.
func TestDecodeAPIErrorWithoutAnEnvelopeKeepsOnlyTheStatus(t *testing.T) {
	for name, body := range map[string]string{
		"html":          "<html>http://10.0.0.1/internal</html>",
		"empty":         "",
		"other json":    `{"message":"http://10.0.0.1/internal","hint":"/etc/passwd"}`,
		"empty message": `{"error":{"code":"x","message":"","details":{"hint":"http://10.0.0.1/internal"}}}`,
		"wrong types":   `{"error":{"message":42,"details":{"hint":["x"]}}}`,
		"truncated":     `{"error":{"message":"http://10.0.0.1/inter`,
	} {
		t.Run(name, func(t *testing.T) {
			want := contract.APIError{Status: http.StatusBadGateway}
			if got := contract.DecodeAPIError(http.StatusBadGateway, nil, []byte(body)); got != want {
				t.Errorf("DecodeAPIError(%q) = %#v, want %#v", body, got, want)
			}
		})
	}
}

func FuzzCleanText(f *testing.F) {
	for _, c := range cleanCases {
		f.Add(c.in)
	}
	f.Add("a\x9b31mb\xff")
	f.Fuzz(func(t *testing.T, in string) {
		got := contract.CleanText(in)
		if !utf8.ValidString(got) {
			t.Fatalf("CleanText(%q) = %q, not valid UTF-8", in, got)
		}
		for _, r := range got {
			if unsafeRune(r) {
				t.Fatalf("CleanText(%q) = %q keeps %U", in, got, r)
			}
		}
		if again := contract.CleanText(got); again != got {
			t.Fatalf("CleanText is not idempotent: %q then %q", got, again)
		}
	})
}

func FuzzCleanLine(f *testing.F) {
	for _, c := range cleanCases {
		f.Add(c.in)
	}
	f.Add("a\x9b31mb\xff")
	f.Fuzz(func(t *testing.T, in string) {
		got := contract.CleanLine(in)
		if !utf8.ValidString(got) {
			t.Fatalf("CleanLine(%q) = %q, not valid UTF-8", in, got)
		}
		for _, r := range got {
			if unsafeRune(r) || r == '\n' || r == '\t' || r == 0x2028 || r == 0x2029 {
				t.Fatalf("CleanLine(%q) = %q keeps %U", in, got, r)
			}
		}
		if again := contract.CleanLine(got); again != got {
			t.Fatalf("CleanLine is not idempotent: %q then %q", got, again)
		}
	})
}
