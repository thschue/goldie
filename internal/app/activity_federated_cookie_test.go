package app

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance tests for #629 (slice 1 of #22): GET /api/v1/activity?replicas=all
// forwards the caller's session cookie to the peers when the caller has no
// bearer token, so an operator signed into the UI sees every replica. The
// harness is activity_federated_test.go's; every replica shares sessionKey
// unless a nodeSpec says otherwise.

// cookieOf is the session cookie of an operator signed in on n.
func cookieOf(t *testing.T, n *replicaNode, token string) *http.Cookie {
	t.Helper()
	return signedIn(t, n.pub, secureBase, token)
}

// peerCookies are the cookies the peer's requests carried, by name.
func peerCookies(r peerRequest) map[string]string {
	out := map[string]string{}
	for _, c := range (&http.Request{Header: r.head}).Cookies() {
		out[c.Name] = c.Value
	}
	return out
}

// Criterion 1: with only the session cookie, the merged stream carries the
// events of every reachable peer and none is down, as with a bearer token.
func TestReplicasAllWithOnlyTheSessionCookieStreamsEveryPeer(t *testing.T) {
	a, b, c := abc(t)
	record(t, b, "b-hist")
	record(t, c, "c-hist")
	ck := cookieOf(t, a, operatorToken)
	r := openFederated(t, t.Context(), a, withCookie(ck))
	r.until(t, 10*time.Second, "b-hist", query("b-hist"))
	if len(eventsFor(r.seen, "c-hist")) == 0 {
		r.until(t, 10*time.Second, "c-hist", query("c-hist"))
	}
	record(t, c, "c-live")
	r.until(t, 10*time.Second, "c-live", query("c-live"))
	r.drain(time.Second)
	if n := len(statuses(r.seen)); n != 0 {
		t.Errorf("%d replica_status events, want none: every peer accepted the cookie", n)
	}
	for _, p := range []*replicaNode{b, c} {
		if len(p.sw.peerRequests()) == 0 {
			t.Errorf("%s was never asked", p.name)
		}
	}
}

// Criterion 2: with a bearer token and the cookie, peers get the bearer token
// and no cookie.
func TestABearerTokenIsForwardedAndTheCookieIsNot(t *testing.T) {
	a, b, c := abc(t)
	ck := cookieOf(t, a, operatorToken)
	r := openFederated(t, t.Context(), a, both(bearer(operatorToken), withCookie(ck)))
	r.drain(time.Second)
	for _, p := range []*replicaNode{b, c} {
		reqs := p.sw.peerRequests()
		if len(reqs) == 0 {
			t.Fatalf("%s was never asked", p.name)
		}
		for _, q := range reqs {
			if q.auth != "Bearer "+operatorToken {
				t.Errorf("%s got Authorization %q, want the caller's bearer token", p.name, q.auth)
			}
			if got := q.head.Values("Cookie"); len(got) != 0 {
				t.Errorf("%s got a Cookie header next to the bearer token: %v", p.name, got)
			}
		}
	}
}

// Criterion 3: only the session cookie goes to a peer, under its local name;
// no other cookie and none of the request's other headers do.
func TestOnlyTheSessionCookieIsForwardedToPeers(t *testing.T) {
	a, b, c := abc(t)
	ck := cookieOf(t, a, operatorToken)
	r := openFederated(t, t.Context(), a, both(withCookie(ck), func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: "analytics", Value: "tracker-marker"})
		req.AddCookie(&http.Cookie{Name: "__Host-goldie_other", Value: "other-marker"})
		req.Header.Set("X-Forwarded-For", "203.0.113.9")
		req.Header.Set("X-Custom-Secret", "header-marker")
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Referer", "https://evil.example/page")
		req.Header.Set("Proxy-Authorization", "Basic cHJveHk6cHJveHk=")
	}))
	r.drain(time.Second)
	for _, p := range []*replicaNode{b, c} {
		reqs := p.sw.peerRequests()
		if len(reqs) == 0 {
			t.Fatalf("%s was never asked", p.name)
		}
		for _, q := range reqs {
			got := peerCookies(q)
			if len(got) != 1 || got[ck.Name] != ck.Value {
				t.Errorf("%s got cookies %v, want only %s", p.name, got, ck.Name)
			}
			for _, h := range []string{"X-Forwarded-For", "X-Custom-Secret", "Origin", "Referer", "Proxy-Authorization", "Authorization"} {
				if v := q.head.Get(h); v != "" {
					t.Errorf("%s got header %s: %q", p.name, h, v)
				}
			}
		}
	}
}

// Criterion 4 and the leak criterion: a peer with another GOLDIE_SESSION_KEY
// refuses the cookie, shows as down with refused_token, and the others go on.
// The cookie appears in no log line, no event and no byte of the stream.
func TestAPeerWithAnotherSessionKeyIsRefusedTokenAndTheOthersGoOn(t *testing.T) {
	sink := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug}))
	nodes := startReplicas(t, nodeSpec{name: "goldie-a", logger: logger}, nodeSpec{name: "goldie-b", key: otherKey}, nodeSpec{name: "goldie-c"})
	a, b, c := nodes[0], nodes[1], nodes[2]
	record(t, c, "c-hist")
	ck := cookieOf(t, a, operatorToken)
	r := openFederated(t, t.Context(), a, withCookie(ck))
	r.until(t, 10*time.Second, "goldie-b down", status("goldie-b", false))
	if len(eventsFor(r.seen, "c-hist")) == 0 {
		r.until(t, 10*time.Second, "c-hist", query("c-hist"))
	}
	record(t, c, "c-live")
	r.until(t, 10*time.Second, "c-live", query("c-live"))
	r.drain(500 * time.Millisecond)

	logs := strings.Join(sink.lines(), "\n")
	if !strings.Contains(logs, "goldie-b") || !strings.Contains(logs, "refused_token") {
		t.Errorf("the log does not say goldie-b is down with refused_token:\n%s", logs)
	}
	for where, text := range map[string]string{"the log": logs, "the stream": string(r.s.bytes())} {
		for _, secret := range []string{ck.Value, operatorToken} {
			if strings.Contains(text, secret) {
				t.Errorf("%s carries the forwarded credential %q", where, secret)
			}
		}
	}
	assertNoAddress(t, "the merged stream", r.s.bytes(), a, b, c)
}

// Criterion 5: a reader, by cookie or by bearer token, gets a 403 before any
// peer is called, and the body carries no credential.
func TestAReaderIsRefusedBeforeAnyPeerIsCalled(t *testing.T) {
	a, b, c := abc(t)
	ck := cookieOf(t, a, readerToken)
	for name, mutate := range map[string]func(*http.Request){"cookie": withCookie(ck), "bearer": bearer(readerToken)} {
		resp, body := do(t, http.MethodGet, a.srv.URL+federatedPath, mutate)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("a reader's %s: status %d, want 403: %s", name, resp.StatusCode, body)
		}
		assertEnvelope(t, body, "")
		for _, secret := range []string{ck.Value, readerToken} {
			if strings.Contains(string(body), secret) {
				t.Errorf("a reader's %s: the 403 carries the credential", name)
			}
		}
	}
	time.Sleep(300 * time.Millisecond)
	for _, p := range []*replicaNode{b, c} {
		if got := p.sw.peerRequests(); len(got) != 0 {
			t.Errorf("%s was called %d times for a reader", p.name, len(got))
		}
	}
}
