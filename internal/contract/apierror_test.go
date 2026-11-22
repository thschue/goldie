package contract_test

import (
	"net/http"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// A message that is empty once cleaned counts as no message: the client
// words the error from the status, not from an empty string before it.
func TestDecodeAPIErrorWithAMessageOfOnlyInvisibleRunesKeepsOnlyTheStatus(t *testing.T) {
	body := []byte(`{"error":{"code":"x","message":"\u200b\u202e\u001b","details":{"hint":"http://10.0.0.1/internal"}}}`)
	want := contract.APIError{Status: http.StatusNotFound}
	if got := contract.DecodeAPIError(http.StatusNotFound, nil, body); got != want {
		t.Errorf("DecodeAPIError = %#v, want %#v", got, want)
	}
}

// Acceptance tests for #174: DecodeAPIError exposes the Retry-After value as
// APIError.RetryAfter, in whole seconds, and 0 when the header is absent or
// malformed. The API it pins:
//
//	type APIError struct { Status int; Message, Hint string; RetryAfter int }
//	func DecodeAPIError(status int, header http.Header, body []byte) APIError
//
// A valid value is delay-seconds (RFC 9110, 10.2.3): one or more digits.
// Anything else is malformed, so no client tells a person or an agent to wait
// a number of seconds the server did not send.

// rateLimited is the 429 envelope the API writes.
const rateLimited = `{"error":{"code":"rate_limited","message":"too many requests for this API token",` +
	`"details":{"hint":"Wait the number of seconds in the Retry-After header, then retry."}}}`

func TestDecodeAPIErrorReadsRetryAfterInWholeSeconds(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header // nil: no header at all
		want   int
	}{
		{"no headers", nil, 0},
		{"absent", http.Header{"Content-Type": {"application/json"}}, 0},
		{"empty", http.Header{"Retry-After": {""}}, 0},
		{"words", http.Header{"Retry-After": {"soon"}}, 0},
		{"a unit", http.Header{"Retry-After": {"5s"}}, 0},
		{"a fraction", http.Header{"Retry-After": {"1.5"}}, 0},
		{"an exponent", http.Header{"Retry-After": {"1e3"}}, 0},
		{"negative", http.Header{"Retry-After": {"-5"}}, 0},
		{"a plus sign", http.Header{"Retry-After": {"+5"}}, 0},
		{"inner space", http.Header{"Retry-After": {"1 0"}}, 0},
		{"overflows an int64", http.Header{"Retry-After": {"99999999999999999999"}}, 0},
		{"hex", http.Header{"Retry-After": {"0x10"}}, 0},
		{"zero", http.Header{"Retry-After": {"0"}}, 0},
		{"one second", http.Header{"Retry-After": {"1"}}, 1},
		{"a minute", http.Header{"Retry-After": {"60"}}, 60},
		{"leading zeros", http.Header{"Retry-After": {"007"}}, 7},
		{"set through Set", func() http.Header { h := http.Header{}; h.Set("retry-after", "42"); return h }(), 42},
		// #182 (from #200): a wait above an hour is unknown, so clients say "Wait a little".
		{"an hour", http.Header{"Retry-After": {"3600"}}, 3600},
		{"an hour and a second", http.Header{"Retry-After": {"3601"}}, 0},
		{"a day", http.Header{"Retry-After": {"86400"}}, 0},
		{"above an hour with leading zeros", http.Header{"Retry-After": {"0003601"}}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := contract.DecodeAPIError(http.StatusTooManyRequests, c.header, []byte(rateLimited))
			want := contract.APIError{
				Status:     http.StatusTooManyRequests,
				Message:    "too many requests for this API token",
				Hint:       "Wait the number of seconds in the Retry-After header, then retry.",
				RetryAfter: c.want,
			}
			if got != want {
				t.Errorf("DecodeAPIError(429, Retry-After %q) = %#v, want %#v", c.header.Get("Retry-After"), got, want)
			}
		})
	}
}

// Retry-After is a header, not part of the body: it is read whatever the
// body is, and on any status that may carry it, while nothing of a body that
// is not an envelope is passed on.
func TestDecodeAPIErrorReadsRetryAfterWhateverTheBody(t *testing.T) {
	h := http.Header{"Retry-After": {"30"}}
	for name, body := range map[string]string{
		"html":     "<html>http://10.0.0.1/internal</html>",
		"empty":    "",
		"no error": `{"message":"http://10.0.0.1/internal"}`,
	} {
		t.Run(name, func(t *testing.T) {
			want := contract.APIError{Status: http.StatusTooManyRequests, RetryAfter: 30}
			if got := contract.DecodeAPIError(http.StatusTooManyRequests, h, []byte(body)); got != want {
				t.Errorf("DecodeAPIError(429, %q) = %#v, want %#v", body, got, want)
			}
		})
	}
	want := contract.APIError{Status: http.StatusServiceUnavailable, Message: "boom", RetryAfter: 120}
	body := []byte(`{"error":{"code":"x","message":"boom","details":{}}}`)
	if got := contract.DecodeAPIError(http.StatusServiceUnavailable, http.Header{"Retry-After": {"120"}}, body); got != want {
		t.Errorf("DecodeAPIError(503) = %#v, want %#v", got, want)
	}
}

// #163 (from #223): Wait words APIError.RetryAfter for a person or an agent,
// and is empty when there is no wait to pass on.
func TestAPIErrorWaitWordsTheWaitInWholeSeconds(t *testing.T) {
	for _, c := range []struct {
		retryAfter int
		want       string
	}{
		{0, ""},
		{1, "1 second"},
		{2, "2 seconds"},
		{3600, "3600 seconds"},
	} {
		e := contract.APIError{Status: http.StatusTooManyRequests, RetryAfter: c.retryAfter}
		if got := e.Wait(); got != c.want {
			t.Errorf("APIError{RetryAfter: %d}.Wait() = %q, want %q", c.retryAfter, got, c.want)
		}
	}
}
