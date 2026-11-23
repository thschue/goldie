package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #399 beyond the #245 set in activity_federated_test.go
// and activity_federated_revision_test.go (whose harness they share): the
// `replicas` enum, reconnect backoff, hostile peers, log lines and the docs.

// Criterion 1: `replicas` is an enum with only `all`; any other value is a
// 400 in the error envelope, answered before any peer is asked and before a
// stream opens, and it echoes neither the value nor an address.
func TestAnyReplicasValueButAllIs400(t *testing.T) {
	a, b, c := abc(t)
	for _, v := range []string{"none", "ALL", "All", "all,all", "", "1", "%20all", "all%20", "all%00", "local", "*", "%2e%2e%2f", "http%3A%2F%2F127.0.0.1%3A1"} {
		t.Run(v, func(t *testing.T) {
			resp, body := do(t, http.MethodGet, a.srv.URL+activityPath+"?replicas="+v, bearer(operatorToken))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("replicas=%q: status %d, want 400: %s", v, resp.StatusCode, body)
			}
			if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
				t.Errorf("replicas=%q opened a stream", v)
			}
			assertEnvelope(t, body, "")
			assertNoAddress(t, "the 400", body, a, b, c)
			if dec, err := url.QueryUnescape(v); err == nil && len(dec) > 3 && strings.Contains(string(body), dec) {
				t.Errorf("replicas=%q: the 400 echoes the value: %s", v, body)
			}
		})
	}
	// A repeated parameter with a bad value is as wrong as a single one.
	resp, body := do(t, http.MethodGet, a.srv.URL+activityPath+"?replicas=all&replicas=none", bearer(operatorToken))
	if resp.StatusCode == http.StatusOK {
		t.Errorf("replicas=all&replicas=none: status 200, want a 400: %s", body)
	}
	time.Sleep(300 * time.Millisecond)
	for _, peer := range []*replicaNode{b, c} {
		if got := peer.sw.peerRequests(); len(got) != 0 {
			t.Errorf("%s was asked %d times for a request the serving replica refused as malformed", peer.name, len(got))
		}
	}
}

// Criterion 2: a peer that stays down is retried with backoff, not in a tight
// loop (each attempt costs a connection and the forwarded credential), and
// the retries go on: it is back up once it answers.
func TestADownPeerIsRetriedWithBackoffNotInATightLoop(t *testing.T) {
	a, b, _ := abc(t)
	b.sw.set(modeRefuse)
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	r.until(t, 10*time.Second, "goldie-b down", status("goldie-b", false))
	time.Sleep(3 * time.Second)
	if n := len(b.sw.peerRequests()); n < 2 || n > 10 {
		t.Errorf("a down peer was asked %d times in about 3s, want a few (2 to 10): retries with backoff", n)
	}
	b.sw.set(modeUp)
	r.until(t, recoverWithin, "goldie-b up", status("goldie-b", true))
	if s := statuses(r.seen); len(s) != 2 {
		t.Errorf("%d statuses over the outage, want one down and one up: retries are silent", len(s))
	}
}

// hostilePeer is a replica whose stream the test writes by hand.
func hostilePeer(t *testing.T, serve func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		serve(w, r)
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop); srv.CloseClientConnections() })
	return srv
}

const liveMarker = "event: live\ndata: {}\n\n"

// Criterion 2: a peer is down when its stream is broken, whatever way it is
// broken, and the others are not held up: it sends an event over the size
// limit, a line that is no event, an event that claims another replica, or
// ends its stream right after the marker. None of its events show, nothing of
// what it sent reaches the client, and no address does.
func TestAHostilePeerShowsAsDownAndTheOthersFlow(t *testing.T) {
	huge := strings.Repeat("x", 20<<20)
	for name, serve := range map[string]func(http.ResponseWriter, *http.Request){
		"an event over the size limit": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, liveMarker)
			_, _ = fmt.Fprintf(w, `data: {"id":1,"time":"2026-01-01T00:00:00Z","kind":"search","query":"%s","project":"","token":"t","client_version":"","trace_id":"","miss":false,"replica":"goldie-bad","late":false}`+"\n\n", huge)
		},
		"a line that is no event": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, liveMarker)
			_, _ = fmt.Fprint(w, "data: this is not json\n\n")
		},
		"a stream that ends after the marker": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, liveMarker)
		},
		"events of another replica": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, liveMarker)
			_, _ = fmt.Fprint(w, `data: {"id":1,"time":"2026-01-01T00:00:00Z","kind":"search","query":"forged","project":"","token":"t","client_version":"","trace_id":"","miss":false,"replica":"goldie-c","late":false}`+"\n\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, b, c := abc(t)
			bad := hostilePeer(t, serve)
			useResolver(t, func(_ context.Context, n string) ([]activityPeer, error) {
				if n != peersName {
					return nil, errors.New(resolverFailure)
				}
				return []activityPeer{{Name: "goldie-a", URL: a.srv.URL}, {Name: "goldie-b", URL: b.srv.URL},
					{Name: "goldie-bad", URL: bad.URL}, {Name: "goldie-c", URL: c.srv.URL}}, nil
			})
			record(t, b, "b-hist")
			r := openFederated(t, t.Context(), a, bearer(operatorToken))
			r.until(t, 20*time.Second, "goldie-bad down", status("goldie-bad", false))
			r.until(t, 5*time.Second, "b-hist", query("b-hist"))
			record(t, c, "c-live")
			r.until(t, 5*time.Second, "c-live", query("c-live"))
			r.drain(time.Second)
			if n := len(eventsFor(r.seen, "forged")); n != 0 {
				t.Errorf("an event forged for another replica was shown %d times", n)
			}
			for _, e := range r.seen {
				if w, ok := asEvent(e); ok && w.replica() == "goldie-bad" {
					t.Errorf("an event of the broken peer: %.200s", e.Data)
				}
			}
			raw := r.s.bytes()
			if len(raw) > 1<<20 {
				t.Errorf("the merged stream carried %d bytes: the peer's oversized event reached the client", len(raw))
			}
			assertNoAddress(t, "the merged stream", raw, a, b, c)
			if strings.Contains(string(raw), strings.TrimPrefix(bad.URL, "http://")) {
				t.Error("the merged stream carries the broken peer's address")
			}
		})
	}
}

// Criterion 3: no peer address appears in any log line. The serving replica
// logs through the logger it is given and the default logger, at debug level;
// a failing resolver (whose error names a DNS server), an unreachable peer, a
// hanging one, a redirect, a peer that refuses the token and a clean merged
// stream leave neither an address, a resolver's words nor a token in the log.
func TestNoPeerAddressAppearsInAnyLogLine(t *testing.T) {
	sink := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug}))
	old := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })

	nodes := startReplicas(t, nodeSpec{name: "goldie-a", logger: logger}, nodeSpec{name: "goldie-b"}, nodeSpec{name: "goldie-c"},
		nodeSpec{name: "goldie-d"}, nodeSpec{name: "goldie-e", tokens: "ci-reader:reader:" + readerToken})
	a, b, c, d, e := nodes[0], nodes[1], nodes[2], nodes[3], nodes[4]
	trap, trapHits := newTrap(t)
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()
	b.sw.set(modeRefuse)
	c.sw.set(modeHang)
	d.sw.redirect(http.StatusTemporaryRedirect, trap.URL)

	useResolver(t, func(context.Context, string) ([]activityPeer, error) { return nil, errors.New(resolverFailure) })
	first, leave := context.WithCancel(t.Context())
	r := openFederated(t, first, a, bearer(operatorToken))
	r.drain(500 * time.Millisecond)
	leave()
	waitReadEnds(t, r.s)
	waitHubEmpty(t, a.pub.Activity())

	useResolver(t, func(_ context.Context, n string) ([]activityPeer, error) {
		if n != peersName {
			return nil, errors.New(resolverFailure)
		}
		return []activityPeer{{Name: "goldie-a", URL: a.srv.URL}, {Name: "goldie-b", URL: b.srv.URL}, {Name: "goldie-c", URL: c.srv.URL},
			{Name: "goldie-d", URL: d.srv.URL}, {Name: "goldie-e", URL: e.srv.URL}, {Name: "goldie-gone", URL: goneURL}}, nil
	})
	r = openFederated(t, t.Context(), a, bearer(operatorToken))
	for _, n := range []string{"goldie-b", "goldie-c", "goldie-d", "goldie-e", "goldie-gone"} {
		r.until(t, 20*time.Second, n+" down", status(n, false))
	}
	r.drain(2 * time.Second)

	if n := trapHits(); n != 0 {
		t.Errorf("the redirect target got %d requests", n)
	}
	logs := strings.Join(sink.lines(), "\n")
	bad := []string{"127.0.0.1", "10.96.0.10", "resolv.conf", "no such host", "leak-marker", operatorToken, readerToken, goneURL, trap.URL}
	for _, n := range nodes {
		bad = append(bad, n.srv.URL, n.host())
	}
	for _, s := range bad {
		if strings.Contains(logs, s) {
			t.Errorf("a log line carries %q:\n%s", s, logs)
		}
	}
	for kind, re := range leaks {
		if kind != "IPv4 address" && kind != "IPv6 address" {
			continue
		}
		if m := re.FindString(logs); m != "" {
			t.Errorf("a log line contains a %s (%q):\n%s", kind, m, logs)
		}
	}
}

// Criterion 4, the docs: the setting's text, in platform.Settings and in the
// committed reference, covers a StatefulSet whose serviceName is the headless
// Service, says a Deployment gets the local stream, requires TLS or a
// NetworkPolicy for the forwarded bearer token, and no longer says the
// feature is not served.
func TestTheOperatorGuideCoversFederatedActivity(t *testing.T) {
	var meaning string
	for _, s := range platform.Settings {
		if s.Name == platform.EnvActivityPeers {
			meaning = s.Meaning
		}
	}
	if meaning == "" {
		t.Fatalf("%s is not in platform.Settings", platform.EnvActivityPeers)
	}
	ref, err := os.ReadFile("../../docs/reference/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	var row string
	for _, line := range strings.Split(string(ref), "\n") {
		if strings.HasPrefix(line, "| `"+platform.EnvActivityPeers+"` |") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("docs/reference/configuration.md has no %s row", platform.EnvActivityPeers)
	}
	for where, text := range map[string]string{"the setting": meaning, "docs/reference/configuration.md": row} {
		low := strings.ToLower(text)
		for _, want := range []string{"statefulset", "serviceName", "deployment", "local stream", "replicas=all", "bearer token"} {
			if !strings.Contains(low, strings.ToLower(want)) {
				t.Errorf("%s does not mention %q", where, want)
			}
		}
		if !strings.Contains(text, "TLS") || !strings.Contains(text, "NetworkPolicy") {
			t.Errorf("%s does not require TLS or a NetworkPolicy for the forwarded token", where)
		}
		for _, stale := range []string{"does not serve", "not served yet"} {
			if strings.Contains(low, stale) {
				t.Errorf("%s still says the feature is not served (%q)", where, stale)
			}
		}
	}
}

// Criterion 4, the reference: docs/reference/api.md and the served spec
// explain replicas=all and replica_status, and say the 10,000-event history
// bound is per replica, since a merged history is not bounded overall.
func TestTheAPIReferenceDescribesFederatedActivity(t *testing.T) {
	md, err := os.ReadFile("../../docs/reference/api.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := specDoc(t)
	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths[activityPath].(map[string]any)
	op, _ := item["get"].(map[string]any)
	desc, _ := op["description"].(string)
	for where, text := range map[string]string{"docs/reference/api.md": string(md), "the spec's description of GET " + activityPath: desc} {
		for _, want := range []string{"replicas=all", "replica_status"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not mention %q", where, want)
			}
		}
		flat := strings.Join(strings.Fields(text), " ")
		found := false
		for _, sentence := range strings.Split(flat, ". ") {
			if strings.Contains(sentence, "10,000") {
				found = true
				if !strings.Contains(sentence, "per replica") {
					t.Errorf("%s: %q does not say the history bound is per replica", where, sentence)
				}
			}
		}
		if !found {
			t.Errorf("%s no longer states the history bound", where)
		}
	}
}
