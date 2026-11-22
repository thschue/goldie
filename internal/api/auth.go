package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// MinTokenLength is the shortest API token CheckToken accepts, in bytes. It
// keeps a guessable token out of production; `openssl rand -hex 32` gives 64.
const MinTokenLength = 32

// CheckToken validates a configured API token. It must be printable ASCII
// without spaces, so that it survives an Authorization header unchanged, and
// at least MinTokenLength bytes long. Errors never contain the token, any
// part of it, or its length.
func CheckToken(token string) error {
	if token == "" {
		return errors.New("is not set: the API needs a token")
	}
	for i := 0; i < len(token); i++ {
		if c := token[i]; c <= ' ' || c > '~' {
			return errors.New("must be printable ASCII without spaces")
		}
	}
	if len(token) < MinTokenLength {
		return fmt.Errorf("must be at least %d bytes: generate one with `openssl rand -hex 32`", MinTokenLength)
	}
	return nil
}

// RequireToken lets a request through to next only when it carries
// "Authorization: Bearer <token>". Anything else is a 401 in the error
// envelope. The comparison takes the same time for every presented token,
// whatever its length or content, and the presented value is never logged or
// echoed. A token that fails CheckToken, empty or too short included, rejects
// every request, even one that presents it.
func RequireToken(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	valid := CheckToken(token) == nil
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := bearerToken(r.Header.Get("Authorization"))
		got := sha256.Sum256([]byte(presented))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 || !ok || !valid {
			w.Header().Set("WWW-Authenticate", `Bearer realm="goldie"`)
			WriteError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API token",
				"Send the API token in the Authorization header: Bearer <token>.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts the credentials of a Bearer Authorization header. The
// scheme is case-insensitive (RFC 9110, section 11.1).
func bearerToken(header string) (string, bool) {
	scheme, cred, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || cred == "" {
		return "", false
	}
	return cred, true
}
