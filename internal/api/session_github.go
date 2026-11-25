package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thschue/goldie/internal/api/github"
	"github.com/thschue/goldie/internal/contract"
)

// Sign in with GitHub (ADR 0032): an OAuth authorization code flow with PKCE.
// The start sends the browser to GitHub with a state and an S256 challenge and
// a signed pre-login cookie that holds the state, the verifier and the path to
// return to; the callback checks the cookie and the state, asks GitHub who the
// user is and which Role that earns, and sets the session cookie.

const (
	// GitHubCallbackPath is where GitHub sends the browser back to.
	GitHubCallbackPath = SessionPath + "/github/callback"

	githubLoginCookie = "goldie_github_login"
	// githubLoginTTL is how long the browser has to finish at GitHub.
	githubLoginTTL = 10 * time.Minute
	maxNextLength  = 512
)

// GitHubSignIn is what the flow needs of the GitHub client (internal/api/github).
type GitHubSignIn interface {
	AuthorizeURL(redirectURI, challenge, state string) string
	Exchange(ctx context.Context, code, verifier, redirectURI string) (github.Identity, error)
}

// WithGitHub turns sign-in with GitHub on: gh talks to GitHub, and the redirect
// URI GitHub sends the browser back to is built from publicURL (GOLDIE_PUBLIC_URL),
// never from a request header. It needs the sessions of ServeSessions.
func WithGitHub(gh GitHubSignIn, publicURL string) ServerOption {
	return func(s *Server) {
		s.gh, s.ghRedirect = gh, strings.TrimRight(publicURL, "/")+GitHubCallbackPath
	}
}

var notConfiguredGitHub = envelope("not_found", "GitHub sign-in is not configured on this server",
	"Sign in with a token instead.")

// signInRedirect is a refusal: the sign-in page with a fixed error code, which
// is all the browser ever learns (GitHub's own text is never echoed).
func signInRedirect(code string) *string {
	l := "/signin?error=" + code
	return &l
}

// githubFlow is what the request hands the flow operations: the response the
// cookies go on and the request they came on.
type githubFlow struct {
	w http.ResponseWriter
	r *http.Request
}

type githubFlowKey struct{}

// githubFlowRequest hands the two flow operations the request and the response
// writer, so they can read and set cookies (like activityRequest).
func (s *Server) githubFlowRequest(next contract.StrictHandlerFunc, op string) contract.StrictHandlerFunc {
	if op != "GetSessionGithub" && op != "GetSessionGithubCallback" {
		return next
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		return next(context.WithValue(ctx, githubFlowKey{}, githubFlow{w, r}), w, r, req)
	}
}

func (s *Server) githubReady() bool {
	return s.gh != nil && s.tokens != nil && s.tokens.sessions != nil
}

func (s *Server) GetSessionGithub(ctx context.Context, req contract.GetSessionGithubRequestObject) (contract.GetSessionGithubResponseObject, error) {
	f, ok := ctx.Value(githubFlowKey{}).(githubFlow)
	if !ok || !s.githubReady() {
		return contract.GetSessionGithub404JSONResponse(notConfiguredGitHub), nil
	}
	next := "/"
	if req.Params.Next != nil {
		next = *req.Params.Next
	}
	if !sameSitePath(next) {
		return contract.GetSessionGithub302Response{Headers: contract.GetSessionGithub302ResponseHeaders{Location: signInRedirect("invalid_next")}}, nil
	}
	state, verifier := randomToken(), randomToken()
	if state == "" || verifier == "" {
		return nil, errors.New("api: no randomness for the GitHub sign-in")
	}
	now := time.Now()
	sum := sha256.Sum256([]byte(verifier))
	s.tokens.sessions.setLoginCookie(f.w, f.r, githubLogin{State: state, Verifier: verifier, Next: next, Expires: now.Add(githubLoginTTL).Unix()}, now)
	loc := s.gh.AuthorizeURL(s.ghRedirect, base64.RawURLEncoding.EncodeToString(sum[:]), state)
	return contract.GetSessionGithub302Response{Headers: contract.GetSessionGithub302ResponseHeaders{Location: &loc}}, nil
}

func (s *Server) GetSessionGithubCallback(ctx context.Context, req contract.GetSessionGithubCallbackRequestObject) (contract.GetSessionGithubCallbackResponseObject, error) {
	f, ok := ctx.Value(githubFlowKey{}).(githubFlow)
	if !ok || !s.githubReady() {
		return contract.GetSessionGithubCallback404JSONResponse(notConfiguredGitHub), nil
	}
	sessions, now := s.tokens.sessions, time.Now()
	login, ok := sessions.openLoginCookie(f.r, now)
	sessions.clearLoginCookie(f.w, f.r) // a state works once, whatever follows
	refuse := func(code string, count bool) (contract.GetSessionGithubCallbackResponseObject, error) {
		if count {
			ReportSignInFailure(ctx)
		}
		return contract.GetSessionGithubCallback302Response{Headers: contract.GetSessionGithubCallback302ResponseHeaders{Location: signInRedirect(code)}}, nil
	}
	var state, code string
	if req.Params.State != nil {
		state = *req.Params.State
	}
	if req.Params.Code != nil {
		code = *req.Params.Code
	}
	if !ok || state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(login.State)) != 1 {
		return refuse("state_invalid", true)
	}
	if req.Params.Error != nil {
		return refuse("access_denied", false)
	}
	id, err := s.gh.Exchange(ctx, code, login.Verifier, s.ghRedirect)
	if errors.Is(err, github.ErrNotMember) {
		return refuse("not_a_member", false)
	}
	role, known := roleNames[id.Role]
	if err != nil || !known {
		return refuse("exchange_failed", true)
	}
	v, expires := sessions.value(subject{login: id.Login, role: role}, now, now)
	sessions.setCookie(f.w, f.r, v, now, expires)
	return contract.GetSessionGithubCallback302Response{Headers: contract.GetSessionGithubCallback302ResponseHeaders{Location: &login.Next}}, nil
}

// sameSitePath reports whether next is a path on this site: one slash, then
// not a second slash or a backslash (which browsers read as another host), no
// control character, and nothing a URL parser reads as a scheme or host.
func sameSitePath(next string) bool {
	if next == "" || len(next) > maxNextLength || next[0] != '/' {
		return false
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return false
	}
	for _, c := range []byte(next) {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return false
		}
	}
	u, err := url.Parse(next)
	return err == nil && u.Scheme == "" && u.Host == ""
}

// randomToken is 32 random bytes as 43 URL-safe characters, "" if the system
// has no randomness.
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// githubLogin is the pre-login cookie's content.
type githubLogin struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Next     string `json:"n"`
	Expires  int64  `json:"x"`
}

func (s *Sessions) loginMAC(payload string) []byte {
	h := hmac.New(sha256.New, s.key)
	_, _ = h.Write([]byte("goldie-github-login\n" + payload)) // a hash.Hash never errors
	return h.Sum(nil)
}

func (s *Sessions) loginCookieName(r *http.Request) string {
	if s.plain(r) {
		return githubLoginCookie
	}
	return hostPrefix + githubLoginCookie
}

// setLoginCookie sends the signed pre-login cookie. It is SameSite=Lax, not
// Strict: GitHub's redirect back is a cross-site navigation, which a Strict
// cookie would not ride on.
func (s *Sessions) setLoginCookie(w http.ResponseWriter, r *http.Request, l githubLogin, now time.Time) {
	b, _ := json.Marshal(l) // strings and an int64 always marshal
	p := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name: s.loginCookieName(r), Value: p + "." + base64.RawURLEncoding.EncodeToString(s.loginMAC(p)),
		Path: "/", MaxAge: int(time.Unix(l.Expires, 0).Sub(now) / time.Second),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: !s.plain(r),
	})
}

func (s *Sessions) clearLoginCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: s.loginCookieName(r), Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: !s.plain(r),
	})
}

// openLoginCookie returns the pre-login cookie's content when it is well
// formed, signed with this key and not expired.
func (s *Sessions) openLoginCookie(r *http.Request, now time.Time) (githubLogin, bool) {
	c, err := r.Cookie(s.loginCookieName(r))
	if err != nil {
		return githubLogin{}, false
	}
	p, sig, ok := strings.Cut(c.Value, ".")
	sum, err := base64.RawURLEncoding.DecodeString(sig)
	if !ok || err != nil || !hmac.Equal(sum, s.loginMAC(p)) {
		return githubLogin{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	var l githubLogin
	if err != nil || json.Unmarshal(raw, &l) != nil || !now.Before(time.Unix(l.Expires, 0)) ||
		l.State == "" || l.Verifier == "" || !sameSitePath(l.Next) {
		return githubLogin{}, false
	}
	return l, true
}
