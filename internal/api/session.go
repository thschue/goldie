package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
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
	// sessionMaxAge is the absolute cap after sign-in.
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
type Sessions struct {
	key []byte
	// dev drops Secure and the __Host- prefix on plain http, for the loopback
	// dev server the composition root starts itself. It comes from the
	// configuration, never from the request: goldie terminates no TLS, so a
	// Host header or a forwarded header says nothing about the browser's scheme.
	dev bool
}

// WithSessions returns the token set with browser sessions on, signed with key.
// An empty key is a random one for this process. A key shorter than
// MinSessionKeyLength is an error that never contains the key. dev is true
// only for the loopback dev server (server --dev, local mode): its plain-http
// cookies drop Secure and __Host-, which a browser would refuse there. Every
// other server always sets both.
func (t *Tokens) WithSessions(key string, dev bool) (*Tokens, error) {
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
	out := &Tokens{sessions: &Sessions{key: k, dev: dev}}
	if t != nil {
		out.entries = t.entries
	}
	return out, nil
}

// mac signs the token's name and the digest of its configured value, so
// rotating the value under the same name ends its sessions.
func (s *Sessions) mac(e tokenEntry, issued, expires int64) []byte {
	h := hmac.New(sha256.New, s.key)
	_, _ = fmt.Fprintf(h, "goldie-session\n%s\n%x\n%d\n%d", e.name, e.digest, issued, expires) // a hash.Hash never errors
	return h.Sum(nil)
}

// value is the cookie value for a session of the token e, started at issued
// and valid until now plus sessionTTL, or the absolute cap if that comes first.
func (s *Sessions) value(e tokenEntry, issued, now time.Time) (string, time.Time) {
	expires := now.Add(sessionTTL)
	if end := issued.Add(sessionMaxAge); expires.After(end) {
		expires = end
	}
	i, x := issued.Unix(), expires.Unix()
	return e.name + ":" + strconv.FormatInt(i, 10) + ":" + strconv.FormatInt(x, 10) + ":" +
		base64.RawURLEncoding.EncodeToString(s.mac(e, i, x)), expires
}

// open checks a cookie value: well formed, naming a configured token (byName),
// signed with this key over that token's current value, not expired and
// within the absolute cap. It returns the token entry and the sign-in time.
func (s *Sessions) open(value string, now time.Time, byName func(string) (tokenEntry, bool)) (e tokenEntry, issued time.Time, ok bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 4 {
		return tokenEntry{}, time.Time{}, false
	}
	e, found := byName(parts[0])
	i, err1 := strconv.ParseInt(parts[1], 10, 64)
	x, err2 := strconv.ParseInt(parts[2], 10, 64)
	sum, err3 := base64.RawURLEncoding.DecodeString(parts[3])
	if !found || err1 != nil || err2 != nil || err3 != nil || !hmac.Equal(sum, s.mac(e, i, x)) {
		return tokenEntry{}, time.Time{}, false
	}
	if x-i > int64(sessionMaxAge/time.Second) || !now.Before(time.Unix(x, 0)) {
		return tokenEntry{}, time.Time{}, false
	}
	return e, time.Unix(i, 0), true
}

// CSRFHeader carries the CSRF token: the response of a sign-in and of a
// session GET hands it out, and a cookie-authenticated unsafe request must
// send it back.
const CSRFHeader = "X-Goldie-CSRF"

// csrf is the double-submit token of the session of e that began at issued
// (Unix seconds): an HMAC under the session key over the token's name, its
// digest and the sign-in time. It is the same for the life of the session,
// across cookie re-issues, and for no other session, key or token value.
func (s *Sessions) csrf(e tokenEntry, issued int64) string {
	h := hmac.New(sha256.New, s.key)
	_, _ = fmt.Fprintf(h, "goldie-csrf\n%s\n%x\n%d", e.name, e.digest, issued) // a hash.Hash never errors
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// csrfOK reports whether r carries the CSRF token of the session of e.
func (s *Sessions) csrfOK(r *http.Request, e tokenEntry, issued time.Time) bool {
	return hmac.Equal([]byte(r.Header.Get(CSRFHeader)), []byte(s.csrf(e, issued.Unix())))
}

var crossOrigin = http.NewCrossOriginProtection()

// crossSite reports a request whose Origin or Sec-Fetch-Site says another
// site sent it. A request with neither header (a non-browser client) passes;
// the CSRF token is what it needs. The Origin scheme is compared too when the
// request came over TLS, which http.CrossOriginProtection leaves out.
func crossSite(r *http.Request) bool {
	if crossOrigin.Check(r) != nil {
		return true
	}
	if o := r.Header.Get("Origin"); o != "" && r.TLS != nil {
		u, err := url.Parse(o)
		return err != nil || u.Scheme != "https"
	}
	return false
}

// plain reports a plain-http request to the dev server, where a browser would
// drop a Secure or __Host- cookie. Any other server keeps both.
func (s *Sessions) plain(r *http.Request) bool { return s.dev && r.TLS == nil }

func (s *Sessions) cookieName(r *http.Request) string {
	if s.plain(r) {
		return sessionCookie
	}
	return hostPrefix + sessionCookie
}

// setCookie sends the session cookie; a zero expires clears it.
func (s *Sessions) setCookie(w http.ResponseWriter, r *http.Request, value string, now, expires time.Time) {
	maxAge := -1
	if !expires.IsZero() {
		maxAge = max(1, int(expires.Sub(now)/time.Second))
	}
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(r), Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: !s.plain(r),
	})
}

// serveCookie authenticates a GET or HEAD with a valid session cookie: the
// token is looked up by name, so its Role is the configuration's now and a
// removed token ends the session. It re-issues the cookie. handled is false
// when the request carries no cookie.
func (t *Tokens) serveCookie(w http.ResponseWriter, r *http.Request, minRole Role, next http.Handler) (handled bool) {
	c, err := r.Cookie(t.sessions.cookieName(r))
	if err != nil {
		return false
	}
	now := time.Now()
	e, issued, ok := t.sessions.open(c.Value, now, t.byName)
	if !ok {
		return false // not a session: the usual 401
	}
	safe := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case !safe && crossSite(r):
		WriteError(w, http.StatusForbidden, "forbidden", "cross-origin requests with a session cookie are refused",
			"Send the request from the same origin.")
	case !safe && !t.sessions.csrfOK(r, e, issued):
		WriteError(w, http.StatusForbidden, "forbidden", "a session cookie needs a valid CSRF token for this request",
			"Send the token from GET /api/v1/session in the "+CSRFHeader+" header, or send the API token as a bearer token.")
	case e.role < minRole:
		WriteError(w, http.StatusForbidden, "forbidden", "the API token's role does not allow this operation",
			"Use a token with the operator role.")
	default:
		v, expires := t.sessions.value(e, issued, now)
		w.Header().Add("Vary", "Cookie")
		w.Header().Set("Cache-Control", "private, no-store")
		t.sessions.setCookie(w, r, v, now, expires)
		if r.URL.Path == SessionPath {
			w.Header().Set(CSRFHeader, t.sessions.csrf(e, issued.Unix()))
		}
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

// Sign-in and sign-out take no token, so neither reaches the per-token rate
// limiter or the version gate (ADR 0027); failed sign-ins are capped per
// remote address by SignInLimiter.
var (
	unauthorizedSignIn = envelope("unauthorized", "missing or invalid API token",
		"Send the API token in the body: {\"token\": \"<token>\"}.")
	unauthorizedSession = envelope("unauthorized", "no valid session",
		"Sign in with POST /api/v1/session and the token in the body, or send the token in the Authorization header.")
)

func (s *Server) PostSession(ctx context.Context, req contract.PostSessionRequestObject) (contract.PostSessionResponseObject, error) {
	if s.tokens == nil || s.tokens.sessions == nil || req.Body == nil {
		ReportSignInFailure(ctx)
		return contract.PostSession401JSONResponse(unauthorizedSignIn), nil
	}
	match, found := s.tokens.find(req.Body.Token)
	if found != 1 {
		ReportSignInFailure(ctx)
		return contract.PostSession401JSONResponse(unauthorizedSignIn), nil
	}
	e := s.tokens.entries[match]
	return contract.PostSession200JSONResponse{Name: e.name, Role: contract.SessionRole(e.role.String())}, nil
}

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
			e, _ := s.tokens.byName(resp.Name) // the entry the sign-in just matched
			v, expires := s.tokens.sessions.value(e, now, now)
			s.tokens.sessions.setCookie(w, r, v, now, expires)
			w.Header().Set(CSRFHeader, s.tokens.sessions.csrf(e, now.Unix()))
		case contract.DeleteSession204Response:
			if s.tokens != nil && s.tokens.sessions != nil {
				s.tokens.sessions.setCookie(w, r, "", now, time.Time{})
			}
		}
		return resp, err
	}
}
