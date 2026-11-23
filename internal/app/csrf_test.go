package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// CSRF protection for cookie-authenticated writes (#490, slice 3 of #21).
// Sign-in and GET /api/v1/session return a signed double-submit token; a
// cookie-authenticated unsafe request must echo it in X-Goldie-CSRF. A bearer
// request needs none, and a cross-site Origin or Sec-Fetch-Site is refused.
// These tests drive the real public handler over HTTP.
//
// The issue does not fix where the token is returned. csrfTokenOf reads the
// X-Goldie-CSRF response header or a "csrf_token" key in the JSON body, so
// either reading passes.

const csrfHeader = "X-Goldie-CSRF"

// csrfTokenOf is the CSRF token a session response carries, or "".
func csrfTokenOf(resp *http.Response, body []byte) string {
	if v := resp.Header.Get(csrfHeader); v != "" {
		return v
	}
	var raw map[string]any
	if json.Unmarshal(body, &raw) == nil {
		if s, ok := raw["csrf_token"].(string); ok {
			return s
		}
	}
	return ""
}

// csrfSession signs token in and returns the session cookie and the CSRF
// token the sign-in returned.
func csrfSession(t *testing.T, h http.Handler, token string) (*http.Cookie, string) {
	t.Helper()
	resp, body := signIn(t, h, secureBase, token)
	assertStatus(t, "sign-in", resp, body, http.StatusOK)
	csrf := csrfTokenOf(resp, body)
	if csrf == "" {
		t.Fatalf("sign-in returns no CSRF token (header %s or body csrf_token): %s", csrfHeader, body)
	}
	return theCookie(t, resp), csrf
}

func withCSRF(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(csrfHeader, token) }
}

func withOrigin(origin string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Origin", origin) }
}

func withFetchSite(site string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", site) }
}

func noMutation(*http.Request) {}

func postTrigger(t *testing.T, h http.Handler, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return send(t, h, http.MethodPost, secureBase+"/api/v1/sync", both(mutate...))
}

func assertRefusedCSRF(t *testing.T, what string, resp *http.Response, body []byte) {
	t.Helper()
	assertStatus(t, what, resp, body, http.StatusForbidden)
	assertEnvelope(t, body, "forbidden")
	assertNoCookie(t, what, resp)
}

// Sign-in and GET /api/v1/session each return a CSRF token that is not the
// cookie, the API token or the signing key.
func TestSignInAndGetSessionReturnACSRFToken(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c, fromSignIn := csrfSession(t, h, operatorToken)

	resp, body := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c))
	assertStatus(t, "GET session", resp, body, http.StatusOK)
	fromGet := csrfTokenOf(resp, body)
	if fromGet == "" {
		t.Fatalf("GET /api/v1/session returns no CSRF token: %s", body)
	}
	assertNoStore(t, "GET session", resp)
	for _, tok := range []string{fromSignIn, fromGet} {
		if tok == c.Value || tok == operatorToken || tok == sessionKey || strings.Contains(tok, operatorToken) {
			t.Errorf("the CSRF token is a secret or the cookie: %.8s...", tok)
		}
	}

	// Without a session there is no token to hand out.
	resp, body = send(t, h, http.MethodGet, secureBase+sessionPath, nil)
	assertStatus(t, "GET without cookie", resp, body, http.StatusUnauthorized)
	if csrfTokenOf(resp, body) != "" {
		t.Error("a 401 carries a CSRF token")
	}
	// A failed sign-in gets none either.
	resp, body = signIn(t, h, secureBase, "unknown-token-0123456789abcdef0123456789")
	assertStatus(t, "failed sign-in", resp, body, http.StatusUnauthorized)
	if csrfTokenOf(resp, body) != "" {
		t.Error("a failed sign-in carries a CSRF token")
	}
}

// A cookie write without a valid X-Goldie-CSRF header is a 403 in the
// envelope; with the token from sign-in or from GET session it goes through.
func TestCookieWriteNeedsAValidCSRFToken(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c, tok := csrfSession(t, h, operatorToken)
	resp, body := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c))
	assertStatus(t, "GET session", resp, body, http.StatusOK)
	viaGet := csrfTokenOf(resp, body)

	flip := func(s string) string {
		b := []byte(s)
		b[len(b)/2] ^= 1
		return string(b)
	}
	for name, header := range map[string]func(*http.Request){
		"no header":          noMutation,
		"empty header":       withCSRF(""),
		"garbage":            withCSRF("not-a-token"),
		"changed token":      withCSRF(flip(tok)),
		"truncated token":    withCSRF(tok[:len(tok)-1]),
		"the session cookie": withCSRF(c.Value),
		"the API token":      withCSRF(operatorToken),
		"token plus space":   withCSRF(tok + " "),
	} {
		resp, body := postTrigger(t, h, withCookie(c), setSameOrigin, header)
		assertRefusedCSRF(t, name, resp, body)
	}

	want, _ := postTrigger(t, h, bearer(operatorToken))
	if want.StatusCode == http.StatusUnauthorized || want.StatusCode == http.StatusForbidden {
		t.Fatalf("POST /api/v1/sync with the operator bearer: status %d", want.StatusCode)
	}
	for name, token := range map[string]string{"sign-in token": tok, "GET session token": viaGet} {
		resp, body := postTrigger(t, h, withCookie(c), withCSRF(token), setSameOrigin)
		if resp.StatusCode != want.StatusCode {
			t.Errorf("%s: POST /api/v1/sync with cookie: status %d, want %d as with the bearer: %s", name, resp.StatusCode, want.StatusCode, body)
		}
	}
}

// The token stays valid for the life of its session, across the re-issue of
// the sliding cookie, and every unsafe method needs it.
func TestCSRFTokenSurvivesReissueAndCoversEveryUnsafeMethod(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c, tok := csrfSession(t, h, operatorToken)
	resp, body := send(t, h, http.MethodGet, secureBase+sessionSearch, withCookie(c))
	assertStatus(t, "GET", resp, body, http.StatusOK)
	if cs := resp.Cookies(); len(cs) == 1 {
		c = cs[0]
	}
	resp, body = postTrigger(t, h, withCookie(c), withCSRF(tok), setSameOrigin)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		t.Errorf("the sign-in token after a cookie re-issue: status %d: %s", resp.StatusCode, body)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		resp, body := send(t, h, method, secureBase+"/api/v1/nope", both(withCookie(c), setSameOrigin))
		assertRefusedCSRF(t, method+" without the token", resp, body)
	}
}

// A bearer request needs no CSRF token, and a cookie that rides along does
// not make it need one; a bad CSRF header on a bearer request is not read.
func TestBearerRequestsNeedNoCSRFToken(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c, tok := csrfSession(t, h, operatorToken)
	alone, body := postTrigger(t, h, bearer(operatorToken))
	if alone.StatusCode == http.StatusUnauthorized || alone.StatusCode == http.StatusForbidden {
		t.Fatalf("POST /api/v1/sync with the operator bearer: status %d: %s", alone.StatusCode, body)
	}
	for name, mutate := range map[string]func(*http.Request){
		"bearer and cookie":                both(bearer(operatorToken), withCookie(c)),
		"bearer and a wrong token":         both(bearer(operatorToken), withCSRF("not-a-token")),
		"bearer, cookie and a wrong token": both(bearer(operatorToken), withCookie(c), withCSRF("not-a-token")),
		"bearer and same-origin fetch":     both(bearer(operatorToken), setSameOrigin),
	} {
		resp, body := postTrigger(t, h, mutate)
		if resp.StatusCode != alone.StatusCode {
			t.Errorf("%s: status %d, want %d as with the bearer alone: %s", name, resp.StatusCode, alone.StatusCode, body)
		}
		assertNoCookie(t, name, resp)
	}
	// A bad bearer is not rescued by a cookie and a valid token.
	resp, body := postTrigger(t, h, bearer("unknown-token-0123456789abcdef0123456789"), withCookie(c), withCSRF(tok))
	assertStatus(t, "bad bearer, cookie and token", resp, body, http.StatusUnauthorized)
}

// A CSRF token alone authenticates nothing, and one from another session,
// another signing key or a signed-out browser is no key.
func TestACSRFTokenIsBoundToItsOwnSession(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	opCookie, opTok := csrfSession(t, h, operatorToken)
	_, readerTok := csrfSession(t, h, readerToken)

	other := sessionHandler(t, namedTokenLines(), otherKey, nil)
	_, foreignTok := csrfSession(t, other, operatorToken)

	for name, tok := range map[string]string{
		"a token from another session":    readerTok,
		"a token signed with another key": foreignTok,
	} {
		resp, body := postTrigger(t, h, withCookie(opCookie), withCSRF(tok), setSameOrigin)
		assertRefusedCSRF(t, name, resp, body)
	}

	// After sign-out the browser holds no cookie: the old token is nothing.
	resp, body := send(t, h, http.MethodDelete, secureBase+sessionPath, both(withCookie(opCookie), setSameOrigin))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("sign-out: status %d: %s", resp.StatusCode, body)
	}
	resp, body = postTrigger(t, h, withCSRF(opTok), setSameOrigin)
	assertStatus(t, "token after sign-out, no cookie", resp, body, http.StatusUnauthorized)
	resp, body = postTrigger(t, h, withCookie(&http.Cookie{Name: opCookie.Name, Value: ""}), withCSRF(opTok))
	assertStatus(t, "token with a cleared cookie", resp, body, http.StatusUnauthorized)

	// A token from the same sign-in cannot be used with a forged cookie.
	forged := &http.Cookie{Name: opCookie.Name, Value: opCookie.Value[:len(opCookie.Value)-2] + "xx"}
	resp, body = postTrigger(t, h, withCookie(forged), withCSRF(opTok))
	assertStatus(t, "token with a forged cookie", resp, body, http.StatusUnauthorized)
}

// The token does not lift a reader to an operator: the Role still decides.
func TestACSRFTokenDoesNotGrantARole(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c, tok := csrfSession(t, h, readerToken)
	resp, body := postTrigger(t, h, withCookie(c), withCSRF(tok), setSameOrigin)
	assertStatus(t, "reader cookie with its own token", resp, body, http.StatusForbidden)
	assertEnvelope(t, body, "forbidden")
}

// A request whose Origin or Sec-Fetch-Site is cross-site is rejected even
// with a valid token. A request with neither header (a non-browser client)
// or a same-origin one is not rejected on that ground.
func TestCookieWriteRejectsCrossSiteRequestsEvenWithAValidToken(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c, tok := csrfSession(t, h, operatorToken)
	for name, mutate := range map[string]func(*http.Request){
		"cross-site fetch metadata":          withFetchSite("cross-site"),
		"same-site fetch metadata":           withFetchSite("same-site"),
		"foreign origin":                     withOrigin("https://evil.example"),
		"null origin":                        withOrigin("null"),
		"other port origin":                  withOrigin("https://goldie.example.com:8443"),
		"http origin on https":               withOrigin("http://goldie.example.com"),
		"cross-site with same origin header": both(withFetchSite("cross-site"), withOrigin(secureBase)),
	} {
		resp, body := postTrigger(t, h, withCookie(c), withCSRF(tok), mutate)
		assertRefusedCSRF(t, name, resp, body)
	}

	want, _ := postTrigger(t, h, bearer(operatorToken))
	for name, mutate := range map[string]func(*http.Request){
		"no Origin and no fetch metadata": noMutation,
		"same-origin fetch metadata":      setSameOrigin,
		"none fetch metadata":             withFetchSite("none"),
		"same Origin":                     withOrigin(secureBase),
	} {
		resp, body := postTrigger(t, h, withCookie(c), withCSRF(tok), mutate)
		if resp.StatusCode != want.StatusCode {
			t.Errorf("%s: status %d, want %d: %s", name, resp.StatusCode, want.StatusCode, body)
		}
	}
}

// The token never reaches a log line.
func TestCSRFTokenIsNeverLogged(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := sessionHandler(t, namedTokenLines(), sessionKey, logger)
	c, tok := csrfSession(t, h, operatorToken)
	postTrigger(t, h, withCookie(c), withCSRF(tok), setSameOrigin)
	postTrigger(t, h, withCookie(c), withCSRF("not-a-token"), setSameOrigin)
	if strings.Contains(logs.String(), tok) {
		t.Errorf("a log line carries the CSRF token: %s", logs.String())
	}
}
