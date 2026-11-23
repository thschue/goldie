package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/thschue/goldie/internal/contract/contracttest"
)

// Rate-limited sign-in (#489, slice 2 of #21): after N failed
// POST /api/v1/session calls from one remote host within the window, the next
// call is 429 in the error envelope with Retry-After. The issue does not fix N
// or the window, so the tests find N by sending failures until the first 429
// and assert only what the issue states: the cap exists, is modest, and
// behaves as described around it. The host is the connection's remote address
// (X-Forwarded-For is never trusted); IPv6 is keyed by /64.

const (
	hostA     = "203.0.113.7:41000"
	hostB     = "203.0.113.8:41000"
	signInCap = 100 // a cap above this is no cap for a brute-force guard
	badGuess  = "unknown-token-0123456789abcdef0123456789"
)

// signInFrom posts token to the session endpoint as the remote address
// remote, with optional extra request changes. The response is checked
// against the spec.
func signInFrom(t *testing.T, h http.Handler, remote, token string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req := signInRequest(t, remote, token, mutate...)
	resp, body := serve(t, h, req)
	contracttest.Conforms(t, resp, body)
	return resp, body
}

func signInRequest(t *testing.T, remote, token string, mutate ...func(*http.Request)) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, secureBase+sessionPath, strings.NewReader(mustJSON(t, map[string]string{"token": token})))
	req.RemoteAddr = remote
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	for _, m := range mutate {
		m(req)
	}
	return req
}

// failuresUntilLimited sends bad sign-ins from remote until the first 429 and
// returns how many were answered 401 before it (N), with that 429. It fails
// when no 429 comes within signInCap failures.
func failuresUntilLimited(t *testing.T, h http.Handler, remote string, mutate ...func(*http.Request)) (n int, resp *http.Response, body []byte) {
	t.Helper()
	for n = 0; n <= signInCap; n++ {
		resp, body = signInFrom(t, h, remote, badGuess, mutate...)
		switch resp.StatusCode {
		case http.StatusUnauthorized:
		case http.StatusTooManyRequests:
			return n, resp, body
		default:
			t.Fatalf("failed sign-in %d: status %d, want 401 or 429: %s", n+1, resp.StatusCode, body)
		}
	}
	t.Fatalf("%d failed sign-ins from one host were never limited: sign-in accepts unlimited guesses", signInCap+1)
	return 0, nil, nil
}

func assertLimited(t *testing.T, what string, resp *http.Response, body []byte) {
	t.Helper()
	assertStatus(t, what, resp, body, http.StatusTooManyRequests)
	if code, _ := errorHint(t, body); code != "rate_limited" {
		t.Errorf("%s: code %q, want rate_limited", what, code)
	}
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err != nil || secs < 1 {
		t.Errorf("%s: Retry-After %q, want whole seconds of at least 1", what, resp.Header.Get("Retry-After"))
	}
	assertNoCookie(t, what, resp)
	assertNoStore(t, what, resp)
	for name, re := range leaks {
		if m := re.FindString(string(body)); m != "" {
			t.Errorf("%s: body contains a %s (%q): %s", what, name, m, body)
		}
	}
}

// After N failed sign-ins from one host the next call is 429 in the envelope
// with Retry-After, whatever the source port; N is a real, modest cap.
func TestSignInFromOneHostIsLimitedAfterRepeatedFailures(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	n, resp, body := failuresUntilLimited(t, h, hostA)
	if n < 1 {
		t.Fatalf("the very first failed sign-in was limited (N=%d): a cap of zero locks everyone out", n)
	}
	assertLimited(t, "the call after the cap", resp, body)

	resp, body = signInFrom(t, h, "203.0.113.7:55555", badGuess)
	assertLimited(t, "same host, new source port", resp, body)
}

// A successful sign-in below the cap is not blocked by earlier failures, and
// successes never count toward the cap.
func TestSignInBelowTheCapSucceedsAfterEarlierFailures(t *testing.T) {
	probe := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	n, _, _ := failuresUntilLimited(t, probe, hostA)

	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	for i := range n - 1 {
		if resp, body := signInFrom(t, h, hostA, badGuess); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("failure %d of %d: status %d, want 401: %s", i+1, n-1, resp.StatusCode, body)
		}
	}
	resp, body := signInFrom(t, h, hostA, operatorToken)
	assertStatus(t, "valid sign-in below the cap", resp, body, http.StatusOK)
	theCookie(t, resp)

	fresh := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	for i := range 2*signInCap + 1 {
		if resp, body := signInFrom(t, fresh, hostB, readerToken); resp.StatusCode != http.StatusOK {
			t.Fatalf("successful sign-in %d from one host: status %d, want 200: %s", i+1, resp.StatusCode, body)
		}
	}
}

// One host's failures do not limit another host.
func TestSignInLimitIsPerHost(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	failuresUntilLimited(t, h, hostA)
	resp, body := signInFrom(t, h, hostB, badGuess)
	assertStatus(t, "another host's first failure", resp, body, http.StatusUnauthorized)
	resp, body = signInFrom(t, h, hostB, operatorToken)
	assertStatus(t, "another host's valid sign-in", resp, body, http.StatusOK)
}

// A valid token from a limited host is refused too, and the 429 is the same
// whether or not the token was valid, so it reveals nothing about it. Once
// Retry-After has passed the host may sign in again.
func TestAValidTokenFromALimitedHostIsRefusedWithoutRevealingValidity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
		_, bad, badBody := failuresUntilLimited(t, h, hostA)

		resp, body := signInFrom(t, h, hostA, operatorToken)
		assertLimited(t, "valid token from a limited host", resp, body)
		assertNoLeak(t, "valid token from a limited host", resp, body, operatorToken)
		if string(body) != string(badBody) {
			t.Errorf("429 for a valid token differs from the 429 for a bad one:\n valid: %s\n   bad: %s", body, badBody)
		}
		if resp.Header.Get("Content-Type") != bad.Header.Get("Content-Type") {
			t.Errorf("429 Content-Type differs between a valid and a bad token")
		}
		if strings.Contains(string(body), "ci-operator") {
			t.Errorf("429 names the token: %s", body)
		}

		secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
		if err != nil {
			t.Fatalf("Retry-After %q: %v", resp.Header.Get("Retry-After"), err)
		}
		time.Sleep(time.Duration(secs) * time.Second)
		resp, body = signInFrom(t, h, hostA, operatorToken)
		assertStatus(t, "valid sign-in after Retry-After", resp, body, http.StatusOK)
	})
}

// X-Forwarded-For is ignored: there is no trusted-proxy setting, so a client
// can neither dodge the cap by rotating it nor cause another host's limit.
func TestSignInLimitIgnoresForwardedHeaders(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	i := 0
	rotate := func(r *http.Request) {
		i++
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i%250+1))
		r.Header.Set("Forwarded", fmt.Sprintf("for=198.51.100.%d", i%250+1))
		r.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", i%250+1))
	}
	failuresUntilLimited(t, h, hostA, rotate)

	resp, body := signInFrom(t, h, hostA, badGuess, func(r *http.Request) { r.Header.Set("X-Forwarded-For", "192.0.2.200") })
	assertLimited(t, "limited host claiming a fresh X-Forwarded-For", resp, body)

	resp, body = signInFrom(t, h, hostB, operatorToken, func(r *http.Request) { r.Header.Set("X-Forwarded-For", "203.0.113.7") })
	assertStatus(t, "fresh host claiming the limited host's address", resp, body, http.StatusOK)
}

// IPv6 is keyed by /64: addresses in one /64 share a limit, another /64 does
// not.
func TestSignInLimitKeysIPv6ByItsSlash64(t *testing.T) {
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	failuresUntilLimited(t, h, "[2001:db8:1:2::1]:41000")

	resp, body := signInFrom(t, h, "[2001:db8:1:2:ffff:ffff:ffff:ffff]:41000", badGuess)
	assertLimited(t, "another address in the same /64", resp, body)
	resp, body = signInFrom(t, h, "[2001:db8:1:2:aaaa::9]:41000", operatorToken)
	assertLimited(t, "a valid token from another address in the same /64", resp, body)

	resp, body = signInFrom(t, h, "[2001:db8:1:3::1]:41000", operatorToken)
	assertStatus(t, "a different /64", resp, body, http.StatusOK)
}

// Failed sign-ins from a host do not spend any API token's bucket, and the
// per-token limit still applies to bearer requests as before.
func TestSignInLimitLeavesPerTokenLimitsAsTheyWere(t *testing.T) {
	cfg := sessionConfig(t, namedTokenLines(), map[string]string{"GOLDIE_SESSION_KEY": sessionKey})
	cfg.RateLimit = loadedRateLimit(t, "2/m")
	h, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	failuresUntilLimited(t, h, hostA)

	get := func(token string) int {
		resp, _ := send(t, h, http.MethodGet, secureBase+lookupPath, func(r *http.Request) {
			r.RemoteAddr = hostA
			bearer(token)(r)
		})
		return resp.StatusCode
	}
	for i := range 2 {
		if got := get(readerToken); got != http.StatusOK {
			t.Fatalf("bearer request %d from a sign-in-limited host: status %d, want 200", i+1, got)
		}
	}
	if got := get(readerToken); got != http.StatusTooManyRequests {
		t.Errorf("third bearer request within 2/m: status %d, want 429 (per-token limit as before)", got)
	}
	if got := get(operatorToken); got != http.StatusOK {
		t.Errorf("another token: status %d, want 200", got)
	}
}

// The limiter's memory is bounded: a flood of failures from many distinct
// hosts must not grow the heap without limit.
func TestSignInLimiterMemoryIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("floods the handler with distinct hosts")
	}
	h := sessionHandler(t, namedTokenLines(), sessionKey, nil)
	failuresUntilLimited(t, h, hostA) // there is a limiter to bound
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	flood := func(from, to int) {
		for i := from; i < to; i++ {
			req := signInRequest(t, fmt.Sprintf("10.%d.%d.%d:4000", i>>16&255, i>>8&255, i&255), badGuess)
			h.ServeHTTP(httptest.NewRecorder(), req)
		}
	}
	const batch = 150_000
	flood(0, 1000)
	base := heap()
	flood(1000, batch)
	grown := heap()
	if grown > base+24<<20 {
		t.Errorf("heap grew from %d to %d bytes (+%d MiB) over %d distinct hosts: the limiter map is unbounded",
			base, grown, (grown-base)>>20, batch)
	}
	runtime.KeepAlive(h)
}
