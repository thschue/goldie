package app

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/platform"
)

// Browser sessions (#488, slice 1 of #21): POST /api/v1/session exchanges a
// configured token for a signed, stateless session cookie; GET reads it back;
// DELETE clears it. The cookie authenticates GET and HEAD only, and a bearer
// token beats it. These tests drive the real public handler over HTTP.
//
// The issue does not fix how sign-in carries the token. signIn sends it as
// "Authorization: Bearer <token>" and as {"token": "<token>"} in the JSON
// body, so either reading passes; a rejected sign-in sends the bad value both
// ways.

const (
	sessionPath   = "/api/v1/session"
	sessionSearch = "/api/v1/search?q=kubernetes"
	sessionKey    = "session-key-0123456789abcdef0123456789abcdef"
	otherKey      = "other-key-0123456789abcdef0123456789abcdef01"

	// Bases the tests call the handler as. A https URL makes the request
	// TLS; a plain http one on a loopback host is the dev listener.
	secureBase   = "https://goldie.example.com"
	plainRemote  = "http://goldie.example.com"
	loopbackBase = "http://127.0.0.1:8080"
)

// sessionConfig is LoadConfig over the named tokens and the given extra
// environment, so the tests need only the documented variables.
func sessionConfig(t *testing.T, tokens string, extra map[string]string) platform.Config {
	t.Helper()
	env := map[string]string{
		"GOLDIE_API_TOKENS":  tokens,
		"GOLDIE_CONTENT_DIR": testContent,
	}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := platform.LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.RateLimit = roomyRateLimit
	return cfg
}

// sessionHandler is the public handler with a signing key (empty: none set).
func sessionHandler(t *testing.T, tokens, key string, logger *slog.Logger) http.Handler {
	t.Helper()
	extra := map[string]string{}
	if key != "" {
		extra["GOLDIE_SESSION_KEY"] = key
	}
	h, err := PublicHandler(sessionConfig(t, tokens, extra), logger)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	return h
}

// serve runs req through h in process and returns the response with its body
// read and its request set, so it can be checked against the spec.
func serve(t *testing.T, h http.Handler, req *http.Request) (*http.Response, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Request = req
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

// send serves one request in process. Every response but a HEAD one is
// checked against the spec.
func send(t *testing.T, h http.Handler, method, url string, mutate func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(""))
	if mutate != nil {
		mutate(req)
	}
	resp, body := serve(t, h, req)
	if method != http.MethodHead {
		contracttest.Conforms(t, resp, body)
	}
	return resp, body
}

// signIn posts token to the session endpoint of base.
func signIn(t *testing.T, h http.Handler, base, token string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, base+sessionPath, strings.NewReader(mustJSON(t, map[string]string{"token": token})))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, body := serve(t, h, req)
	contracttest.Conforms(t, resp, body)
	return resp, body
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// signedIn signs token in and returns the session cookie, failing the test
// when the sign-in does not succeed.
func signedIn(t *testing.T, h http.Handler, base, token string) *http.Cookie {
	t.Helper()
	resp, body := signIn(t, h, base, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: status %d, want 200: %s", resp.StatusCode, body)
	}
	return theCookie(t, resp)
}

// theCookie is the one cookie a response sets.
func theCookie(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	cs := resp.Cookies()
	if len(cs) != 1 {
		t.Fatalf("response sets %d cookies, want exactly 1: %v", len(cs), resp.Header.Values("Set-Cookie"))
	}
	return cs[0]
}

// forgedCookie returns c with its signature broken: same name and length, but
// the decoded MAC differs. Overwriting trailing base64 characters is not safe:
// the last character of a 32-byte MAC carries only 4 significant bits, so a
// "forged" tail can decode to the real MAC (about 1 in 1024 sign-ins).
func forgedCookie(c *http.Cookie) *http.Cookie {
	return &http.Cookie{Name: c.Name, Value: forgedValue(c.Value)}
}

// forgedValue flips the lowest bit of the first decoded byte of the signature,
// the part after the last colon, and re-encodes it.
func forgedValue(value string) string {
	i := strings.LastIndex(value, ":")
	mac, err := base64.RawURLEncoding.DecodeString(value[i+1:])
	if err != nil || len(mac) == 0 {
		panic("forgedValue: cookie value has no base64url signature: " + value)
	}
	mac[0] ^= 1
	return value[:i+1] + base64.RawURLEncoding.EncodeToString(mac)
}

// The old trick (overwrite the last two characters with "xx") collides with the
// real signature when the tail already decodes the same; forgedValue never does.
func TestForgedCookieAlwaysDiffersAfterDecoding(t *testing.T) {
	mac := bytes.Repeat([]byte{0x5a}, 32)
	enc := base64.RawURLEncoding.EncodeToString(mac)
	crafted := "ci-operator:1:2:" + enc[:len(enc)-2] + "xw" // "xw" decodes like "xx"
	decode := func(v string) []byte {
		b, err := base64.RawURLEncoding.DecodeString(v[strings.LastIndex(v, ":")+1:])
		if err != nil {
			t.Fatalf("decode %q: %v", v, err)
		}
		return b
	}
	old := crafted[:len(crafted)-2] + "xx"
	if !bytes.Equal(decode(old), decode(crafted)) {
		t.Fatalf("precondition: the old trick should collide for the crafted input")
	}
	for _, v := range []string{crafted, "n:1:2:" + enc, "n:1:2:" + strings.Repeat("A", 43), "n:1:2:" + strings.Repeat("_", 43)} {
		f := forgedValue(v)
		if len(f) != len(v) || bytes.Equal(decode(f), decode(v)) {
			t.Errorf("forgedValue(%q) = %q: same length and a different MAC wanted", v, f)
		}
	}
}

func withCookie(c *http.Cookie) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value}) }
}

func both(fs ...func(*http.Request)) func(*http.Request) {
	return func(r *http.Request) {
		for _, f := range fs {
			f(r)
		}
	}
}

func setSameOrigin(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") }

type sessionBody struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

func readSession(t *testing.T, body []byte) sessionBody {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, body)
	}
	if _, ok := raw["swagger_ui"].(bool); !ok || len(raw) != 3 {
		t.Errorf("session body has keys %v, want exactly name, role and a boolean swagger_ui", raw)
	}
	var s sessionBody
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// assertNoStore checks the response forbids caching.
func assertNoStore(t *testing.T, what string, resp *http.Response) {
	t.Helper()
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(strings.ToLower(got), "no-store") {
		t.Errorf("%s: Cache-Control %q, want no-store", what, got)
	}
}

func assertNoCookie(t *testing.T, what string, resp *http.Response) {
	t.Helper()
	if got := resp.Header.Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("%s: sets a cookie: %v", what, got)
	}
}

// assertNoLeak checks no part of the response carries a secret.
func assertNoLeak(t *testing.T, what string, resp *http.Response, body []byte, secrets ...string) {
	t.Helper()
	var head bytes.Buffer
	_ = resp.Header.Write(&head)
	for _, s := range secrets {
		if s == "" {
			continue
		}
		forms := []string{s, base64.StdEncoding.EncodeToString([]byte(s)), base64.RawURLEncoding.EncodeToString([]byte(s)), hex.EncodeToString([]byte(s)), sha256Hex(s)}
		for _, form := range forms {
			if strings.Contains(string(body), form) || strings.Contains(head.String(), form) {
				t.Errorf("%s: a secret appears in the response", what)
			}
		}
	}
}

func assertStatus(t *testing.T, what string, resp *http.Response, body []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Errorf("%s: status %d, want %d: %s", what, resp.StatusCode, want, body)
	}
}

// Sign-in over TLS: 200 {name, role}, one cookie with every hardening
// attribute and the __Host- prefix, never the token in it, no-store.
func TestSignInReturnsNameAndRoleAndAHardenedHostCookie(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	for token, want := range map[string]sessionBody{
		operatorToken: {"ci-operator", "operator"},
		readerToken:   {"ci-reader", "reader"},
		hashedSecret:  {"ci-hashed", "reader"},
	} {
		resp, body := signIn(t, h, secureBase, token)
		assertStatus(t, want.Name, resp, body, http.StatusOK)
		if got := readSession(t, body); got != want {
			t.Errorf("sign-in as %s: body %+v, want %+v", want.Name, got, want)
		}
		assertNoStore(t, "sign-in "+want.Name, resp)
		c := theCookie(t, resp)
		if !strings.HasPrefix(c.Name, "__Host-") {
			t.Errorf("cookie name %q lacks the __Host- prefix", c.Name)
		}
		if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
			t.Errorf("cookie %+v: want Secure, HttpOnly, SameSite=Strict, Path=/ and no Domain", c)
		}
		if c.Value == "" {
			t.Error("cookie has no value")
		}
		assertNoLeak(t, "sign-in "+want.Name, resp, body, token)
	}
}

// On the plain-http loopback listener of a dev server a browser would drop a
// Secure or __Host- cookie, so both go; the rest of the hardening stays.
func TestSignInOnPlainHTTPLoopbackDropsSecureAndThePrefix(t *testing.T) {
	h := devSessionHandler(t, namedTokenLines(), sessionKey)
	for _, base := range []string{loopbackBase, "http://localhost:8080", "http://[::1]:8080"} {
		resp, body := signIn(t, h, base, operatorToken)
		assertStatus(t, base, resp, body, http.StatusOK)
		c := theCookie(t, resp)
		if c.Secure || strings.HasPrefix(c.Name, "__Host-") {
			t.Errorf("%s: cookie %+v, want no Secure and no __Host- prefix", base, c)
		}
		if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
			t.Errorf("%s: cookie %+v, want HttpOnly, SameSite=Strict, Path=/", base, c)
		}
		// The dev cookie works on the same listener.
		r, b := send(t, h, http.MethodGet, base+sessionPath, withCookie(c))
		assertStatus(t, base+" GET session", r, b, http.StatusOK)
	}
}

// Only loopback is exempt: plain http anywhere else still gets Secure and the
// prefix, so a misconfigured proxy fails closed instead of weakening the cookie.
func TestSignInOverPlainHTTPOffLoopbackKeepsSecureAndThePrefix(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	resp, body := signIn(t, h, plainRemote, operatorToken)
	assertStatus(t, "sign-in", resp, body, http.StatusOK)
	if c := theCookie(t, resp); !c.Secure || !strings.HasPrefix(c.Name, "__Host-") {
		t.Errorf("cookie %+v, want Secure and the __Host- prefix off loopback", c)
	}
}

// A sign-in that is not a configured token is a 401 in the envelope with no
// cookie and no echo of what was presented.
func TestSignInRejectsWhatIsNotAConfiguredToken(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	for name, token := range map[string]string{
		"no token":         "",
		"unknown token":    "unknown-token-0123456789abcdef0123456789",
		"digest as token":  sha256Hex(hashedSecret),
		"hashed entry":     "sha256:" + sha256Hex(hashedSecret),
		"token name":       "ci-operator",
		"whole line":       "ci-reader:reader:" + readerToken,
		"truncated token":  operatorToken[:len(operatorToken)-1],
		"token plus space": operatorToken + " ",
		"upper-cased":      strings.ToUpper(operatorToken),
	} {
		resp, body := signIn(t, h, secureBase, token)
		assertStatus(t, name, resp, body, http.StatusUnauthorized)
		assertNoCookie(t, name, resp)
		assertNoStore(t, name, resp)
		assertEnvelope(t, body, "unauthorized")
		assertNoLeak(t, name, resp, body, token)
	}
}

// A malformed body is refused, never turned into a session.
func TestSignInRefusesAMalformedBody(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	for name, body := range map[string]string{
		"not json":       "{",
		"array":          "[]",
		"token a number": `{"token": 7}`,
		"token an array": `{"token": ["` + operatorToken + `"]}`,
	} {
		resp, _ := send(t, h, http.MethodPost, secureBase+sessionPath, func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		})
		if resp.StatusCode < 400 || resp.StatusCode > 499 {
			t.Errorf("%s: status %d, want a 4xx", name, resp.StatusCode)
		}
		assertNoCookie(t, name, resp)
	}
}

// GET /api/v1/session reads the caller back from a valid cookie; anything else
// is a 401. Every session response forbids caching.
func TestSessionReportsTheCallerForAValidCookieOtherwise401(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c := signedIn(t, h, secureBase, operatorToken)

	resp, body := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c))
	assertStatus(t, "valid cookie", resp, body, http.StatusOK)
	if got, want := readSession(t, body), (sessionBody{"ci-operator", "operator"}); got != want {
		t.Errorf("body %+v, want %+v", got, want)
	}
	assertNoStore(t, "GET with cookie", resp)

	resp, body = send(t, h, http.MethodGet, secureBase+sessionPath, nil)
	assertStatus(t, "no cookie", resp, body, http.StatusUnauthorized)
	assertNoStore(t, "GET without cookie", resp)
	assertEnvelope(t, body, "unauthorized")
}

// A cookie that was changed, cut short or signed with another key is no
// session, on the session endpoint and on the API.
func TestTamperedTruncatedOrWrongKeyCookiesAreRejected(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c := signedIn(t, h, secureBase, operatorToken)
	other := sessionHandler(t, namedTokenLines(), otherKey, nil)
	foreign := signedIn(t, other, secureBase, operatorToken)
	if foreign.Name != c.Name {
		t.Fatalf("cookie names differ between handlers: %q and %q", foreign.Name, c.Name)
	}

	flip := func(s string, i int) string {
		b := []byte(s)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return string(b)
	}
	bad := map[string]string{
		"first character changed":  flip(c.Value, 0),
		"middle character changed": flip(c.Value, len(c.Value)/2),
		"truncated by one":         c.Value[:len(c.Value)-1],
		"truncated by half":        c.Value[:len(c.Value)/2],
		"empty":                    "",
		"garbage":                  "not-a-session",
		"signed with another key":  foreign.Value,
		"the token":                operatorToken,
		"the token name":           "ci-operator",
	}
	for name, value := range bad {
		forged := &http.Cookie{Name: c.Name, Value: value}
		for _, target := range []string{sessionPath, sessionSearch} {
			resp, body := send(t, h, http.MethodGet, secureBase+target, withCookie(forged))
			assertStatus(t, name+" on "+target, resp, body, http.StatusUnauthorized)
			assertNoCookie(t, name+" on "+target, resp)
		}
	}
}

// The cookie signs in for reads: GET and HEAD on the existing API work with
// it; every unsafe method with it and no bearer is a 403 (a CSRF token comes
// in a later slice), and nothing is re-issued on the refusal.
func TestCookieAuthenticatesOnlyGetAndHead(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c := signedIn(t, h, secureBase, operatorToken)

	for _, path := range []string{sessionSearch, "/api/v1/articles/agent-loops"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			resp, body := send(t, h, method, secureBase+path, withCookie(c))
			assertStatus(t, method+" "+path, resp, body, http.StatusOK)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, path := range []string{sessionSearch, "/api/v1/sync", "/api/v1/nope"} {
			resp, body := send(t, h, method, secureBase+path, withCookie(c))
			assertStatus(t, method+" "+path+" with cookie", resp, body, http.StatusForbidden)
			assertNoCookie(t, method+" "+path, resp)
		}
	}
}

// A cookie and a bearer together: the bearer decides, role and refusals
// included, and nothing is re-issued. An unsafe method is allowed by the
// bearer exactly as it is without the cookie.
func TestABearerBeatsACookieAndNothingIsReissued(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	op := signedIn(t, h, secureBase, operatorToken)

	// The reader bearer's role applies, not the operator cookie's.
	resp, body := send(t, h, http.MethodGet, secureBase+sessionSearch+"&drafts=true", both(withCookie(op), bearer(readerToken)))
	assertStatus(t, "reader bearer, operator cookie, drafts", resp, body, http.StatusForbidden)
	assertNoCookie(t, "reader bearer", resp)

	// A bad bearer is not rescued by a good cookie.
	resp, body = send(t, h, http.MethodGet, secureBase+sessionSearch, both(withCookie(op), bearer("unknown-token-0123456789abcdef0123456789")))
	assertStatus(t, "bad bearer, good cookie", resp, body, http.StatusUnauthorized)
	assertNoCookie(t, "bad bearer", resp)

	// A good bearer on a read is answered and nothing is set.
	resp, body = send(t, h, http.MethodGet, secureBase+sessionSearch, both(withCookie(op), bearer(readerToken)))
	assertStatus(t, "good bearer, good cookie", resp, body, http.StatusOK)
	assertNoCookie(t, "good bearer", resp)

	// An unsafe method is the bearer's call: the same answer with or without the cookie.
	alone, _ := send(t, h, http.MethodPost, secureBase+"/api/v1/sync", bearer(operatorToken))
	if alone.StatusCode == http.StatusUnauthorized || alone.StatusCode == http.StatusForbidden {
		t.Fatalf("POST /api/v1/sync with the operator bearer: status %d", alone.StatusCode)
	}
	with, _ := send(t, h, http.MethodPost, secureBase+"/api/v1/sync", both(withCookie(op), bearer(operatorToken)))
	if with.StatusCode != alone.StatusCode {
		t.Errorf("POST /api/v1/sync with bearer and cookie: status %d, want %d, as without the cookie", with.StatusCode, alone.StatusCode)
	}
	assertNoCookie(t, "bearer POST", with)
}

// The cookie carries the token's name, never its Role or the token: a role
// change in config is seen on the next request, and a token removed from
// config ends the session.
func TestSessionRoleIsReadLiveAndARemovedTokenEndsTheSession(t *testing.T) {
	asOperator := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c := signedIn(t, asOperator, secureBase, operatorToken)

	demoted := strings.Replace(namedTokenLines(), "ci-operator:operator:", "ci-operator:reader:", 1)
	d := sessionHandler(t, demoted, sessionKey, nil)
	resp, body := send(t, d, http.MethodGet, secureBase+sessionPath, withCookie(c))
	assertStatus(t, "demoted", resp, body, http.StatusOK)
	if got := readSession(t, body); got != (sessionBody{"ci-operator", "reader"}) {
		t.Errorf("after the demotion the session is %+v, want ci-operator as reader", got)
	}
	resp, body = send(t, d, http.MethodGet, secureBase+sessionSearch+"&drafts=true", withCookie(c))
	assertStatus(t, "demoted, drafts", resp, body, http.StatusForbidden)
	resp, body = send(t, asOperator, http.MethodGet, secureBase+sessionSearch+"&drafts=true", withCookie(c))
	assertStatus(t, "operator, drafts", resp, body, http.StatusOK)

	removed := sessionHandler(t, "ci-reader:reader:"+readerToken, sessionKey, nil)
	for _, target := range []string{sessionPath, sessionSearch} {
		resp, body = send(t, removed, http.MethodGet, secureBase+target, withCookie(c))
		assertStatus(t, "removed token on "+target, resp, body, http.StatusUnauthorized)
	}
}

// A path the spec does not know stays behind the same gate: no valid
// credential is a 401 (not a 404 that reveals nothing exists), and the cookie
// is no key to an unsafe method there either.
func TestACookieOnAnUnknownPathStillFailsClosed(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c := signedIn(t, h, secureBase, operatorToken)
	forged := forgedCookie(c)
	for _, path := range []string{"/api/v1/nope", "/api/", "/api/v1/session/extra", "/api/v2/search"} {
		resp, body := send(t, h, http.MethodGet, secureBase+path, withCookie(forged))
		assertStatus(t, "forged cookie on "+path, resp, body, http.StatusUnauthorized)
		resp, body = send(t, h, http.MethodGet, secureBase+path, nil)
		assertStatus(t, "no credential on "+path, resp, body, http.StatusUnauthorized)
		resp, body = send(t, h, http.MethodPost, secureBase+path, withCookie(c))
		assertStatus(t, "POST with cookie on "+path, resp, body, http.StatusForbidden)
	}
}

// DELETE clears the cookie: the same name and path, no value, already expired.
func TestSignOutClearsTheCookie(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	for _, base := range []string{secureBase, loopbackBase} {
		c := signedIn(t, h, base, operatorToken)
		resp, body := send(t, h, http.MethodDelete, base+sessionPath, both(withCookie(c), setSameOrigin))
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s: DELETE status %d, want 200 or 204: %s", base, resp.StatusCode, body)
		}
		assertNoStore(t, base+" DELETE", resp)
		cleared := theCookie(t, resp)
		if cleared.Name != c.Name || cleared.Path != "/" || cleared.Value != "" {
			t.Errorf("%s: cleared cookie %+v, want the session cookie's name, Path=/ and no value", base, cleared)
		}
		expired := cleared.MaxAge < 0 || (cleared.MaxAge == 0 && !cleared.Expires.IsZero() && cleared.Expires.Before(time.Now()))
		if !expired {
			t.Errorf("%s: cleared cookie %+v is not expired (want Max-Age<0 or a past Expires)", base, cleared)
		}
		if base == secureBase && (!cleared.Secure || !cleared.HttpOnly) {
			t.Errorf("clearing cookie %+v must keep Secure and HttpOnly so the browser matches it", cleared)
		}
	}
}

// A cross-origin DELETE is refused (http.CrossOriginProtection) and clears
// nothing; a same-origin one is not.
func TestSignOutRejectsACrossOriginRequest(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c := signedIn(t, h, secureBase, operatorToken)
	for name, mutate := range map[string]func(*http.Request){
		"cross-site fetch metadata": func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"same-site fetch metadata":  func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") },
		"foreign origin":            func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
		"null origin":               func(r *http.Request) { r.Header.Set("Origin", "null") },
	} {
		resp, body := send(t, h, http.MethodDelete, secureBase+sessionPath, both(withCookie(c), mutate))
		assertStatus(t, name, resp, body, http.StatusForbidden)
		assertNoCookie(t, name, resp)
	}
	resp, body := send(t, h, http.MethodDelete, secureBase+sessionPath, both(withCookie(c), func(r *http.Request) {
		r.Header.Set("Origin", secureBase)
	}))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Errorf("same-origin DELETE: status %d, want 200 or 204: %s", resp.StatusCode, body)
	}
}

// The signing key is configuration, never derived from tokens: with no key set
// each process picks its own, so a cookie from one server means nothing to
// another with the same tokens; the same key, from the variable or its _FILE,
// makes the sessions portable.
func TestSessionKeyComesFromConfigurationOrIsRandomPerProcess(t *testing.T) {
	first := sessionHandler(t, namedTokenLines(), "", nil)
	second := sessionHandler(t, namedTokenLines(), "", nil)
	c := signedIn(t, first, secureBase, operatorToken)
	if resp, body := send(t, first, http.MethodGet, secureBase+sessionPath, withCookie(c)); resp.StatusCode != http.StatusOK {
		t.Fatalf("a server rejects its own cookie: %d %s", resp.StatusCode, body)
	}
	if resp, body := send(t, second, http.MethodGet, secureBase+sessionPath, withCookie(c)); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("another process with the same tokens and no key accepts the cookie: %d %s", resp.StatusCode, body)
	}

	keyFile := filepath.Join(t.TempDir(), "session-key")
	if err := os.WriteFile(keyFile, []byte(sessionKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := PublicHandler(sessionConfig(t, namedTokenLines(), map[string]string{"GOLDIE_SESSION_KEY_FILE": keyFile}), nil)
	if err != nil {
		t.Fatal(err)
	}
	fromEnv := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c = signedIn(t, fromEnv, secureBase, operatorToken)
	if resp, body := send(t, fromFile, http.MethodGet, secureBase+sessionPath, withCookie(c)); resp.StatusCode != http.StatusOK {
		t.Errorf("GOLDIE_SESSION_KEY_FILE with the same key does not accept GOLDIE_SESSION_KEY's cookie: %d %s", resp.StatusCode, body)
	}
}

// Neither the token, nor its digest, nor the session cookie reaches a log line.
func TestSessionsLogNeitherTheTokenNorTheCookie(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := sessionHandler(t, namedTokenLines(), sessionKey, logger)
	const unknown = "unknown-token-0123456789abcdef0123456789"
	signIn(t, h, secureBase, unknown)
	c := signedIn(t, h, secureBase, operatorToken)
	send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c))
	send(t, h, http.MethodGet, secureBase+sessionSearch, withCookie(c))
	send(t, h, http.MethodPost, secureBase+"/api/v1/sync", withCookie(c))
	send(t, h, http.MethodDelete, secureBase+sessionPath, both(withCookie(c), setSameOrigin))
	for _, secret := range []string{operatorToken, unknown, sha256Hex(operatorToken), c.Value, sessionKey} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("a log line carries a secret (%.8s...): %s", secret, logs.String())
		}
	}
}

// Lifetime, on a fake clock: a session in use stays alive past one TTL
// because each use re-issues the cookie (sliding), an idle one expires, and
// even a session used without a break ends at the absolute cap.
func TestSessionLifetimeSlidesWithUseAndEndsAtTheAbsoluteCap(t *testing.T) {
	const step = 5 * time.Minute

	t.Run("sliding: use keeps it alive", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
			c := signedIn(t, h, secureBase, operatorToken)
			reissued := 0
			for elapsed := time.Duration(0); elapsed < 3*time.Hour; elapsed += step {
				time.Sleep(step)
				resp, body := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c))
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("after %s of regular use: status %d, want 200: %s", elapsed+step, resp.StatusCode, body)
				}
				if len(resp.Cookies()) > 0 {
					reissued++
					c = theCookie(t, resp)
					if c.MaxAge <= 0 && c.Expires.IsZero() {
						t.Fatalf("the re-issued cookie %+v has no lifetime", c)
					}
				}
			}
			if reissued == 0 {
				t.Error("a session in use was never re-issued, so it cannot slide")
			}
		})
	})

	t.Run("idle: it expires", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
			c := signedIn(t, h, secureBase, operatorToken)
			time.Sleep(step)
			if resp, body := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c)); resp.StatusCode != http.StatusOK {
				t.Fatalf("a session %s old: status %d, want 200: %s", step, resp.StatusCode, body)
			}
			time.Sleep(400 * 24 * time.Hour)
			resp, body := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c))
			assertStatus(t, "idle for 400 days", resp, body, http.StatusUnauthorized)
			assertNoCookie(t, "expired", resp)
			resp, body = send(t, h, http.MethodGet, secureBase+sessionSearch, withCookie(c))
			assertStatus(t, "expired on the API", resp, body, http.StatusUnauthorized)
		})
	})

	t.Run("absolute cap: constant use does not outlive it", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
			c := signedIn(t, h, secureBase, operatorToken)
			const limit = 400 * 24 * time.Hour
			ended := time.Duration(0)
			for elapsed := time.Duration(0); elapsed < limit; elapsed += step {
				time.Sleep(step)
				resp, _ := send(t, h, http.MethodHead, secureBase+sessionSearch, withCookie(c))
				if resp.StatusCode == http.StatusUnauthorized {
					ended = elapsed + step
					break
				}
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("after %s: status %d, want 200 or the cap's 401", elapsed+step, resp.StatusCode)
				}
				if len(resp.Cookies()) > 0 {
					c = theCookie(t, resp)
				}
			}
			if ended == 0 {
				t.Fatalf("a session used every %s was still alive after %s: there is no absolute cap", step, limit)
			}
			// Signing in again is a fresh session.
			c = signedIn(t, h, secureBase, operatorToken)
			if resp, body := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(c)); resp.StatusCode != http.StatusOK {
				t.Errorf("a new sign-in after the cap: status %d: %s", resp.StatusCode, body)
			}
		})
	})
}

// The session says whether the server serves Swagger UI (#802), so the web UI
// links to it only when it is there. POST and GET agree, and
// GOLDIE_SWAGGER_UI_ENABLED=false turns it off.
func TestSessionReportsWhetherSwaggerUIIsServed(t *testing.T) {
	for name, c := range map[string]struct {
		extra map[string]string
		want  bool
	}{
		"on by default": {nil, true},
		"switched off":  {map[string]string{platform.EnvSwaggerUIEnabled: "false"}, false},
	} {
		extra := map[string]string{"GOLDIE_SESSION_KEY": sessionKey}
		for k, v := range c.extra {
			extra[k] = v
		}
		h, err := PublicHandler(sessionConfig(t, namedTokenLines(), extra), nil)
		if err != nil {
			t.Fatalf("PublicHandler: %v", err)
		}
		resp, body := signIn(t, h, secureBase, readerToken)
		assertStatus(t, name+": sign-in", resp, body, http.StatusOK)
		if got := swaggerUIOf(t, body); got != c.want {
			t.Errorf("%s: POST session swagger_ui = %v, want %v: %s", name, got, c.want, body)
		}
		resp, body = send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(theCookie(t, resp)))
		assertStatus(t, name+": GET session", resp, body, http.StatusOK)
		if got := swaggerUIOf(t, body); got != c.want {
			t.Errorf("%s: GET session swagger_ui = %v, want %v: %s", name, got, c.want, body)
		}
	}
}

// swaggerUIOf is the required boolean swagger_ui of a session body.
func swaggerUIOf(t *testing.T, body []byte) bool {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, body)
	}
	v, ok := raw["swagger_ui"].(bool)
	if !ok {
		t.Fatalf("session body has no boolean swagger_ui: %s", body)
	}
	return v
}

// The session carries the Grafana and trace links of the activity view (#807)
// for the operator role only: both PostSession and GetSession fill
// grafana_url and trace_url_template from GOLDIE_GRAFANA_URL and
// GOLDIE_TRACE_URL_TEMPLATE, omit them for a reader, and omit each that is
// unset.
const (
	linksGrafana = "https://grafana.example.com/d/goldie?orgId=1"
	linksTrace   = "https://tempo.example.com/trace/{trace_id}"
)

// sessionKeys is the decoded body of a session response.
func sessionKeys(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, body)
	}
	return raw
}

func TestSessionCarriesObservabilityLinksForTheOperatorOnly(t *testing.T) {
	h, err := PublicHandler(sessionConfig(t, namedTokenLines(), map[string]string{
		"GOLDIE_SESSION_KEY":        sessionKey,
		"GOLDIE_GRAFANA_URL":        linksGrafana,
		"GOLDIE_TRACE_URL_TEMPLATE": linksTrace,
	}), nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	for _, c := range []struct {
		who, token string
		want       bool
	}{{"operator", operatorToken, true}, {"reader", readerToken, false}} {
		resp, post := signIn(t, h, secureBase, c.token)
		assertStatus(t, c.who+": sign-in", resp, post, http.StatusOK)
		resp, get := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(theCookie(t, resp)))
		assertStatus(t, c.who+": GET session", resp, get, http.StatusOK)
		for op, body := range map[string][]byte{"POST": post, "GET": get} {
			raw := sessionKeys(t, body)
			g, hasG := raw["grafana_url"]
			tr, hasT := raw["trace_url_template"]
			if c.want {
				if g != linksGrafana || tr != linksTrace {
					t.Errorf("%s %s session: grafana_url=%v trace_url_template=%v, want both as configured: %s", c.who, op, g, tr, body)
				}
			} else if hasG || hasT {
				t.Errorf("%s %s session carries a link (grafana_url %v, trace_url_template %v), want neither: %s", c.who, op, hasG, hasT, body)
			}
		}
	}
}

// Each link is omitted, not empty, when its setting is unset; the other is
// still sent. With neither set the body is the three-key session.
func TestSessionOmitsAnUnsetObservabilityLink(t *testing.T) {
	for name, c := range map[string]struct {
		extra        map[string]string
		wantG, wantT string
	}{
		"neither":      {nil, "", ""},
		"grafana only": {map[string]string{"GOLDIE_GRAFANA_URL": linksGrafana}, linksGrafana, ""},
		"trace only":   {map[string]string{"GOLDIE_TRACE_URL_TEMPLATE": linksTrace}, "", linksTrace},
	} {
		extra := map[string]string{"GOLDIE_SESSION_KEY": sessionKey}
		for k, v := range c.extra {
			extra[k] = v
		}
		h, err := PublicHandler(sessionConfig(t, namedTokenLines(), extra), nil)
		if err != nil {
			t.Fatalf("PublicHandler: %v", err)
		}
		resp, post := signIn(t, h, secureBase, operatorToken)
		assertStatus(t, name+": sign-in", resp, post, http.StatusOK)
		resp, get := send(t, h, http.MethodGet, secureBase+sessionPath, withCookie(theCookie(t, resp)))
		assertStatus(t, name+": GET session", resp, get, http.StatusOK)
		for op, body := range map[string][]byte{"POST": post, "GET": get} {
			raw := sessionKeys(t, body)
			for key, want := range map[string]string{"grafana_url": c.wantG, "trace_url_template": c.wantT} {
				got, has := raw[key]
				switch {
				case want == "" && has:
					t.Errorf("%s %s: %s = %v, want it omitted: %s", name, op, key, got, body)
				case want != "" && got != want:
					t.Errorf("%s %s: %s = %v, want %q: %s", name, op, key, got, want, body)
				}
			}
		}
	}
}

// The contract change is additive: both properties are in the Session schema
// of the reference as optional strings, and name, role and swagger_ui stay
// required.
func TestSessionLinksAreOptionalStringsInTheAPIReference(t *testing.T) {
	b, err := os.ReadFile("../../docs/reference/api.md")
	if err != nil {
		t.Fatal(err)
	}
	ref := string(b)
	for _, row := range []string{"| `grafana_url` | `string` | no | ", "| `trace_url_template` | `string` | no | "} {
		if !strings.Contains(ref, row) {
			t.Errorf("docs/reference/api.md Session has no row %q (run: ./run regen)", row)
		}
	}
	for _, row := range []string{"| `name` | `string` | yes | ", "| `role` | `string` | yes | ", "| `swagger_ui` | `boolean` | yes | "} {
		if !strings.Contains(ref, row) {
			t.Errorf("docs/reference/api.md Session lost the required row %q", row)
		}
	}
}

// The session providers (#645, slice 4 of #25): an unauthenticated
// GET /api/v1/session/providers tells the UI whether to offer GitHub sign-in.
// The body is {"github": true|false} and nothing else, never a URL, a client
// id or an org name, and it is never cached.
const (
	providersPath = "/api/v1/session/providers"

	providersClientID = "Iv1.providersclientid0123"
	providersSecret   = "providers-oauth-secret-0123456789abcdef0123456789"
	providersGitHub   = "https://ghe.providers.example.com"
	providersOrg      = "providers-secret-org"
	providersPublic   = "https://goldie.providers.example.com"
)

// providersGitHubEnv is a complete GitHub sign-in configuration.
func providersGitHubEnv() map[string]string {
	return map[string]string{
		platform.EnvGitHubClientID:     providersClientID,
		platform.EnvGitHubClientSecret: providersSecret,
		platform.EnvGitHubURL:          providersGitHub,
		platform.EnvGitHubOrgs:         providersOrg,
		"GOLDIE_PUBLIC_URL":            providersPublic,
		"GOLDIE_SESSION_KEY":           sessionKey,
	}
}

func TestSessionProvidersReportsWhetherGitHubSignInIsConfigured(t *testing.T) {
	for name, c := range map[string]struct {
		extra map[string]string
		want  bool
	}{
		"configured":     {providersGitHubEnv(), true},
		"not configured": {map[string]string{"GOLDIE_SESSION_KEY": sessionKey}, false},
	} {
		t.Run(name, func(t *testing.T) {
			h, err := PublicHandler(sessionConfig(t, namedTokenLines(), c.extra), nil)
			if err != nil {
				t.Fatalf("PublicHandler: %v", err)
			}
			// No token and no cookie.
			resp, body := send(t, h, http.MethodGet, secureBase+providersPath, nil)
			assertStatus(t, "GET providers", resp, body, http.StatusOK)
			assertNoStore(t, "GET providers", resp)
			assertNoCookie(t, "GET providers", resp)
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("body is not JSON: %v: %s", err, body)
			}
			if len(got) != 1 || got["github"] != c.want {
				t.Errorf("body = %s, want exactly {\"github\": %v}", body, c.want)
			}
			assertNoLeak(t, "GET providers", resp, body,
				providersClientID, providersSecret, providersGitHub, providersOrg, providersPublic,
				"ghe.providers.example.com", readerToken, operatorToken)
		})
	}
}

// Like sign-in, the providers need no token; any other method is the 405
// envelope listing GET.
func TestSessionProvidersOtherMethodsAreTheEnvelope405(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		resp, body := send(t, h, method, secureBase+providersPath, nil)
		assertStatus(t, method+" providers", resp, body, http.StatusMethodNotAllowed)
		if code, _ := errorHint(t, body); code != "method_not_allowed" {
			t.Errorf("%s providers: code %q, want method_not_allowed: %s", method, code, body)
		}
		if got := resp.Header.Get("Allow"); !strings.Contains(got, "GET") {
			t.Errorf("%s providers: Allow %q, want GET listed", method, got)
		}
	}
}
