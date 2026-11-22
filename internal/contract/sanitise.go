package contract

import (
	"encoding/json"
	"net/http"
	"strconv"
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
// CleanText keeps newline and tab, which are C0 controls.
func UnsafeRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.Is(unicode.Cf, r)
}

// CleanText drops every unsafe rune other than newline and tab, keeping the
// line and paragraph separators U+2028 and U+2029. It is for text that may
// span lines, such as an article's content. Invalid UTF-8 becomes U+FFFD, so
// the result is always valid UTF-8 and no stray byte reaches a terminal as an
// 8-bit control.
func CleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && UnsafeRune(r) {
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
		case UnsafeRune(r):
			return -1
		}
		return r
	}, s)
}

// APIError is what a client may pass on from an error response: the status,
// the envelope's message and hint cleaned with CleanLine, and the wait of a
// valid Retry-After header. Each client words its own message from it.
type APIError struct {
	Status  int
	Message string
	Hint    string
	// RetryAfter is the wait the server asked for, in whole seconds. It is 0
	// when the header is absent, malformed or 0, so no client claims a wait
	// the server did not send.
	RetryAfter int
}

// DecodeAPIError reads an error response. A body that is not an error
// envelope, or an envelope without a message, yields only the status and
// RetryAfter: nothing else of the body is passed on.
func DecodeAPIError(status int, header http.Header, body []byte) APIError {
	e := APIError{Status: status, RetryAfter: retryAfter(header)}
	var env ErrorEnvelope
	if json.Unmarshal(body, &env) != nil {
		return e
	}
	if e.Message = CleanLine(env.Error.Message); e.Message != "" {
		e.Hint = CleanLine(env.Error.Details.Hint)
	}
	return e
}

// retryAfter reads Retry-After as delay-seconds (RFC 9110, 10.2.3): one or
// more ASCII digits and nothing else. Any other value, or one too large for
// an int, is 0. The HTTP-date form is not read: the API never sends it.
func retryAfter(h http.Header) int {
	v := h.Get("Retry-After")
	if v == "" || strings.Trim(v, "0123456789") != "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}
