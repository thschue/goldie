package contract

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// ErrNotAPIAnswer is returned by Answer for a 200 that is not an API answer:
// no JSON content type, such as a proxy's login page. It carries nothing of
// the answer.
var ErrNotAPIAnswer = errors.New("the server's answer is not an API answer")

// errNoAnswer is what Answer returns for a server it could not reach, or an
// answer it could not read. The cause is dropped: it carries the address.
var errNoAnswer = errors.New("the server could not be reached, or its answer could not be read")

// Error names the status only: the client words the message and hint itself.
func (e *APIError) Error() string {
	return fmt.Sprintf("the server answered %d %s", e.Status, http.StatusText(e.Status))
}

// Answer reads one API answer, so every client classifies a failure the same
// way and only words it. raw and err are what a call of the client
// NewAPIClient built returned; parse is the generated parser of that call and
// body picks its 200 body. Answer returns that body, or exactly one of:
// ErrRedirect, ErrTooLarge, context.Canceled or context.DeadlineExceeded,
// ErrNotAPIAnswer, an *APIError for any status other than 200, or an error
// that is none of these for a server it could not reach or an answer it could
// not read. No error carries the address, the token or the answer's body.
func Answer[R, B any](raw *http.Response, err error, parse func(*http.Response) (R, error), body func(R) *B) (*B, error) {
	if err != nil {
		return nil, callError(err)
	}
	resp, e, err := ParseOK(raw, parse)
	switch {
	case err != nil:
		return nil, callError(err)
	case e != nil:
		return nil, e
	}
	b := body(resp)
	if b == nil {
		// The generated parser fills the 200 body only for a JSON content type.
		return nil, ErrNotAPIAnswer
	}
	return b, nil
}

// callError replaces a failure to make a call or read its answer with the
// sentinel of its class, dropping the cause.
func callError(err error) error {
	for _, known := range []error{ErrRedirect, ErrTooLarge, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return known
		}
	}
	return errNoAnswer
}
