package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/api"
)

// Acceptance tests for #245, from the architect's answers on PR #378: the
// live marker that ends the history, a peer without it counting as down,
// re-resolving the peers on every federated request and every reconnect, an
// install without peer names, and the merged stream's dropped notice. The
// harness is activity_federated_test.go's.

// The local stream sends `event: live` with data {} straight after its
// retained history, even an empty one, and once; the merged stream sends it
// once, after the merged history.
func TestTheStreamMarksTheEndOfItsHistory(t *testing.T) {
	a, b, _ := abc(t)
	for _, name := range []string{"local", "merged"} {
		path := map[string]string{"local": activityPath, "merged": federatedPath}[name]
		ctx, cancel := context.WithCancel(t.Context())
		resp, s, body := openStream(t, ctx, http.DefaultClient, a.srv.URL+path, bearer(operatorToken))
		if resp.StatusCode != http.StatusOK {
			cancel()
			t.Fatalf("%s with no history: status %d: %s", name, resp.StatusCode, body)
		}
		r := &fedReader{s: s}
		first := r.until(t, 10*time.Second, "the first event", func(sseEvent) bool { return true })
		if first.Type != "live" || first.Data != "{}" || first.DataLines != 1 {
			t.Errorf("%s with no history: the first event is %+v, want the live marker (event: live, data: {})", name, first)
		}
		cancel()
		waitReadEnds(t, s)
		waitHubEmpty(t, a.pub.Activity())
	}

	record(t, a, "a-1")
	record(t, a, "a-2")
	record(t, b, "b-1")
	for _, name := range []string{"local", "merged"} {
		path := map[string]string{"local": activityPath, "merged": federatedPath}[name]
		ctx, cancel := context.WithCancel(t.Context())
		resp, s, body := openStream(t, ctx, http.DefaultClient, a.srv.URL+path, bearer(operatorToken))
		if resp.StatusCode != http.StatusOK {
			cancel()
			t.Fatalf("%s: status %d: %s", name, resp.StatusCode, body)
		}
		r := &fedReader{s: s}
		r.until(t, 10*time.Second, "the live marker", func(e sseEvent) bool { return e.Type == "live" })
		before := len(r.seen) - 1
		if want := map[string]int{"local": 2, "merged": 3}[name]; before != want {
			t.Errorf("%s: %d events before the marker, want the %d of the history: %+v", name, before, want, r.seen)
		}
		record(t, a, "a-live-"+name)
		r.until(t, 5*time.Second, "the live event", query("a-live-"+name))
		r.drain(300 * time.Millisecond)
		if n := countType(r.seen, "live"); n != 1 {
			t.Errorf("%s: %d live markers, want one", name, n)
		}
		cancel()
		waitReadEnds(t, s)
		waitHubEmpty(t, a.pub.Activity())
	}
}

// oldPeer is a peer of an older version: its stream has events and
// keepalives, but no live marker.
func oldPeer(t *testing.T, name string) *httptest.Server {
	t.Helper()
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		rc := http.NewResponseController(w)
		_ = rc.Flush()
		for id := 1; ; id++ {
			data, _ := json.Marshal(map[string]any{"id": id, "time": time.Now().UTC(), "kind": "search", "query": "old-" + name,
				"project": "", "token": "ci-operator", "client_version": "", "trace_id": "", "miss": false, "replica": name, "late": false})
			if _, err := fmt.Fprintf(w, "data: %s\n\n: keepalive\n\n", data); err != nil {
				return
			}
			_ = rc.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop); srv.CloseClientConnections() })
	return srv
}

// A peer that sends no live marker (an older version) counts as down: a
// replica_status down, none of its events, and the others flow.
func TestAPeerWithoutTheLiveMarkerCountsAsDown(t *testing.T) {
	a, b, c := abc(t)
	old := oldPeer(t, "goldie-old")
	useResolver(t, func(_ context.Context, name string) ([]activityPeer, error) {
		if name != peersName {
			return nil, errors.New(resolverFailure)
		}
		return []activityPeer{{Name: "goldie-a", URL: a.srv.URL}, {Name: "goldie-b", URL: b.srv.URL},
			{Name: "goldie-old", URL: old.URL}, {Name: "goldie-c", URL: c.srv.URL}}, nil
	})
	record(t, b, "b-hist")
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	r.until(t, 15*time.Second, "goldie-old down", status("goldie-old", false))
	r.until(t, 5*time.Second, "b-hist", query("b-hist"))
	record(t, c, "c-live")
	r.until(t, 5*time.Second, "c-live", query("c-live"))
	r.drain(time.Second)
	for _, e := range r.seen {
		if w, ok := asEvent(e); ok && w.replica() == "goldie-old" {
			t.Errorf("an event of a peer that sent no live marker: %s", e.Data)
		}
	}
	assertNoAddress(t, "the merged stream", r.s.bytes(), a, b, c)
	if strings.Contains(string(r.s.bytes()), strings.TrimPrefix(old.URL, "http://")) {
		t.Error("the merged stream carries the old peer's address")
	}
}

// The peers are resolved again on every federated request and on every
// reconnect: a StatefulSet pod that restarts keeps its name and gets a new
// address (and a new Activity, ids from 1). The serving replica follows it:
// goldie-b goes down, comes back up at the new address, and its new events
// arrive; the old address is not asked again.
func TestAReconnectResolvesThePeersAgain(t *testing.T) {
	a, b, c := abc(t)
	var mu sync.Mutex
	calls := 0
	bURL := b.srv.URL
	useResolver(t, func(_ context.Context, name string) ([]activityPeer, error) {
		mu.Lock()
		defer mu.Unlock()
		if name != peersName {
			return nil, errors.New(resolverFailure)
		}
		calls++
		return []activityPeer{{Name: "goldie-a", URL: a.srv.URL}, {Name: "goldie-b", URL: bURL}, {Name: "goldie-c", URL: c.srv.URL}}, nil
	})
	callCount := func() int { mu.Lock(); defer mu.Unlock(); return calls }

	for i := range 2 {
		ctx, cancel := context.WithCancel(t.Context())
		before := callCount()
		r := openFederated(t, ctx, a, bearer(operatorToken))
		r.until(t, 10*time.Second, "the live marker", func(e sseEvent) bool { return e.Type == "live" })
		if callCount() <= before {
			t.Errorf("federated request %d did not resolve the peers", i+1)
		}
		cancel()
		waitReadEnds(t, r.s)
		waitHubEmpty(t, a.pub.Activity())
	}

	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	record(t, b, "b-old-address")
	r.until(t, 10*time.Second, "b-old-address", query("b-old-address"))
	oldRequests := len(b.sw.peerRequests())

	b2 := buildReplica(t, nodeSpec{name: "goldie-b"})
	mu.Lock()
	bURL = b2.srv.URL
	mu.Unlock()
	before := callCount()
	b.sw.set(modeRefuse)
	b.srv.CloseClientConnections()
	r.until(t, 10*time.Second, "goldie-b down", status("goldie-b", false))
	r.until(t, recoverWithin, "goldie-b up at its new address", status("goldie-b", true))
	if callCount() <= before {
		t.Error("the reconnect did not resolve the peers again")
	}
	record(t, b2, "b-new-address")
	if e := mustEvent(t, r.until(t, 10*time.Second, "b-new-address", query("b-new-address"))); e.replica() != "goldie-b" {
		t.Errorf("the restarted pod's event has replica %q, want goldie-b", e.replica())
	}
	if n := len(b.sw.peerRequests()); n != oldRequests {
		t.Errorf("the old address was asked %d more times after the name moved", n-oldRequests)
	}
	assertNoAddress(t, "the merged stream", r.s.bytes(), a, b, c, b2)
}

// An install without peer names (a Deployment: lookupActivityPeers skips
// every dashed-IP PTR) gets this replica's stream for ?replicas=all: no
// status, no peer asked or shown.
func TestAnInstallWithoutPeerNamesGetsTheLocalStream(t *testing.T) {
	a, b, _ := abc(t)
	useResolver(t, func(context.Context, string) ([]activityPeer, error) { return nil, nil })
	record(t, a, "a-hist")
	record(t, b, "b-hist")
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	r.until(t, 5*time.Second, "a-hist", query("a-hist"))
	record(t, a, "a-live")
	r.until(t, 5*time.Second, "a-live", query("a-live"))
	r.drain(500 * time.Millisecond)
	if s := statuses(r.seen); len(s) != 0 {
		t.Errorf("replica_status with no peers: %+v", s)
	}
	if len(eventsFor(r.seen, "b-hist")) != 0 || len(b.sw.peerRequests()) != 0 {
		t.Error("an unnamed peer was asked or shown")
	}
}

// A merged subscriber that stops reading loses events (the merger never
// waits on it) and, reading again, gets a dropped notice counted from the
// merger's own drops: one line of JSON with dropped at least 1 and no
// replica field.
func TestASlowMergedSubscriberGetsADroppedNotice(t *testing.T) {
	a, b, _ := abc(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := newStallWriter(ctx)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, federatedPath, nil)
	bearer(operatorToken)(req)
	served := make(chan struct{})
	go func() {
		defer close(served)
		a.pub.ServeHTTP(w, req)
	}()
	select {
	case <-w.wrote:
	case <-served:
		t.Fatalf("the merged stream ended before writing: status %d: %s", w.code, w.bytes())
	case <-time.After(10 * time.Second):
		t.Fatal("the merged stream wrote nothing within 10s, not even the live marker")
	}
	for i := range 3 * api.ActivitySubscriberBuffer {
		record(t, b, fmt.Sprintf("b-%d", i))
	}
	time.Sleep(4 * testWindow) // every held event falls due into the stalled output
	close(w.gate)
	var events []sseEvent
	deadline := time.Now().Add(10 * time.Second)
	for {
		record(t, a, "after")
		time.Sleep(50 * time.Millisecond)
		events, _ = parseSSE(w.bytes())
		if slices.ContainsFunc(events, func(e sseEvent) bool { return strings.Contains(e.Data, `"after"`) }) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the merged subscriber never caught up; it got:\n%s", w.bytes())
		}
	}
	cancel()
	<-served
	i := slices.IndexFunc(events, func(e sseEvent) bool { return e.Type == "dropped" })
	if i < 0 {
		t.Fatalf("a merged subscriber that lost events got no dropped notice:\n%s", w.bytes())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(events[i].Data), &m); err != nil || events[i].DataLines != 1 {
		t.Fatalf("the dropped notice is not one line of JSON: %q", events[i].Data)
	}
	if n, _ := m["dropped"].(float64); n < 1 {
		t.Errorf("dropped notice %v, want the number lost, at least 1", m)
	}
	if _, ok := m["replica"]; ok || len(m) != 1 {
		t.Errorf("dropped notice %v, want only dropped (no replica field)", m)
	}
}
