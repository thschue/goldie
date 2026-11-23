package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/thschue/goldie/internal/contract"
)

// SessionPath is the session resource, the one place a browser signs in and
// out (ADR 0027).
const SessionPath = "/api/v1/session"

const (
	// sessionTTL is how long a session lives without use: each authenticated
	// GET or HEAD re-issues the cookie for another sessionTTL.
	sessionTTL = 30 * time.Minute
	// sessionMaxAge is the absolute cap: re-issuing never moves a session's
	// end past this long after sign-in.
	sessionMaxAge = 7 * 24 * time.Hour
	// MinSessionKeyLength is the shortest signing key accepted, in bytes.
	MinSessionKeyLength = 32

	sessionCookie = "goldie_session"
	hostPrefix    = "__Host-"
	maxSignInBody = 4 << 10
)

// Sessions signs and checks session cookies with one key. The cookie holds the
// token's name and the issue and expiry times under an HMAC, never the token
// or its role, so there is no session store (ADR 0027).
type Sessions struct{ key []byte }

// WithSessions returns the token set with browser sessions on, signed with key.
// An empty key is a random one for this process. A key shorter than
// MinSessionKeyLength is an error that never contains the key.
func (t *Tokens) WithSessions(key string) (*Tokens, error) {
	k := []byte(key)
	switch {
	case key == "":
		k = make([]byte, MinSessionKeyLength)
		if _, err := rand.Read(k); err != nil {
			return nil, fmt.Errorf("session key: %w", err)
		}
	case len(k) < MinSessionKeyLength:
		return nil, fmt.Errorf("must be at least %d bytes: generate one with `openssl rand -hex 32`", MinSessionKeyLength)
	}
	out := &Tokens{sessions: &Sessions{key: k}}
	if t != nil {
		out.entries = t.entries
	}
	return out, nil
}

func (s *Sessions) mac(name string, issued, expires int64) []byte {
	h := hmac.New(sha256.New, s.key)
	_, _ = fmt.Fprintf(h, "goldie-session\n%s\n%d\n%d", name, issued, expires) // a hash.Hash never errors
	return h.Sum(nil)
}

// value is the cookie value for a session of the token name, started at issued
// and valid until now plus sessionTTL, or the absolute cap if that comes first.
func (s *Sessions) value(name string, issued, now time.Time) (string, time.Time) {
	expires := now.Add(sessionTTL)
	if end := issued.Add(sessionMaxAge); expires.After(end) {
		expires = end
	}
	i, e := issued.Unix(), expires.Unix()
	return name + ":" + strconv.FormatInt(i, 10) + ":" + strconv.FormatInt(e, 10) + ":" +
		base64.RawURLEncoding.EncodeToString(s.mac(name, i, e)), expires
}

// open checks a cookie value: well formed, signed with this key, not expired
// and within the absolute cap. It returns the token name and the sign-in time.
func (s *Sessions) open(value string, now time.Time) (name string, issued time.Time, ok bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 4 {
		return "", time.Time{}, false
	}
	i, err1 := strconv.ParseInt(parts[1], 10, 64)
	e, err2 := strconv.ParseInt(parts[2], 10, 64)
	sum, err3 := base64.RawURLEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || err3 != nil || !hmac.Equal(sum, s.mac(parts[0], i, e)) {
		return "", time.Time{}, false
	}
	issued = time.Unix(i, 0)
	if e-i > int64(sessionMaxAge/time.Second) || !now.Before(time.Unix(e, 0)) {
		return "", time.Time{}, false
	}
	return parts[0], issued, true
}

// loopbackPlain reports a plain-http request to a loopback host, the dev
// listener, where a browser would drop a Secure or __Host- cookie. Anything
// else, a proxy-terminated request included, keeps both.
func loopbackPlain(r *http.Request) bool {
	if r.TLS != nil {
		return false
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = strings.Trim(r.Host, "[]")
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func cookieName(r *http.Request) string {
	if loopbackPlain(r) {
		return sessionCookie
	}
	return hostPrefix + sessionCookie
}

// setCookie sends the session cookie; a zero expires clears it.
func setCookie(w http.ResponseWriter, r *http.Request, value string, now, expires time.Time) {
	maxAge := -1
	if !expires.IsZero() {
		maxAge = max(1, int(expires.Sub(now)/time.Second))
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName(r), Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: !loopbackPlain(r),
	})
}

// serveCookie authenticates a GET or HEAD with a valid session cookie: the
// token is looked up by name, so its Role is the configuration's now and a
// removed token ends the session. It re-issues the cookie. handled is false
// when the request carries no cookie.
func (t *Tokens) serveCookie(w http.ResponseWriter, r *http.Request, minRole Role, next http.Handler) (handled bool) {
	c, err := r.Cookie(cookieName(r))
	if err != nil {
		return false
	}
	now := time.Now()
	name, issued, ok := t.sessions.open(c.Value, now)
	var e tokenEntry
	if ok {
		e, ok = t.byName(name)
	}
	if !ok {
		return false // not a session: the usual 401
	}
	switch {
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		WriteError(w, http.StatusForbidden, "forbidden", "a session cookie only authenticates GET and HEAD requests",
			"Send the API token as a bearer token for this request.")
	case e.role < minRole:
		WriteError(w, http.StatusForbidden, "forbidden", "the API token's role does not allow this operation",
			"Use a token with the operator role.")
	default:
		v, expires := t.sessions.value(name, issued, now)
		setCookie(w, r, v, now, expires)
		ctx := context.WithValue(r.Context(), tokenNameKey{}, e.name)
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, tokenRoleKey{}, e.role)))
	}
	return true
}

func (t *Tokens) byName(name string) (tokenEntry, bool) {
	for _, e := range t.entries {
		if e.name == name {
			return e, true
		}
	}
	return tokenEntry{}, false
}

func (r Role) String() string {
	if r == RoleOperator {
		return "operator"
	}
	return "reader"
}

// NoStore forbids caching the response of next.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// SessionEntry guards the session operations that take no bearer token
// (sign-in and sign-out): no caching, a small body, and a cross-origin
// request is refused (http.CrossOriginProtection).
func SessionEntry(next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	return NoStore(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cop.Check(r); err != nil {
			WriteError(w, http.StatusForbidden, "forbidden", "cross-origin requests to the session are refused",
				"Send the request from the same origin.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxSignInBody)
		next.ServeHTTP(w, r)
	}))
}

// WithSessions is the option that serves the session operations over tokens,
// which must have been through Tokens.WithSessions.
func WithSessions(tokens *Tokens) ServerOption { return func(s *Server) { s.tokens = tokens } }

var unauthorizedSession = envelope("unauthorized", "missing or invalid API token",
	"Send the API token in the body: {\"token\": \"<token>\"}.")

// PostSession answers POST /api/v1/session; the cookie is set by sessionCookies.
func (s *Server) PostSession(_ context.Context, req contract.PostSessionRequestObject) (contract.PostSessionResponseObject, error) {
	if s.tokens == nil || s.tokens.sessions == nil || req.Body == nil {
		return contract.PostSession401JSONResponse(unauthorizedSession), nil
	}
	match, found := s.tokens.find(req.Body.Token)
	if found != 1 {
		return contract.PostSession401JSONResponse(unauthorizedSession), nil
	}
	e := s.tokens.entries[match]
	return contract.PostSession200JSONResponse{Name: e.name, Role: contract.SessionRole(e.role.String())}, nil
}

// GetSession answers GET /api/v1/session for a request the guard let through.
func (s *Server) GetSession(ctx context.Context, _ contract.GetSessionRequestObject) (contract.GetSessionResponseObject, error) {
	if s.provider() == nil {
		return contract.GetSession503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	name, ok := TokenName(ctx)
	role, ok2 := TokenRole(ctx)
	if !ok || !ok2 {
		return contract.GetSession401JSONResponse(unauthorizedSession), nil
	}
	return contract.GetSession200JSONResponse{Name: name, Role: contract.SessionRole(role.String())}, nil
}

// DeleteSession answers DELETE /api/v1/session; the cookie is cleared by
// sessionCookies.
func (s *Server) DeleteSession(context.Context, contract.DeleteSessionRequestObject) (contract.DeleteSessionResponseObject, error) {
	return contract.DeleteSession204Response{}, nil
}

// sessionCookies is the strict middleware that sets the cookie a sign-in
// earned and clears it on sign-out.
func (s *Server) sessionCookies(next contract.StrictHandlerFunc, op string) contract.StrictHandlerFunc {
	if op != "PostSession" && op != "DeleteSession" {
		return next
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		resp, err := next(ctx, w, r, req)
		now := time.Now()
		switch resp := resp.(type) {
		case contract.PostSession200JSONResponse:
			v, expires := s.tokens.sessions.value(resp.Name, now, now)
			setCookie(w, r, v, now, expires)
		case contract.DeleteSession204Response:
			setCookie(w, r, "", now, time.Time{})
		}
		return resp, err
	}
}
