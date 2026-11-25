package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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

// RequireToken guards next with one operator token, the GOLDIE_API_TOKEN
// shorthand (see Tokens.Require). A token that fails CheckToken, empty or too
// short included, rejects every request, even one that presents it.
func RequireToken(token string, next http.Handler) http.Handler {
	tokens, err := OperatorToken(token)
	if err != nil {
		tokens = &Tokens{}
	}
	return tokens.Require(RoleReader, next)
}

// Role is what a token may do. Readers search, look up, list and fetch
// bundles; operators can also trigger syncs and read activity. Each Role
// includes the ones below it.
type Role int

// The Roles, lowest first.
const (
	RoleReader Role = iota + 1
	RoleOperator
)

func (r Role) String() string {
	if r == RoleOperator {
		return "operator"
	}
	return "reader"
}

var roleNames = map[string]Role{"reader": RoleReader, "operator": RoleOperator}

// ShorthandName is the name of the GOLDIE_API_TOKEN shorthand's token.
const ShorthandName = "api-token"

// maxNameLength bounds a token name, which goes into logs and telemetry.
const maxNameLength = 64

// hashPrefix marks a hashed entry: the hex SHA-256 digest of the token.
const hashPrefix = "sha256:"

// Tokens is the set of configured API tokens. It holds each token's name,
// Role and SHA-256 digest, never a secret. The zero value rejects every
// request.
type Tokens struct {
	entries  []tokenEntry
	sessions *Sessions // see WithSessions, nil: no session cookies
}

type tokenEntry struct {
	name   string
	role   Role
	digest [sha256.Size]byte
}

// OperatorToken is the set of one operator token named ShorthandName. Its
// errors are CheckToken's.
func OperatorToken(secret string) (*Tokens, error) {
	if err := CheckToken(secret); err != nil {
		return nil, err
	}
	return &Tokens{entries: []tokenEntry{{ShorthandName, RoleOperator, sha256.Sum256([]byte(secret))}}}, nil
}

// ParseTokens reads token lines: name:role:token, or name:role:sha256:<hex>
// with the hex SHA-256 digest of the token. Blank lines and lines starting
// with # are skipped, and a trailing carriage return is dropped. A name is 1
// to 64 letters, digits, '.', '_' or '-' and is used once; a Role is reader
// or operator; a plaintext token passes CheckToken; no token appears twice.
// Errors give the line number but never the line, any part of it, or the
// token's length.
func ParseTokens(lines string) (*Tokens, error) {
	t := &Tokens{}
	names := map[string]int{}
	digests := map[[sha256.Size]byte]int{}
	for i, line := range strings.Split(lines, "\n") {
		n := i + 1
		line = strings.TrimSuffix(line, "\r")
		if line == "" || line[0] == '#' {
			continue
		}
		e, err := parseTokenLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if prev, dup := names[e.name]; dup {
			return nil, fmt.Errorf("line %d: the name is already used on line %d", n, prev)
		}
		if prev, dup := digests[e.digest]; dup {
			return nil, fmt.Errorf("line %d: the same token as line %d: give each client its own", n, prev)
		}
		names[e.name], digests[e.digest] = n, n
		t.entries = append(t.entries, e)
	}
	if len(t.entries) == 0 {
		return nil, errors.New("has no token lines")
	}
	return t, nil
}

// parseTokenLine reads one name:role:token or name:role:sha256:<hex> line.
func parseTokenLine(line string) (tokenEntry, error) {
	name, rest, ok1 := strings.Cut(line, ":")
	role, secret, ok2 := strings.Cut(rest, ":")
	if !ok1 || !ok2 {
		return tokenEntry{}, errors.New("must be name:role:token or name:role:sha256:<hex>")
	}
	if !validName(name) {
		return tokenEntry{}, fmt.Errorf("the name must be 1 to %d letters, digits, '.', '_' or '-'", maxNameLength)
	}
	r, ok := roleNames[role]
	if !ok {
		return tokenEntry{}, errors.New("the role must be reader or operator")
	}
	e := tokenEntry{name: name, role: r}
	if hexDigest, hashed := strings.CutPrefix(secret, hashPrefix); hashed {
		d, err := hex.DecodeString(hexDigest)
		if err != nil || len(d) != sha256.Size {
			return tokenEntry{}, errors.New("a plaintext token must not start with sha256:, which marks a hash, and a hash is sha256: followed by 64 hex digits")
		}
		copy(e.digest[:], d)
		return e, nil
	}
	if secret == "" {
		return tokenEntry{}, errors.New("the token is empty")
	}
	if err := CheckToken(secret); err != nil {
		return tokenEntry{}, fmt.Errorf("the token %w", err)
	}
	e.digest = sha256.Sum256([]byte(secret))
	return e, nil
}

// validName reports whether name is 1 to maxNameLength letters, digits, '.',
// '_' or '-', so that it is safe in logs and telemetry.
func validName(name string) bool {
	if name == "" || len(name) > maxNameLength {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if ('a' > c || c > 'z') && ('A' > c || c > 'Z') && ('0' > c || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

type (
	tokenNameKey struct{}
	tokenRoleKey struct{}
	sessionKey   struct{} // set when a session cookie authenticated the request
	limitKeyKey  struct{} // what the rate limiter counts, when it is not the token's name
)

// TokenName is the name of the token that authenticated the request, never
// its secret. A Require guard sets it on every request it lets through.
func TokenName(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(tokenNameKey{}).(string)
	return name, ok
}

// TokenRole is the Role of the token that authenticated the request. A
// Require guard sets it on every request it lets through; without one there
// is no Role, and a caller checking for a Role must refuse. A Role rule for a
// whole operation is `x-goldie-role` in the spec (contract.OperatorPaths); TokenRole is
// for a rule on one parameter value, such as drafts=true.
func TokenRole(ctx context.Context) (Role, bool) {
	role, ok := ctx.Value(tokenRoleKey{}).(Role)
	return role, ok
}

// Require lets a request through to next only when it carries
// "Authorization: Bearer <token>" with a configured token whose Role is at
// least minRole, and puts the token's name and Role in the request context
// (TokenName, TokenRole). A missing or unknown token is a 401 and too low a Role a 403,
// both in the error envelope. The presented token is hashed and compared with
// every configured digest in constant time, whatever its length or content,
// and it is never logged or echoed.
func (t *Tokens) Require(minRole Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A bearer token beats a cookie; a cookie counts only without one.
		if t != nil && t.sessions != nil && r.Header.Get("Authorization") == "" && t.serveCookie(w, r, minRole, next) {
			return
		}
		presented, ok := bearerToken(r.Header.Get("Authorization"))
		match, found := t.find(presented)
		if found != 1 || !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="goldie"`)
			WriteError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API token",
				"Send the API token in the Authorization header: Bearer <token>.")
			return
		}
		e := t.entries[match]
		if e.role < minRole {
			WriteError(w, http.StatusForbidden, "forbidden", "the API token's role does not allow this operation",
				"Use a token with the operator role.")
			return
		}
		ctx := context.WithValue(r.Context(), tokenNameKey{}, e.name)
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, tokenRoleKey{}, e.role)))
	})
}

// find hashes presented and compares it with every configured digest in
// constant time, whatever its length or content. found is 1 on a match, with
// the entry's index in match.
func (t *Tokens) find(presented string) (match, found int) {
	if t == nil {
		return 0, 0
	}
	got := sha256.Sum256([]byte(presented))
	for i := range t.entries {
		eq := subtle.ConstantTimeCompare(got[:], t.entries[i].digest[:])
		match = subtle.ConstantTimeSelect(eq, i, match)
		found |= eq
	}
	return match, found
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
