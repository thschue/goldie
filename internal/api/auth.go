package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

// CheckToken validates a configured API token. It must be non-empty and
// printable ASCII without spaces, so that it survives an Authorization
// header unchanged. Errors never contain the token.
func CheckToken(token string) error {
	if token == "" {
		return errors.New("is not set: the API needs a token")
	}
	for i := 0; i < len(token); i++ {
		if c := token[i]; c <= ' ' || c > '~' {
			return errors.New("must be printable ASCII without spaces")
		}
	}
	return nil
}

// RequireToken lets a request through to next only when it carries
// "Authorization: Bearer <token>". Anything else is a 401 in the error
// envelope. The comparison takes the same time for every presented token,
// whatever its length or content, and the presented value is never logged or
// echoed. An empty token rejects every request.
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
