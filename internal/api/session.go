package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
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
	// request with the cookie re-issues it for another sessionTTL.
	sessionTTL = 30 * time.Minute
	// sessionMaxAge is the absolute cap after sign-in with a token.
	sessionMaxAge = 7 * 24 * time.Hour
	// githubMaxAge is the absolute cap after sign-in with GitHub: the cookie
	// holds a Role GitHub granted, so this is how long a removed membership
	// can still be served (ADR 0032).
	githubMaxAge = time.Hour
	// MinSessionKeyLength is the shortest signing key accepted, in bytes.
	MinSessionKeyLength = 32

	sessionCookie = "goldie_session"
	hostPrefix    = "__Host-"
	maxSignInBody = 4 << 10
)

// Sessions signs and checks session cookies with one key. A token session's
// cookie holds the token's name and the issue and expiry times under an HMAC,
// never the token or its role; a GitHub session's holds the login, the Role
// and the times (ADR 0032). There is no session store (ADR 0027).
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

// subject is who a session cookie signs in: a configured token, or a GitHub
// login with the Role GitHub granted (login is set, token is not).
type subject struct {
	token tokenEntry
	login string
	role  Role
}

func tokenSubject(e tokenEntry) subject { return subject{token: e, role: e.role} }

// label is the name the principal carries in logs, metrics and the session
// resource: the token's name, or github:<role>, never a login.
func (u subject) label() string {
	if u.login == "" {
		return u.token.name
	}
	return "github:" + u.role.String()
}

// limitKey is what the per-token rate limiter counts: the token's name, or
// the login, so one user's use does not spend another's of the same Role. A
// token name has no ':', so the two never collide.
func (u subject) limitKey() string {
	if u.login == "" {
		return u.token.name
	}
	return "github:" + u.login
}

func (u subject) maxAge() time.Duration {
	if u.login == "" {
		return sessionMaxAge
	}
	return githubMaxAge
}

// mac signs the subject and the times. A token session signs the token's name
// and the digest of its configured value, so rotating the value under the same
// name ends its sessions. A GitHub session signs the login and Role under its
// own domain string, so neither form can stand in for the other.
func (s *Sessions) mac(u subject, issued, expires int64) []byte {
	h := hmac.New(sha256.New, s.key)
	if u.login != "" {
		_, _ = fmt.Fprintf(h, "goldie-session-github\n%s\n%s\n%d\n%d", u.login, u.role, issued, expires) // a hash.Hash never errors
	} else {
		_, _ = fmt.Fprintf(h, "goldie-session\n%s\n%x\n%d\n%d", u.token.name, u.token.digest, issued, expires)
	}
	return h.Sum(nil)
}

// value is the cookie value for a session of u, started at issued and valid
// until now plus sessionTTL, or the absolute cap if that comes first.
func (s *Sessions) value(u subject, issued, now time.Time) (string, time.Time) {
	expires := now.Add(sessionTTL)
	if end := issued.Add(u.maxAge()); expires.After(end) {
		expires = end
	}
	i, x := issued.Unix(), expires.Unix()
	tail := strconv.FormatInt(i, 10) + ":" + strconv.FormatInt(x, 10) + ":" +
		base64.RawURLEncoding.EncodeToString(s.mac(u, i, x))
	if u.login != "" {
		return u.login + ":" + u.role.String() + ":" + tail, expires
	}
	return u.token.name + ":" + tail, expires
}

// open checks a cookie value: well formed, signed with this key, not expired
// and within the absolute cap. A token cookie (4 fields) must name a
// configured token (byName) and is signed over that token's current value; a
// GitHub cookie (5 fields) carries its own login and Role. It returns the
// subject and the sign-in time.
func (s *Sessions) open(value string, now time.Time, byName func(string) (tokenEntry, bool)) (u subject, issued time.Time, ok bool) {
	parts := strings.Split(value, ":")
	switch len(parts) {
	case 4:
		e, found := byName(parts[0])
		if !found {
			return subject{}, time.Time{}, false
		}
		u = tokenSubject(e)
	case 5:
		role, found := roleNames[parts[1]]
		if !found || parts[0] == "" {
			return subject{}, time.Time{}, false
		}
		u = subject{login: parts[0], role: role}
	default:
		return subject{}, time.Time{}, false
	}
	n := len(parts)
	i, err1 := strconv.ParseInt(parts[n-3], 10, 64)
	x, err2 := strconv.ParseInt(parts[n-2], 10, 64)
	sum, err3 := base64.RawURLEncoding.DecodeString(parts[n-1])
	if err1 != nil || err2 != nil || err3 != nil || !hmac.Equal(sum, s.mac(u, i, x)) {
		return subject{}, time.Time{}, false
	}
	if x-i > int64(u.maxAge()/time.Second) || !now.Before(time.Unix(x, 0)) {
		return subject{}, time.Time{}, false
	}
	return u, time.Unix(i, 0), true
}

// CSRFHeader carries the CSRF token: the response of a sign-in and of a
// session GET hands it out, and a cookie-authenticated unsafe request must
// send it back.
const CSRFHeader = "X-Goldie-CSRF"

// csrf returns the CSRF token of the session of u that began at issued (Unix
// seconds): an HMAC under the session key over what the cookie signs (the
// token's name and digest, or the login and Role) and the sign-in time. It is
// stable across cookie re-issues and unique to the session.
func (s *Sessions) csrf(u subject, issued int64) string {
	h := hmac.New(sha256.New, s.key)
	if u.login != "" {
		_, _ = fmt.Fprintf(h, "goldie-csrf-github\n%s\n%s\n%d", u.login, u.role, issued) // a hash.Hash never errors
	} else {
		_, _ = fmt.Fprintf(h, "goldie-csrf\n%s\n%x\n%d", u.token.name, u.token.digest, issued)
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// csrfOK reports whether r carries the CSRF token of the session of u.
func (s *Sessions) csrfOK(r *http.Request, u subject, issued time.Time) bool {
	return hmac.Equal([]byte(r.Header.Get(CSRFHeader)), []byte(s.csrf(u, issued.Unix())))
}

var crossOrigin = http.NewCrossOriginProtection()

// crossSite reports a request whose Origin or Sec-Fetch-Site says another
// site sent it. A request with neither header (a non-browser client) passes;
// the CSRF token is what it needs.
func crossSite(r *http.Request) bool { return crossOrigin.Check(r) != nil }

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

// serveCookie authenticates a request with a valid session cookie; an unsafe
// method also needs the CSRF token. A token session looks the token up by
// name, so its Role is the configuration's now and a removed token ends the
// session; a GitHub session's Role is the one its cookie signs. It
// re-issues the cookie on every request it lets through. handled is false when
// the request carries no cookie.
func (t *Tokens) serveCookie(w http.ResponseWriter, r *http.Request, minRole Role, next http.Handler) (handled bool) {
	c, err := r.Cookie(t.sessions.cookieName(r))
	if err != nil {
		return false
	}
	now := time.Now()
	u, issued, ok := t.sessions.open(c.Value, now, t.byName)
	if !ok {
		return false // not a session: the usual 401
	}
	safe := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case !safe && crossSite(r):
		WriteError(w, http.StatusForbidden, "forbidden", "cross-origin requests with a session cookie are refused",
			"Send the request from the same origin.")
	case !safe && !t.sessions.csrfOK(r, u, issued):
		WriteError(w, http.StatusForbidden, "forbidden", "a session cookie needs a valid CSRF token for this request",
			"Send the token from GET /api/v1/session in the "+CSRFHeader+" header, or send the API token as a bearer token.")
	case u.role < minRole:
		WriteError(w, http.StatusForbidden, "forbidden", "the API token's role does not allow this operation",
			"Use a token with the operator role.")
	default:
		v, expires := t.sessions.value(u, issued, now)
		w.Header().Add("Vary", "Cookie")
		w.Header().Set("Cache-Control", "private, no-store")
		t.sessions.setCookie(w, r, v, now, expires)
		if r.URL.Path == SessionPath {
			w.Header().Set(CSRFHeader, t.sessions.csrf(u, issued.Unix()))
		}
		ctx := context.WithValue(r.Context(), tokenNameKey{}, u.label())
		ctx = context.WithValue(ctx, limitKeyKey{}, u.limitKey())
		ctx = context.WithValue(ctx, sessionKey{}, true)
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, tokenRoleKey{}, u.role)))
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
	return NoStore(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if crossSite(r) {
			WriteError(w, http.StatusForbidden, "forbidden", "cross-origin requests to the session are refused",
				"Send the request from the same origin.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxSignInBody)
		next.ServeHTTP(w, r)
	}))
}

// ServeSessions is the option that serves the session operations over tokens,
// which must have been through Tokens.WithSessions.
func ServeSessions(tokens *Tokens) ServerOption { return func(s *Server) { s.tokens = tokens } }

// WithSwaggerUI sets the swagger_ui field of the session: whether the server
// serves Swagger UI, so the web UI links to it only when it is there.
func WithSwaggerUI(on bool) ServerOption { return func(s *Server) { s.swaggerUI = on } }

// WithObservabilityLinks sets the grafana_url and trace_url_template of the
// session (#807): the links of the activity view. Only an operator's session
// carries them, and an empty one is omitted. The configuration has validated
// both.
func WithObservabilityLinks(grafanaURL, traceURLTemplate string) ServerOption {
	return func(s *Server) { s.grafana, s.traceTmpl = grafanaURL, traceURLTemplate }
}

// WithGitHubSignIn sets whether the server offers GitHub sign-in, which
// GET /api/v1/session/providers reports. The configuration has validated the
// settings as a whole, so a client ID means all of them.
func WithGitHubSignIn(on bool) ServerOption { return func(s *Server) { s.github = on } }

// link is v as the optional string of a session of role, nil when the role is
// not the operator's or v is unset.
func link(role Role, v string) *string {
	if role != RoleOperator || v == "" {
		return nil
	}
	return &v
}

// Sign-in and sign-out take no token, so neither reaches the per-token rate
// limiter or the version gate (ADR 0027); failed sign-ins are capped per
// remote address by SignInLimiter.
var (
	unauthorizedSignIn = envelope("unauthorized", "missing or invalid API token",
		"Tokens are case-sensitive, so copy yours exactly as issued.")
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
	return contract.PostSession200JSONResponse{Name: e.name, Role: contract.SessionRole(e.role.String()), SwaggerUi: s.swaggerUI,
		GrafanaUrl: link(e.role, s.grafana), TraceUrlTemplate: link(e.role, s.traceTmpl)}, nil
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
	return contract.GetSession200JSONResponse{Name: name, Role: contract.SessionRole(role.String()), SwaggerUi: s.swaggerUI,
		GrafanaUrl: link(role, s.grafana), TraceUrlTemplate: link(role, s.traceTmpl)}, nil
}

func (s *Server) GetSessionProviders(context.Context, contract.GetSessionProvidersRequestObject) (contract.GetSessionProvidersResponseObject, error) {
	return contract.GetSessionProviders200JSONResponse{Github: s.github}, nil
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
			u := tokenSubject(e)
			v, expires := s.tokens.sessions.value(u, now, now)
			s.tokens.sessions.setCookie(w, r, v, now, expires)
			w.Header().Set(CSRFHeader, s.tokens.sessions.csrf(u, now.Unix()))
		case contract.DeleteSession204Response:
			if s.tokens != nil && s.tokens.sessions != nil {
				s.tokens.sessions.setCookie(w, r, "", now, time.Time{})
			}
		}
		return resp, err
	}
}
