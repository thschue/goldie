package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// APIError is what a client may pass on from an error response: the status,
// the envelope's message and hint cleaned with CleanLine, and the wait of a
// valid Retry-After header. Each client words its own message from it.
type APIError struct {
	Status  int
	Code    string // the envelope's error code, for clients to branch on; never shown
	Message string
	Hint    string
	// RetryAfter is the wait the server asked for, in whole seconds. It is 0
	// when the header is absent, malformed, 0 or above an hour, so no client
	// claims a wait the server did not send, or passes on an absurd one.
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
		e.Code = CleanLine(env.Error.Code)
		e.Hint = CleanLine(env.Error.Details.Hint)
	}
	return e
}

// Wait words the wait of e in whole seconds, such as "30 seconds", or is
// empty when the server gave no wait a client should pass on.
func (e APIError) Wait() string {
	switch e.RetryAfter {
	case 0:
		return ""
	case 1:
		return "1 second"
	}
	return fmt.Sprintf("%d seconds", e.RetryAfter)
}

// retryAfter reads Retry-After as delay-seconds (RFC 9110, 10.2.3): one or
// more ASCII digits and nothing else. Any other value, or a wait above
// maxRetryAfter, is 0: a client says "wait a little" rather than pass on an
// absurd number. The HTTP-date form is not read: the API never sends it.
func retryAfter(h http.Header) int {
	v := h.Get("Retry-After")
	if v == "" || strings.Trim(v, "0123456789") != "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n > maxRetryAfter {
		return 0
	}
	return n
}

// maxRetryAfter is the longest wait, in seconds, a client passes on: an hour.
const maxRetryAfter = 3600
