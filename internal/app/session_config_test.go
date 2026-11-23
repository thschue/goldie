package app

import (
	"net/http"
	"strings"
	"testing"
)

// The cookie's Secure flag and __Host- prefix follow the server's
// configuration, never the request's Host header: goldie terminates no TLS, so
// a proxy that rewrites Host to loopback must not weaken a production cookie.

// devSessionHandler is the public handler of the loopback dev server
// (server --dev, local mode), the only one that drops Secure and __Host-.
func devSessionHandler(t *testing.T, tokens, key string) http.Handler {
	t.Helper()
	cfg := sessionConfig(t, tokens, map[string]string{"GOLDIE_SESSION_KEY": key})
	parsed, err := apiTokens(cfg)
	if err != nil {
		t.Fatalf("apiTokens: %v", err)
	}
	src, err := serveSource(cfg)
	if err != nil {
		t.Fatalf("serveSource: %v", err)
	}
	h, err := newPublicSessions(true, cfg, parsed, src, nil, 0)
	if err != nil {
		t.Fatalf("newPublicSessions: %v", err)
	}
	return h
}

func TestProductionKeepsSecureAndThePrefixWhateverTheHostHeaderSays(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	for _, base := range []string{loopbackBase, "http://localhost:8080", "http://[::1]:8080"} {
		resp, body := signIn(t, h, base, operatorToken)
		assertStatus(t, base, resp, body, http.StatusOK)
		if c := theCookie(t, resp); !c.Secure || !strings.HasPrefix(c.Name, "__Host-") {
			t.Errorf("%s: cookie %+v, want Secure and the __Host- prefix on a production server", base, c)
		}
	}
}

func TestDevServerDropsSecureAndThePrefixOnPlainHTTPOnly(t *testing.T) {
	h := devSessionHandler(t, namedTokenLines(), sessionKey)
	resp, body := signIn(t, h, loopbackBase, operatorToken)
	assertStatus(t, "plain", resp, body, http.StatusOK)
	c := theCookie(t, resp)
	if c.Secure || strings.HasPrefix(c.Name, "__Host-") {
		t.Errorf("plain: cookie %+v, want no Secure and no __Host- prefix", c)
	}
	r, b := send(t, h, http.MethodGet, loopbackBase+sessionPath, withCookie(c))
	assertStatus(t, "plain GET", r, b, http.StatusOK)

	resp, body = signIn(t, h, secureBase, operatorToken)
	assertStatus(t, "tls", resp, body, http.StatusOK)
	if c := theCookie(t, resp); !c.Secure || !strings.HasPrefix(c.Name, "__Host-") {
		t.Errorf("tls: cookie %+v, want Secure and the __Host- prefix", c)
	}
}

// A session is bound to the token's value: giving the same name a new value
// ends its sessions, though the key is unchanged.
func TestRotatingATokenValueEndsItsSessions(t *testing.T) {
	old := sessionHandler(t, "ci-operator:operator:"+operatorToken, sessionKey, nil)
	resp, body := signIn(t, old, secureBase, operatorToken)
	assertStatus(t, "sign-in", resp, body, http.StatusOK)
	c := theCookie(t, resp)
	r, b := send(t, old, http.MethodGet, secureBase+sessionPath, withCookie(c))
	assertStatus(t, "before", r, b, http.StatusOK)

	rotated := sessionHandler(t, "ci-operator:operator:"+readerToken, sessionKey, nil)
	r, b = send(t, rotated, http.MethodGet, secureBase+sessionPath, withCookie(c))
	assertStatus(t, "after rotation", r, b, http.StatusUnauthorized)
}

// A re-issued session cookie must not be stored by a shared cache.
func TestReissuedSessionCookieIsNotCacheable(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	c := signedIn(t, h, secureBase, operatorToken)
	resp, body := send(t, h, http.MethodGet, secureBase+sessionSearch, withCookie(c))
	assertStatus(t, "cookie GET", resp, body, http.StatusOK)
	if got := resp.Header.Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", got)
	}
	if got := resp.Header.Get("Vary"); !strings.Contains(got, "Cookie") {
		t.Errorf("Vary = %q, want Cookie", got)
	}
}
