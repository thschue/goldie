package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// Sign in with GitHub (#644, slice 3 of #25): GET /api/v1/session/github sends
// the browser to GitHub with an S256 PKCE challenge and a state, and a signed
// pre-login cookie; GET /api/v1/session/github/callback checks the cookie and
// the state, maps the user to a Role, sets the session cookie and redirects to
// a same-site next path. These tests drive the real public handler over HTTP
// against a fake GitHub Enterprise server that behaves like GitHub where it
// matters: a code works once, and only with the verifier of the challenge it
// was issued for.
//
// The issue does not say where next travels (the start request or the
// callback), so every test sends it on both; either reading passes. It does
// not fix the fixed error codes either, so they are checked for their shape.

const (
	ghStartPath    = "/api/v1/session/github"
	ghCallbackPath = ghStartPath + "/callback"
	ghClientID     = "goldie-client-id"
	ghClientSecret = "goldie-client-secret-9e8d7c"
	ghCanary       = "GITHUB-TEXT-MUST-NOT-ECHO-31337"
)

var ghErrorCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

type ghGrant struct {
	login, challenge string
	used             bool
}

// ghFake is GitHub: the OAuth endpoints under the base URL, the REST API under
// /api/v3. alice and bob are members of acme, olga is also in acme/ops, and
// mallory is in nothing.
type ghFake struct {
	*httptest.Server
	mu     sync.Mutex
	grants map[string]*ghGrant
	n      int
}

var ghMembers = map[string]map[string]bool{
	"alice":   {"acme": true},
	"bob":     {"acme": true},
	"olga":    {"acme": true, "acme/ops": true},
	"mallory": {},
}

func newGHFake(t *testing.T) *ghFake {
	t.Helper()
	f := &ghFake{grants: map[string]*ghGrant{}}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Connection", "close")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		g := f.grants[r.Form.Get("code")]
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if g == nil || g.used || r.Form.Get("client_id") != ghClientID || r.Form.Get("client_secret") != ghClientSecret ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			reply(w, http.StatusOK, map[string]string{"error": "bad_verification_code", "error_description": ghCanary})
			return
		}
		g.used = true
		reply(w, http.StatusOK, map[string]string{"access_token": "gho_" + g.login, "token_type": "bearer"})
	})
	login := func(r *http.Request) string {
		return strings.TrimPrefix(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "gho_")
	}
	mux.HandleFunc("GET /api/v3/user", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := ghMembers[login(r)]; !ok {
			reply(w, http.StatusUnauthorized, map[string]string{"message": ghCanary})
			return
		}
		reply(w, http.StatusOK, map[string]string{"login": login(r)})
	})
	member := func(w http.ResponseWriter, who, key string) {
		if ghMembers[who][key] {
			reply(w, http.StatusOK, map[string]string{"state": "active"})
			return
		}
		reply(w, http.StatusNotFound, map[string]string{"message": ghCanary})
	}
	mux.HandleFunc("GET /api/v3/user/memberships/orgs/{org}", func(w http.ResponseWriter, r *http.Request) {
		member(w, login(r), r.PathValue("org"))
	})
	mux.HandleFunc("GET /api/v3/orgs/{org}/teams/{slug}/memberships/{user}", func(w http.ResponseWriter, r *http.Request) {
		member(w, r.PathValue("user"), r.PathValue("org")+"/"+r.PathValue("slug"))
	})
	mux.HandleFunc("DELETE /api/v3/applications/{id}/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusNoContent)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// issue is the code GitHub hands the browser after login approved the app for
// the given PKCE challenge.
func (f *ghFake) issue(login, challenge string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	code := "code-" + login + "-" + string(rune('a'+f.n))
	f.grants[code] = &ghGrant{login: login, challenge: challenge}
	return code
}

// ghHandler is the public handler with GitHub sign-in on against f, signed
// with key.
func ghHandler(t *testing.T, f *ghFake, key string, logger *slog.Logger) *Public {
	t.Helper()
	cfg := sessionConfig(t, namedTokenLines(), map[string]string{
		"GOLDIE_SESSION_KEY":           key,
		"GOLDIE_GITHUB_CLIENT_ID":      ghClientID,
		"GOLDIE_GITHUB_CLIENT_SECRET":  ghClientSecret,
		"GOLDIE_GITHUB_URL":            f.URL,
		"GOLDIE_GITHUB_ORGS":           "acme",
		"GOLDIE_GITHUB_OPERATOR_TEAMS": "acme/ops",
		"GOLDIE_PUBLIC_URL":            secureBase,
	})
	return ghHandlerFor(t, cfg, logger)
}

func ghHandlerFor(t *testing.T, cfg platform.Config, logger *slog.Logger) *Public {
	t.Helper()
	h, err := PublicHandler(cfg, logger)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	return h
}

// ghStart is the browser's first step.
type ghStart struct {
	resp      *http.Response
	body      []byte
	loc       *url.URL
	state     string
	challenge string
	cookie    *http.Cookie
}

func ghStartFlow(t *testing.T, h http.Handler, next string) ghStart {
	t.Helper()
	target := secureBase + ghStartPath
	if next != "" {
		target += "?next=" + url.QueryEscape(next)
	}
	resp, body := send(t, h, http.MethodGet, target, nil)
	s := ghStart{resp: resp, body: body}
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		t.Fatalf("GET %s: status %d, want a redirect: %s", ghStartPath, resp.StatusCode, body)
	}
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("redirect without a Location: %v", err)
	}
	s.loc = loc
	s.state = loc.Query().Get("state")
	s.challenge = loc.Query().Get("code_challenge")
	if cs := resp.Cookies(); len(cs) == 1 {
		s.cookie = cs[0]
	}
	return s
}

// ghCallback is the browser coming back from GitHub, a cross-site navigation.
func ghCallback(t *testing.T, h http.Handler, query url.Values, next string, cookie *http.Cookie) (*http.Response, []byte) {
	t.Helper()
	if next != "" {
		query.Set("next", next)
	}
	return send(t, h, http.MethodGet, secureBase+ghCallbackPath+"?"+query.Encode(), func(r *http.Request) {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		if cookie != nil {
			withCookie(cookie)(r)
		}
	})
}

// ghSessionCookie is the session cookie a response sets, nil when it sets none.
func ghSessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if strings.HasSuffix(c.Name, "goldie_session") && c.Value != "" && c.MaxAge >= 0 {
			return c
		}
	}
	return nil
}

// ghSignIn runs the whole flow for login and returns the session cookie.
func ghSignIn(t *testing.T, h http.Handler, f *ghFake, login string) *http.Cookie {
	t.Helper()
	s := ghStartFlow(t, h, "/activity")
	resp, body := ghCallback(t, h, url.Values{"code": {f.issue(login, s.challenge)}, "state": {s.state}}, "/activity", s.cookie)
	c := ghSessionCookie(resp)
	if c == nil {
		t.Fatalf("sign-in of %s: status %d, Location %q, no session cookie: %s", login, resp.StatusCode, resp.Header.Get("Location"), body)
	}
	return c
}

// ghAssertRefused checks the response ends at /signin?error=<fixed code> with
// no session and none of GitHub's text.
func ghAssertRefused(t *testing.T, what string, resp *http.Response, body []byte) {
	t.Helper()
	loc, err := resp.Location()
	if resp.StatusCode < 300 || resp.StatusCode > 399 || err != nil || loc.Path != "/signin" || !ghErrorCode.MatchString(loc.Query().Get("error")) {
		t.Errorf("%s: status %d, Location %q, want a redirect to /signin?error=<code>: %s", what, resp.StatusCode, resp.Header.Get("Location"), body)
	}
	if err == nil && loc.Host != "" && loc.Host != "goldie.example.com" {
		t.Errorf("%s: Location %q leaves the site", what, loc)
	}
	if c := ghSessionCookie(resp); c != nil {
		t.Errorf("%s: sets a session cookie %q", what, c.Name)
	}
	assertNoLeak(t, what, resp, body, ghCanary, ghClientSecret, "gho_")
}

// The start redirects to GitHub's authorize endpoint with the app, the
// callback as redirect URI, an S256 challenge and a state, and sets one signed
// pre-login cookie: SameSite=Lax (the browser comes back cross-site), ten
// minutes at most.
func TestGitHubSignInStartRedirectsWithPKCEAndASignedLaxCookie(t *testing.T) {
	f := newGHFake(t)
	h := ghHandler(t, f, sessionKey, nil)
	s := ghStartFlow(t, h, "/activity")
	if got := s.loc.Scheme + "://" + s.loc.Host + s.loc.Path; got != f.URL+"/login/oauth/authorize" {
		t.Errorf("redirects to %s, want %s/login/oauth/authorize", got, f.URL)
	}
	q := s.loc.Query()
	if q.Get("client_id") != ghClientID {
		t.Errorf("client_id %q, want %q", q.Get("client_id"), ghClientID)
	}
	if q.Get("redirect_uri") != secureBase+ghCallbackPath {
		t.Errorf("redirect_uri %q, want %q (built from GOLDIE_PUBLIC_URL)", q.Get("redirect_uri"), secureBase+ghCallbackPath)
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method %q, want S256", q.Get("code_challenge_method"))
	}
	if len(s.challenge) != 43 {
		t.Errorf("code_challenge %q is not a base64url SHA-256", s.challenge)
	}
	if len(s.state) < 16 {
		t.Errorf("state %q is too short to be unguessable", s.state)
	}
	if strings.Contains(s.loc.String(), ghClientSecret) {
		t.Error("the redirect carries the client secret")
	}
	assertNoStore(t, "start", s.resp)
	if len(s.resp.Cookies()) != 1 {
		t.Fatalf("the start sets %d cookies, want exactly the pre-login cookie: %v", len(s.resp.Cookies()), s.resp.Header.Values("Set-Cookie"))
	}
	c := s.cookie
	if c.SameSite != http.SameSiteLaxMode || !c.HttpOnly || c.Value == "" {
		t.Errorf("pre-login cookie %+v, want a value, HttpOnly and SameSite=Lax", c)
	}
	if c.MaxAge <= 0 || c.MaxAge > 600 {
		t.Errorf("pre-login cookie Max-Age %d, want 1 to 600 seconds", c.MaxAge)
	}
	again := ghStartFlow(t, h, "/activity")
	if again.state == s.state || again.challenge == s.challenge || again.cookie.Value == s.cookie.Value {
		t.Error("a second start repeats the state, challenge or cookie")
	}
}

// A member signs in and lands on next with a hardened session cookie that
// reads back as github:<role>; the token and the client secret appear nowhere.
func TestGitHubCallbackMapsMembersToARoleAndRedirectsToNext(t *testing.T) {
	f := newGHFake(t)
	h := ghHandler(t, f, sessionKey, nil)
	for login, want := range map[string]sessionBody{
		"alice": {"github:reader", "reader"},
		"olga":  {"github:operator", "operator"},
	} {
		s := ghStartFlow(t, h, "/activity")
		resp, body := ghCallback(t, h, url.Values{"code": {f.issue(login, s.challenge)}, "state": {s.state}}, "/activity", s.cookie)
		loc, err := resp.Location()
		if resp.StatusCode < 300 || resp.StatusCode > 399 || err != nil || loc.Path != "/activity" || (loc.Host != "" && loc.Host != "goldie.example.com") {
			t.Fatalf("%s: status %d, Location %q, want a same-site redirect to /activity: %s", login, resp.StatusCode, resp.Header.Get("Location"), body)
		}
		assertNoStore(t, login, resp)
		c := ghSessionCookie(resp)
		if c == nil {
			t.Fatalf("%s: no session cookie: %v", login, resp.Header.Values("Set-Cookie"))
		}
		if !strings.HasPrefix(c.Name, "__Host-") || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
			t.Errorf("%s: cookie %+v, want the hardened __Host- session cookie", login, c)
		}
		assertNoLeak(t, login, resp, body, "gho_"+login, ghClientSecret)
		if strings.Contains(c.Value, "gho_") || strings.Contains(c.Value, ghClientSecret) {
			t.Errorf("%s: the session cookie carries a GitHub secret", login)
		}
		r, b := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c))
		assertStatus(t, login+" GET session", r, b, http.StatusOK)
		if got := readSession(t, b); got != want {
			t.Errorf("%s: session %+v, want %+v", login, got, want)
		}
	}
}

// A sign-in that is replayed, forged, unbound, hostile or not a member's ends
// at /signin?error=<fixed code> with no session, and GitHub's own text is
// never echoed.
func TestGitHubCallbackRefusesForgedReplayedHostileAndNonMemberSignIns(t *testing.T) {
	f := newGHFake(t)
	h := ghHandler(t, f, sessionKey, nil)
	other := ghHandler(t, f, otherKey, nil)

	t.Run("forged state", func(t *testing.T) {
		s := ghStartFlow(t, h, "/activity")
		resp, body := ghCallback(t, h, url.Values{"code": {f.issue("alice", s.challenge)}, "state": {"forged-" + s.state}}, "/activity", s.cookie)
		ghAssertRefused(t, "forged state", resp, body)
	})
	t.Run("state of another sign-in", func(t *testing.T) {
		a, b := ghStartFlow(t, h, "/activity"), ghStartFlow(t, h, "/activity")
		resp, body := ghCallback(t, h, url.Values{"code": {f.issue("alice", a.challenge)}, "state": {b.state}}, "/activity", a.cookie)
		ghAssertRefused(t, "state of another sign-in", resp, body)
	})
	t.Run("no pre-login cookie", func(t *testing.T) {
		s := ghStartFlow(t, h, "/activity")
		resp, body := ghCallback(t, h, url.Values{"code": {f.issue("alice", s.challenge)}, "state": {s.state}}, "/activity", nil)
		ghAssertRefused(t, "no pre-login cookie", resp, body)
	})
	t.Run("pre-login cookie signed with another key", func(t *testing.T) {
		foreign := ghStartFlow(t, other, "/activity")
		resp, body := ghCallback(t, h, url.Values{"code": {f.issue("alice", foreign.challenge)}, "state": {foreign.state}}, "/activity", foreign.cookie)
		ghAssertRefused(t, "foreign pre-login cookie", resp, body)
	})
	t.Run("replay", func(t *testing.T) {
		s := ghStartFlow(t, h, "/activity")
		q := url.Values{"code": {f.issue("alice", s.challenge)}, "state": {s.state}}
		if resp, body := ghCallback(t, h, q, "/activity", s.cookie); ghSessionCookie(resp) == nil {
			t.Fatalf("the first callback did not sign in: %d %s", resp.StatusCode, body)
		}
		resp, body := ghCallback(t, h, url.Values{"code": q["code"], "state": q["state"]}, "/activity", s.cookie)
		ghAssertRefused(t, "replay", resp, body)
	})
	t.Run("GitHub refuses the code", func(t *testing.T) {
		s := ghStartFlow(t, h, "/activity")
		resp, body := ghCallback(t, h, url.Values{"code": {"never-issued"}, "state": {s.state}}, "/activity", s.cookie)
		ghAssertRefused(t, "refused code", resp, body)
	})
	t.Run("the user denied the app", func(t *testing.T) {
		s := ghStartFlow(t, h, "/activity")
		resp, body := ghCallback(t, h, url.Values{"error": {"access_denied"}, "error_description": {ghCanary}, "state": {s.state}}, "/activity", s.cookie)
		ghAssertRefused(t, "denied", resp, body)
	})
	t.Run("not a member", func(t *testing.T) {
		s := ghStartFlow(t, h, "/activity")
		resp, body := ghCallback(t, h, url.Values{"code": {f.issue("mallory", s.challenge)}, "state": {s.state}}, "/activity", s.cookie)
		ghAssertRefused(t, "non-member", resp, body)
	})
	for _, next := range []string{"https://evil.example/x", "//evil.example/x", `/\evil.example/x`, "javascript:alert(1)"} {
		t.Run("next "+next, func(t *testing.T) {
			target := secureBase + ghStartPath + "?next=" + url.QueryEscape(next)
			resp, body := send(t, h, http.MethodGet, target, nil)
			if loc, err := resp.Location(); err == nil && loc.Path == "/signin" {
				ghAssertRefused(t, "start with next "+next, resp, body) // refused at the start
				return
			}
			s := ghStartFlow(t, h, next)
			resp, body = ghCallback(t, h, url.Values{"code": {f.issue("alice", s.challenge)}, "state": {s.state}}, next, s.cookie)
			ghAssertRefused(t, "next "+next, resp, body)
		})
	}
}

// The GitHub session behaves like a token session on the session resource and
// ends with sign-out; a cookie signed with another key is rejected; token
// sessions are unchanged.
func TestGitHubSessionReadsBackIsRejectedUnderAnotherKeyAndSignsOut(t *testing.T) {
	f := newGHFake(t)
	h := ghHandler(t, f, sessionKey, nil)
	other := ghHandler(t, f, otherKey, nil)
	c := ghSignIn(t, h, f, "alice")

	resp, body := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c))
	assertStatus(t, "GET session", resp, body, http.StatusOK)
	if got := readSession(t, body); got.Name != "github:reader" || got.Role != "reader" {
		t.Errorf("session %+v, want github:reader", got)
	}
	resp, body = send(t, h, http.MethodGet, secureBase+sessionSearch, withCookie(c))
	assertStatus(t, "search with the GitHub session", resp, body, http.StatusOK)

	for _, path := range []string{sessionPath, sessionSearch} {
		resp, body := send(t, other, http.MethodGet, secureBase+path, withCookie(c))
		assertStatus(t, "cookie signed with another key on "+path, resp, body, http.StatusUnauthorized)
		assertNoCookie(t, "cookie signed with another key", resp)
	}

	tc := signedIn(t, h, secureBase, operatorToken)
	resp, body = send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(tc))
	assertStatus(t, "token session GET", resp, body, http.StatusOK)
	if got := readSession(t, body); got.Name != "ci-operator" || got.Role != "operator" {
		t.Errorf("token session %+v, want ci-operator, still named by its token", got)
	}

	resp, body = send(t, h, http.MethodDelete, secureBase+sessionPath, both(withCookie(c), setSameOrigin))
	assertStatus(t, "sign-out", resp, body, http.StatusNoContent)
	if cleared := resp.Cookies(); len(cleared) != 1 || cleared[0].Name != c.Name || cleared[0].MaxAge >= 0 {
		t.Errorf("sign-out sets %v, want the session cookie cleared", resp.Header.Values("Set-Cookie"))
	}
}

// The pre-login cookie lives ten minutes. A GitHub session ends at an absolute
// cap of about one hour however often it is used (the membership re-check),
// and idle it ends sooner. The fake GitHub runs outside the clock bubble and
// closes every connection, so nothing blocks the fake clock.
func TestGitHubSignInExpiresOnTheClock(t *testing.T) {
	f := newGHFake(t)

	t.Run("the pre-login cookie is dead after ten minutes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := ghHandler(t, f, sessionKey, nil)
			s := ghStartFlow(t, h, "/activity")
			code := f.issue("alice", s.challenge)
			time.Sleep(11 * time.Minute)
			resp, body := ghCallback(t, h, url.Values{"code": {code}, "state": {s.state}}, "/activity", s.cookie)
			ghAssertRefused(t, "callback after 11 minutes", resp, body)
		})
	})
	t.Run("a session in constant use ends at about one hour", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := ghHandler(t, f, sessionKey, nil)
			c := ghSignIn(t, h, f, "alice")
			for _, elapsed := range []time.Duration{25 * time.Minute, 50 * time.Minute} {
				time.Sleep(25 * time.Minute)
				resp, body := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c))
				assertStatus(t, "after "+elapsed.String(), resp, body, http.StatusOK)
				if next := ghSessionCookie(resp); next != nil {
					c = next
				}
			}
			time.Sleep(25 * time.Minute)
			resp, body := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c))
			assertStatus(t, "after 75 minutes of constant use", resp, body, http.StatusUnauthorized)
			assertNoCookie(t, "expired", resp)
			resp, body = send(t, h, http.MethodGet, secureBase+sessionSearch, withCookie(c))
			assertStatus(t, "expired on the API", resp, body, http.StatusUnauthorized)
		})
	})
	t.Run("an idle session ends", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := ghHandler(t, f, sessionKey, nil)
			c := ghSignIn(t, h, f, "alice")
			time.Sleep(2 * time.Hour)
			resp, body := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c))
			assertStatus(t, "idle for two hours", resp, body, http.StatusUnauthorized)
		})
	})
}

// The principal is named github:<role> in logs, metrics and the rejection
// counter, never by login; the rate limiter keys on the login, so one user over
// the limit does not limit another of the same role.
func TestGitHubPrincipalIsLabelledByRoleAndLimitedByLogin(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f := newGHFake(t)
	cfg := sessionConfig(t, namedTokenLines(), map[string]string{
		"GOLDIE_SESSION_KEY":           sessionKey,
		"GOLDIE_GITHUB_CLIENT_ID":      ghClientID,
		"GOLDIE_GITHUB_CLIENT_SECRET":  ghClientSecret,
		"GOLDIE_GITHUB_URL":            f.URL,
		"GOLDIE_GITHUB_ORGS":           "acme",
		"GOLDIE_GITHUB_OPERATOR_TEAMS": "acme/ops",
		"GOLDIE_PUBLIC_URL":            secureBase,
	})
	cfg.RateLimit = loadedRateLimit(t, "2/m")
	h := ghHandlerFor(t, cfg, logger)

	alice, bob := ghSignIn(t, h, f, "alice"), ghSignIn(t, h, f, "bob")
	for i := range 2 {
		if resp, body := send(t, h, http.MethodGet, secureBase+sessionSearch, withCookie(alice)); resp.StatusCode != http.StatusOK {
			t.Fatalf("alice request %d within the limit: status %d: %s", i+1, resp.StatusCode, body)
		}
	}
	resp, body := send(t, h, http.MethodGet, secureBase+sessionSearch, withCookie(alice))
	assertStatus(t, "alice over her limit", resp, body, http.StatusTooManyRequests)
	resp, body = send(t, h, http.MethodGet, secureBase+sessionSearch, withCookie(bob))
	assertStatus(t, "bob, same role, own bucket", resp, body, http.StatusOK)

	got := rejections(t, h)
	if got["github:reader"] == 0 {
		t.Errorf("rejections %v, want the one of alice counted under github:reader", got)
	}
	for name := range got {
		if strings.Contains(name, "alice") || strings.Contains(name, "bob") {
			t.Errorf("a rejection label %q carries a login", name)
		}
	}
	for _, secret := range []string{"alice", "bob", alice.Value, bob.Value, sessionKey, ghClientSecret, "gho_"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("a log line carries %q: %s", secret, logs.String())
		}
	}
}
