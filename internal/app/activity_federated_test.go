package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #245 (ADR 0022): GET /api/v1/activity?replicas=all
// merges the live activity of every replica. The seam is the public handler
// PublicHandler builds, two or three of them in process, each served over a
// real listener, and read the way an SSE client reads them. The contract
// they fix, beyond the spec:
//
//	// activityPeer is one peer GOLDIE_ACTIVITY_PEERS resolves to: Name is its
//	// replica name, the only thing clients see of it (MergeInput.Name,
//	// ReplicaStatus.Replica); URL is the base URL the forwarded credential
//	// goes to, never shown to a client.
//	type activityPeer struct{ Name, URL string }
//
//	// resolveActivityPeers resolves the configured DNS name to the peers,
//	// this replica among them. A variable so tests stand in for DNS (in
//	// process, every replica is on 127.0.0.1 with its own port); in
//	// production it is lookupActivityPeers over the system resolver and this
//	// replica's listen port (activity_peers_lookup_test.go). Called on every
//	// federated request and on every reconnect, never with a name from the
//	// request.
//	var resolveActivityPeers func(ctx context.Context, name string) ([]activityPeer, error)
//
//	// activityReplicaName is this replica's own name, the replica of its
//	// events (with or without peers; NewActivity gets it instead of "") and
//	// how it knows itself among the peers, by Name equality. Read when the
//	// public handler is built, like activityKeepalive. It defaults to
//	// replicaNameFromHostname(os.Hostname()).
//	var activityReplicaName func() string
//
//	// activityPeerIdle is the idle read deadline of a peer stream: with no
//	// byte, keepalives included, for this long, the peer's Live closes and it
//	// shows as down. Read when the public handler is built.
//	var activityPeerIdle time.Duration
//
//	// MaxFederatedSubscribers caps concurrent ?replicas=all streams per
//	// replica; each costs one stream on every peer.
//	const api.MaxFederatedSubscribers
//
// Config.ActivityPeers and Config.ActivityReorderWindow are the settings
// (internal/platform). The wire format, beyond #243's:
//   - every event's JSON, local or merged, carries "replica", the name of the
//     replica that recorded it, and "late", true when it reached the merger
//     after its reorder window, false otherwise and always on the local
//     stream;
//   - right after the retained history, even an empty one, the stream sends
//     one SSE event of type live whose data is {} (ActivityLive); the merged
//     stream sends it once, after the merged history;
//   - a peer going down or coming back up is an SSE event of type
//     replica_status whose data is one line of JSON:
//     {"replica": <name>, "up": <bool>, "last_seen": <RFC 3339 time>};
//   - a peer that is up when the stream opens gets no status (the first
//     attach is silent);
//   - a merged subscriber that loses events gets the dropped notice, counted
//     from the merger's own drops, with no replica field.
//
// Revised for the architect's answers on PR #378 (the live marker, replica
// and late always sent, re-resolving on reconnect).
//
// Until these names exist this package's tests do not compile.

// peersName is the GOLDIE_ACTIVITY_PEERS every test replica is configured
// with; the stand-in resolver knows no other name.
const peersName = "goldie-peers.test"

// resolverFailure is what the stand-in resolver says about any other name:
// an error that carries addresses, as a real resolver's does.
const resolverFailure = "lookup goldie-peers.test on 10.96.0.10:53: dial udp 10.96.0.10:53: no such host /etc/resolv.conf"

const (
	testWindow   = 500 * time.Millisecond
	testPeerIdle = time.Second
	// recoverWithin bounds a reconnect after a peer comes back: the backoff
	// is the implementation's, this only says it is not minutes.
	recoverWithin = 20 * time.Second
)

// peerMode is how a replica's listener answers.
type peerMode int

const (
	modeUp       peerMode = iota // serve
	modeRefuse                   // drop every connection: the replica is down
	modeHang                     // accept, never answer
	modeRedirect                 // answer redirectCode to location
)

type peerRequest struct {
	host  string
	path  string
	query url.Values
	auth  string
}

// peerSwitch fronts one replica's handler, records every request it sees and
// lets a test take the replica down, hang it, freeze its open streams or
// turn it into a redirect.
type peerSwitch struct {
	next http.Handler
	stop chan struct{}

	mu           sync.Mutex
	mode         peerMode
	location     string
	redirectCode int
	frozen       bool
	reqs         []peerRequest
}

func (p *peerSwitch) set(mode peerMode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = mode
}

func (p *peerSwitch) redirect(code int, location string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode, p.redirectCode, p.location = modeRedirect, code, location
}

// freeze stalls every write of every stream, open or new: the replica falls
// silent, sending neither events nor keepalives, without closing anything.
func (p *peerSwitch) freeze() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.frozen = true
}

func (p *peerSwitch) isFrozen() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.frozen
}

// peerRequests are the activity requests that did not ask for replicas=all:
// the ones another replica sends. Tests never send such a request through
// the switch themselves.
func (p *peerSwitch) peerRequests() []peerRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []peerRequest
	for _, r := range p.reqs {
		if r.path == activityPath && r.query.Get("replicas") != "all" {
			out = append(out, r)
		}
	}
	return out
}

func (p *peerSwitch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.reqs = append(p.reqs, peerRequest{host: r.Host, path: r.URL.Path, query: r.URL.Query(), auth: r.Header.Get("Authorization")})
	mode, code, location := p.mode, p.redirectCode, p.location
	p.mu.Unlock()
	switch mode {
	case modeRefuse:
		if hj, ok := w.(http.Hijacker); ok {
			if c, _, err := hj.Hijack(); err == nil {
				_ = c.Close()
				return
			}
		}
		panic(http.ErrAbortHandler)
	case modeHang:
		select {
		case <-r.Context().Done():
		case <-p.stop:
		}
		return
	case modeRedirect:
		w.Header().Set("Location", location+"/leak-marker"+r.URL.RequestURI())
		w.WriteHeader(code)
		return
	}
	p.next.ServeHTTP(&freezeWriter{ResponseWriter: w, p: p, ctx: r.Context()}, r)
}

// freezeWriter blocks every Write while its switch is frozen.
type freezeWriter struct {
	http.ResponseWriter
	p   *peerSwitch
	ctx context.Context
}

func (w *freezeWriter) Write(b []byte) (int, error) {
	if w.p.isFrozen() {
		select {
		case <-w.ctx.Done():
			return 0, w.ctx.Err()
		case <-w.p.stop:
			return 0, http.ErrAbortHandler
		}
	}
	return w.ResponseWriter.Write(b)
}

func (w *freezeWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *freezeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// replicaNode is one in-process replica.
type replicaNode struct {
	name string
	pub  *Public
	sw   *peerSwitch
	srv  *httptest.Server
}

func (n *replicaNode) host() string { return strings.TrimPrefix(n.srv.URL, "http://") }

// nodeSpec describes one replica to start.
type nodeSpec struct {
	name    string
	tokens  string        // GOLDIE_API_TOKENS; "" is namedTokenLines()
	claims  string        // the name it stamps on its own events; "" is name
	noPeers bool          // GOLDIE_ACTIVITY_PEERS unset
	window  time.Duration // 0 is testWindow
}

// startReplicas starts the replicas, each configured with peersName, and
// stands in for DNS: peersName resolves to every replica, its own name and
// base URL, the serving one included, as a headless Service does.
func startReplicas(t *testing.T, specs ...nodeSpec) []*replicaNode {
	t.Helper()
	shortKeepalive(t, 50*time.Millisecond)
	oldIdle, oldName := activityPeerIdle, activityReplicaName
	t.Cleanup(func() { activityPeerIdle, activityReplicaName = oldIdle, oldName })
	activityPeerIdle = testPeerIdle

	nodes := make([]*replicaNode, 0, len(specs))
	for _, s := range specs {
		nodes = append(nodes, buildReplica(t, s))
	}
	useResolver(t, func(_ context.Context, name string) ([]activityPeer, error) {
		if name != peersName {
			return nil, errors.New(resolverFailure)
		}
		peers := make([]activityPeer, 0, len(nodes))
		for _, n := range nodes {
			peers = append(peers, activityPeer{Name: n.name, URL: n.srv.URL})
		}
		return peers, nil
	})
	return nodes
}

// buildReplica starts one replica. Call it after startReplicas, which set
// the timings and restore activityReplicaName.
func buildReplica(t *testing.T, s nodeSpec) *replicaNode {
	t.Helper()
	stamp := s.name
	if s.claims != "" {
		stamp = s.claims
	}
	activityReplicaName = func() string { return stamp }
	cfg := platform.Config{ActivityPeers: peersName, ActivityReorderWindow: testWindow, APITokens: s.tokens}
	if s.noPeers {
		cfg.ActivityPeers = ""
	}
	if s.window != 0 {
		cfg.ActivityReorderWindow = s.window
	}
	pub := streamHandler(t, cfg)
	sw := &peerSwitch{next: pub, stop: make(chan struct{})}
	srv := httptest.NewServer(sw)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(sw.stop); srv.CloseClientConnections() }) // runs before Close
	return &replicaNode{name: s.name, pub: pub, sw: sw, srv: srv}
}

func useResolver(t *testing.T, f func(context.Context, string) ([]activityPeer, error)) {
	t.Helper()
	old := resolveActivityPeers
	resolveActivityPeers = f
	t.Cleanup(func() { resolveActivityPeers = old })
}

func abc(t *testing.T) (a, b, c *replicaNode) {
	t.Helper()
	n := startReplicas(t, nodeSpec{name: "goldie-a"}, nodeSpec{name: "goldie-b"}, nodeSpec{name: "goldie-c"})
	return n[0], n[1], n[2]
}

// record has n record a search for q, in process, with the reader token.
func record(t *testing.T, n *replicaNode, q string) {
	t.Helper()
	if rec := serveAs(t, n.pub, searchPath(q), func(*http.Request) {}); rec.Code != http.StatusOK {
		t.Fatalf("fixture: search %q on %s: status %d", q, n.name, rec.Code)
	}
}

const federatedPath = activityPath + "?replicas=all"

// fedReader reads a merged stream and keeps every event it read.
type fedReader struct {
	s    *sseLive
	seen []sseEvent
}

func openFederated(t *testing.T, ctx context.Context, n *replicaNode, mutate func(*http.Request)) *fedReader {
	t.Helper()
	resp, s, body := openStream(t, ctx, http.DefaultClient, n.srv.URL+federatedPath, mutate)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s on %s: status %d, want 200: %s", federatedPath, n.name, resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("GET %s: Content-Type %q, want text/event-stream", federatedPath, ct)
	}
	return &fedReader{s: s}
}

// until reads events until pred holds for one and returns it.
func (r *fedReader) until(t *testing.T, within time.Duration, what string, pred func(sseEvent) bool) sseEvent {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case it, ok := <-r.s.items:
			if !ok {
				t.Fatalf("the merged stream ended while waiting for %s; so far:\n%s", what, r.s.bytes())
			}
			if it.comment {
				continue
			}
			r.seen = append(r.seen, it.event)
			if pred(it.event) {
				return it.event
			}
		case <-deadline:
			t.Fatalf("no %s within %v; so far:\n%s", what, within, r.s.bytes())
		}
	}
}

// drain reads for d and returns the events read in that time.
func (r *fedReader) drain(d time.Duration) []sseEvent {
	deadline := time.After(d)
	var got []sseEvent
	for {
		select {
		case it, ok := <-r.s.items:
			if !ok {
				return got
			}
			if !it.comment {
				r.seen = append(r.seen, it.event)
				got = append(got, it.event)
			}
		case <-deadline:
			return got
		}
	}
}

// fedEvent is the part of a merged event's JSON these tests read.
type fedEvent struct {
	ID      *uint64 `json:"id"`
	Replica *string `json:"replica"`
	Kind    string  `json:"kind"`
	Query   string  `json:"query"`
	Time    string  `json:"time"`
	Late    *bool   `json:"late"`
}

func (e fedEvent) late() bool { return e.Late != nil && *e.Late }

func (e fedEvent) replica() string {
	if e.Replica == nil {
		return ""
	}
	return *e.Replica
}

// fedStatus is a replica_status event's data.
type fedStatus struct {
	Replica  *string `json:"replica"`
	Up       *bool   `json:"up"`
	LastSeen *string `json:"last_seen"`
}

func asEvent(e sseEvent) (fedEvent, bool) {
	var w fedEvent
	if e.Type != "message" || json.Unmarshal([]byte(e.Data), &w) != nil || w.ID == nil {
		return fedEvent{}, false
	}
	return w, true
}

func asStatus(e sseEvent) (fedStatus, bool) {
	var s fedStatus
	if e.Type != "replica_status" || json.Unmarshal([]byte(e.Data), &s) != nil {
		return fedStatus{}, false
	}
	return s, true
}

// mustEvent checks e is one line of JSON with an id and a replica.
func mustEvent(t *testing.T, e sseEvent) fedEvent {
	t.Helper()
	w, ok := asEvent(e)
	if !ok || e.DataLines != 1 {
		t.Fatalf("not an activity event of one JSON line with an id: %+v", e)
	}
	if w.replica() == "" {
		t.Fatalf("a merged event without its replica: %s", e.Data)
	}
	return w
}

// mustStatus checks e is a well-formed replica_status.
func mustStatus(t *testing.T, e sseEvent) fedStatus {
	t.Helper()
	s, ok := asStatus(e)
	if !ok || e.DataLines != 1 || s.Replica == nil || s.Up == nil || s.LastSeen == nil {
		t.Fatalf("a replica_status that is not one JSON line with replica, up and last_seen: %+v", e)
	}
	if _, err := time.Parse(time.RFC3339Nano, *s.LastSeen); err != nil {
		t.Errorf("replica_status last_seen %q is not an RFC 3339 time: %v", *s.LastSeen, err)
	}
	return s
}

func query(q string) func(sseEvent) bool {
	return func(e sseEvent) bool { w, ok := asEvent(e); return ok && w.Query == q }
}

func status(name string, up bool) func(sseEvent) bool {
	return func(e sseEvent) bool {
		s, ok := asStatus(e)
		return ok && s.Replica != nil && *s.Replica == name && s.Up != nil && *s.Up == up
	}
}

// countType counts the events of type typ.
func countType(events []sseEvent, typ string) int {
	n := 0
	for _, e := range events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// statuses are the replica_status events among events.
func statuses(events []sseEvent) []sseEvent {
	var out []sseEvent
	for _, e := range events {
		if e.Type == "replica_status" {
			out = append(out, e)
		}
	}
	return out
}

// eventsFor are the activity events among events whose query is q.
func eventsFor(events []sseEvent, q string) []fedEvent {
	var out []fedEvent
	for _, e := range events {
		if w, ok := asEvent(e); ok && w.Query == q {
			out = append(out, w)
		}
	}
	return out
}

// assertNoAddress fails when raw carries any peer's address, the configured
// name or a resolver's or dialer's words.
func assertNoAddress(t *testing.T, where string, raw []byte, nodes ...*replicaNode) {
	t.Helper()
	bad := []string{peersName, "127.0.0.1", "10.96.0.10", "resolv.conf", "no such host", "dial ", "connection refused", "leak-marker"}
	for _, n := range nodes {
		bad = append(bad, n.srv.URL, n.host())
	}
	for _, b := range bad {
		if strings.Contains(string(raw), b) {
			t.Errorf("%s carries %q:\n%s", where, b, raw)
		}
	}
}

// assertNameIsNoAddress fails when a replica name shown to a client looks
// like an address, a port, a hostname or a path.
func assertNameIsNoAddress(t *testing.T, name string) {
	t.Helper()
	for kind, re := range leaks {
		if m := re.FindString(name); m != "" {
			t.Errorf("the replica name %q shown to the client contains a %s (%q)", name, kind, m)
		}
	}
}

// Criteria 2 and 3: the merged stream opens with every replica's history in
// time order, none of it late, each event naming its replica with the id that
// replica gave it; live events from every replica follow, ordered by time
// within the window. Replicas that are up give no status (the first attach is
// silent), ids that interleave across replicas are no dropped notice, and the
// serving replica asks each peer once, with the caller's credential, for its
// local stream, and never asks itself.
func TestFederatedActivityMergesEveryReplicaInTimeOrder(t *testing.T) {
	a, b, c := abc(t)
	for _, step := range []struct {
		n *replicaNode
		q string
	}{{b, "b-hist"}, {c, "c-hist"}, {a, "a-hist"}, {b, "b-hist-2"}} {
		record(t, step.n, step.q)
		time.Sleep(20 * time.Millisecond)
	}

	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	r.until(t, 10*time.Second, "the last history event", query("b-hist-2"))
	want := []struct {
		q, replica string
		id         uint64
	}{{"b-hist", "goldie-b", 1}, {"c-hist", "goldie-c", 1}, {"a-hist", "goldie-a", 1}, {"b-hist-2", "goldie-b", 2}}
	if len(r.seen) != len(want) {
		t.Fatalf("history: %d events, want %d, in time order: %+v", len(r.seen), len(want), r.seen)
	}
	for i, w := range want {
		e := mustEvent(t, r.seen[i])
		if e.Query != w.q || e.replica() != w.replica || *e.ID != w.id || e.late() {
			t.Errorf("history %d: query %q replica %q id %d late %v; want %q from %s with id %d, not late",
				i, e.Query, e.replica(), *e.ID, e.late(), w.q, w.replica, w.id)
		}
	}

	marker := r.until(t, 5*time.Second, "the live marker", func(e sseEvent) bool { return true })
	if marker.Type != "live" || marker.Data != "{}" {
		t.Fatalf("after the merged history: %+v, want the live marker (event: live, data: {})", marker)
	}
	history := len(r.seen)

	record(t, c, "c-live")
	time.Sleep(30 * time.Millisecond)
	record(t, b, "b-live")
	time.Sleep(30 * time.Millisecond)
	record(t, a, "a-live")
	r.until(t, 10*time.Second, "a-live", query("a-live"))
	var live []fedEvent
	for _, e := range r.seen[history:] {
		live = append(live, mustEvent(t, e))
	}
	if len(live) != 3 || live[0].Query != "c-live" || live[1].Query != "b-live" || live[2].Query != "a-live" {
		t.Fatalf("live events %+v, want c-live, b-live, a-live: ordered by time within the reorder window", live)
	}
	for i, wantReplica := range []string{"goldie-c", "goldie-b", "goldie-a"} {
		if live[i].replica() != wantReplica || live[i].late() {
			t.Errorf("live %s: replica %q late %v, want %s and not late", live[i].Query, live[i].replica(), live[i].late(), wantReplica)
		}
	}
	for _, e := range r.drain(300 * time.Millisecond) {
		t.Errorf("an extra event on a quiet merged stream: %+v", e)
	}
	for _, e := range r.seen {
		if e.Type == "dropped" || e.Type == "replica_status" {
			t.Errorf("a %s event while every replica was up and the subscriber kept up: %s", e.Type, e.Data)
		}
	}
	if n := countType(r.seen, "live"); n != 1 {
		t.Errorf("%d live markers on the merged stream, want exactly one", n)
	}

	for _, peer := range []*replicaNode{b, c} {
		got := peer.sw.peerRequests()
		if len(got) != 1 {
			t.Fatalf("%s saw %d peer requests, want exactly 1", peer.name, len(got))
		}
		if got[0].auth != "Bearer "+operatorToken {
			t.Errorf("%s got Authorization %q, want the caller's own credential", peer.name, got[0].auth)
		}
		if got[0].host != peer.host() {
			t.Errorf("%s got Host %q, want the resolved address %q", peer.name, got[0].host, peer.host())
		}
	}
	if got := a.sw.peerRequests(); len(got) != 0 {
		t.Errorf("the serving replica opened %d peer streams to itself", len(got))
	}
	assertNoAddress(t, "the merged stream", r.s.bytes(), a, b, c)
}

// Criterion 2: a client that does not send replicas=all gets today's stream:
// only this replica's events, ids consecutive from 1, the live marker once
// after the history, no status, no field beyond #243's but the additive
// replica (this replica's name) and late (always sent, always false), and no
// peer is asked.
func TestActivityWithoutReplicasAllIsTheLocalStreamUnchanged(t *testing.T) {
	a, b, c := abc(t)
	record(t, a, "a-1")
	record(t, b, "b-1")
	record(t, c, "c-1")
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, a.srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	r := &fedReader{s: s}
	record(t, b, "b-2")
	record(t, a, "a-2")
	r.until(t, 5*time.Second, "a-2", query("a-2"))
	r.drain(500 * time.Millisecond)
	known := map[string]bool{"id": true, "time": true, "kind": true, "query": true, "project": true, "token": true,
		"client_version": true, "trace_id": true, "miss": true, "replica": true, "late": true}
	if len(r.seen) != 3 || r.seen[1].Type != "live" || r.seen[1].Data != "{}" {
		t.Fatalf("the local stream: %+v, want a-1, the live marker, a-2", r.seen)
	}
	events := []sseEvent{r.seen[0], r.seen[2]}
	for i, e := range events {
		if e.Type != "message" {
			t.Errorf("event %d is of type %q on the local stream: %s", i, e.Type, e.Data)
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(e.Data), &m); err != nil {
			t.Fatalf("event %d is not JSON: %q", i, e.Data)
		}
		for k := range m {
			if !known[k] {
				t.Errorf("the local stream's event carries the new field %q: %s", k, e.Data)
			}
		}
		w := decodeEvent(t, e)
		if *w.ID != uint64(i+1) || !strings.HasPrefix(w.Query, "a-") {
			t.Errorf("event %d: id %d query %q, want id %d from this replica only", i, *w.ID, w.Query, i+1)
		}
		if late, ok := m["late"].(bool); !ok || late {
			t.Errorf("the local stream's event has late %v, want it sent and false: %s", m["late"], e.Data)
		}
		if m["replica"] != "goldie-a" {
			t.Errorf("the local stream's event has replica %v, want this replica's name goldie-a: %s", m["replica"], e.Data)
		}
	}
	for _, peer := range []*replicaNode{b, c} {
		if got := peer.sw.peerRequests(); len(got) != 0 {
			t.Errorf("%s was asked %d times for a stream without replicas=all", peer.name, len(got))
		}
	}
}

// Criterion 1: without GOLDIE_ACTIVITY_PEERS, replicas=all is the local
// stream: 200, this replica's events named after it, no status, nothing
// resolved, and live events not held for the (here 5s) reorder window.
func TestReplicasAllWithoutPeersIsTheLocalStream(t *testing.T) {
	n := startReplicas(t, nodeSpec{name: "goldie-solo", noPeers: true, window: 5 * time.Second})[0]
	useResolver(t, func(context.Context, string) ([]activityPeer, error) {
		t.Error("a replica without GOLDIE_ACTIVITY_PEERS resolved peers")
		return nil, errors.New(resolverFailure)
	})
	record(t, n, "solo-hist")
	r := openFederated(t, t.Context(), n, bearer(operatorToken))
	if e := mustEvent(t, r.until(t, 5*time.Second, "the history", query("solo-hist"))); e.replica() != "goldie-solo" || *e.ID != 1 {
		t.Errorf("history event replica %q id %d, want goldie-solo and 1", e.replica(), *e.ID)
	}
	record(t, n, "solo-live")
	start := time.Now()
	e := mustEvent(t, r.until(t, 5*time.Second, "the live event", query("solo-live")))
	if d := time.Since(start); d > time.Second {
		t.Errorf("the live event took %v: without peers there is nothing to reorder, so it is not held for the window", d)
	}
	if e.replica() != "goldie-solo" || e.late() {
		t.Errorf("live event replica %q late %v, want goldie-solo, not late", e.replica(), e.late())
	}
	r.drain(300 * time.Millisecond)
	if s := statuses(r.seen); len(s) != 0 {
		t.Errorf("replica_status without peers: %+v", s)
	}
}

// Criteria 3 and 6: a peer is down only once its stream closes: replica_status
// down with its last-seen time. The other replicas' events keep flowing
// meanwhile. When it is back, the serving replica reconnects on its own
// (backoff, Merger.Add): one up status, the event recorded while it was away
// arrives marked late (it is older than the window), and the events already
// shown are not shown again.
func TestFederatedActivityShowsAPeerGoingDownAndRecovering(t *testing.T) {
	a, b, c := abc(t)
	record(t, b, "b-before")
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	r.until(t, 10*time.Second, "b-before", query("b-before"))
	record(t, b, "b-up")
	r.until(t, 5*time.Second, "b-up", query("b-up"))

	b.sw.set(modeRefuse)
	b.srv.CloseClientConnections()
	down := mustStatus(t, r.until(t, 10*time.Second, "goldie-b down", status("goldie-b", false)))
	if *down.Replica != "goldie-b" {
		t.Errorf("down status names %q, want goldie-b", *down.Replica)
	}

	record(t, a, "a-during")
	record(t, c, "c-during")
	r.until(t, 5*time.Second, "a-during", query("a-during"))
	r.until(t, 5*time.Second, "c-during", query("c-during"))
	record(t, b, "b-during")
	time.Sleep(3 * testWindow)

	b.sw.set(modeUp)
	r.until(t, recoverWithin, "goldie-b up again", status("goldie-b", true))
	late := mustEvent(t, r.until(t, 10*time.Second, "b-during", query("b-during")))
	if !late.late() || late.replica() != "goldie-b" {
		t.Errorf("b-during: late %v replica %q, want late from goldie-b: it reached the merger after its window", late.late(), late.replica())
	}
	record(t, b, "b-after")
	if e := mustEvent(t, r.until(t, 5*time.Second, "b-after", query("b-after"))); e.late() {
		t.Error("b-after, recorded after the recovery, is marked late")
	}
	r.drain(500 * time.Millisecond)

	for _, q := range []string{"b-before", "b-up", "b-during", "a-during", "c-during", "b-after"} {
		if n := len(eventsFor(r.seen, q)); n != 1 {
			t.Errorf("%s shown %d times, want once: events are deduped by replica and id", q, n)
		}
	}
	var ups, downs int
	for _, e := range statuses(r.seen) {
		s := mustStatus(t, e)
		if *s.Replica != "goldie-b" {
			t.Errorf("a status for %s, which never went down", *s.Replica)
		}
		if *s.Up {
			ups++
		} else {
			downs++
		}
	}
	if ups != 1 || downs != 1 {
		t.Errorf("goldie-b: %d up and %d down statuses, want exactly one of each", ups, downs)
	}
	assertNoAddress(t, "the merged stream", r.s.bytes(), a, b, c)
}

// Criterion 6: a peer that sends only keepalives is up, however long it has
// no events; a peer that falls silent (no keepalive either) is down once the
// idle read deadline closes its stream, and the others are not held up.
func TestAQuietPeerStaysUpAndASilentOneGoesDown(t *testing.T) {
	a, b, c := abc(t)
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	record(t, c, "c-1")
	r.until(t, 5*time.Second, "c-1", query("c-1"))
	r.drain(3 * testPeerIdle)
	if s := statuses(r.seen); len(s) != 0 {
		t.Fatalf("a status while every peer sent keepalives: %+v", s)
	}

	c.sw.freeze()
	record(t, b, "b-while-c-is-silent")
	r.until(t, 5*time.Second, "b-while-c-is-silent", query("b-while-c-is-silent"))
	r.until(t, 5*testPeerIdle, "goldie-c down", status("goldie-c", false))
	record(t, a, "a-after")
	r.until(t, 5*time.Second, "a-after", query("a-after"))
	assertNoAddress(t, "the merged stream", r.s.bytes(), a, b, c)
}

// Criterion 3: a peer that never answers does not hold the merged stream: its
// headers and the other replicas' history come at once, the slow peer is
// reported down, live events flow, and once it answers it is up.
func TestASlowPeerDoesNotBlockTheMergedStream(t *testing.T) {
	a, b, c := abc(t)
	c.sw.set(modeHang)
	record(t, a, "a-hist")
	record(t, b, "b-hist")
	start := time.Now()
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	r.until(t, 5*time.Second, "a-hist", query("a-hist"))
	r.until(t, 5*time.Second, "b-hist", query("b-hist"))
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the history took %v with one peer hanging", d)
	}
	r.until(t, 10*time.Second, "goldie-c down", status("goldie-c", false))
	record(t, b, "b-live")
	r.until(t, 5*time.Second, "b-live", query("b-live"))

	c.sw.set(modeUp)
	r.until(t, recoverWithin, "goldie-c up", status("goldie-c", true))
	record(t, c, "c-live")
	r.until(t, 5*time.Second, "c-live", query("c-live"))
	assertNoAddress(t, "the merged stream", r.s.bytes(), a, b, c)
}

// Criteria 2 and 3: each peer re-checks the forwarded credential. A peer
// where the caller's token is only a reader (403) or unknown (401) shows as
// that replica down, never as the merged stream failing; the refusal shows no
// peer address and nothing of the peer's answer.
func TestAPeerThatRefusesTheCallersTokenShowsAsDownAndLeaksNothing(t *testing.T) {
	for name, bTokens := range map[string]string{
		"reader on the peer":  "ci-reader:reader:" + readerToken + "\nci-op-demoted:reader:" + operatorToken,
		"unknown to the peer": "ci-reader:reader:" + readerToken,
	} {
		t.Run(name, func(t *testing.T) {
			n := startReplicas(t, nodeSpec{name: "goldie-a"}, nodeSpec{name: "goldie-b", tokens: bTokens}, nodeSpec{name: "goldie-c"})
			a, b, c := n[0], n[1], n[2]
			record(t, b, "b-hidden")
			record(t, c, "c-hist")

			// What the peer answers the caller's token directly.
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, activityPath, nil)
			bearer(operatorToken)(req)
			rec := httptest.NewRecorder()
			b.pub.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
				t.Fatalf("fixture: goldie-b answers the caller's token %d, want 401 or 403", rec.Code)
			}
			var env struct {
				Error struct {
					Code, Message string
					Details       struct{ Hint string }
				}
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatal(err)
			}

			r := openFederated(t, t.Context(), a, bearer(operatorToken))
			r.until(t, 10*time.Second, "goldie-b down", status("goldie-b", false))
			r.until(t, 5*time.Second, "c-hist", query("c-hist"))
			record(t, a, "a-live")
			record(t, c, "c-live")
			r.until(t, 5*time.Second, "a-live", query("a-live"))
			r.until(t, 5*time.Second, "c-live", query("c-live"))
			r.drain(300 * time.Millisecond)

			for _, e := range r.seen {
				if w, ok := asEvent(e); ok && w.replica() == "goldie-b" {
					t.Errorf("an event of the refusing peer: %s", e.Data)
				}
			}
			raw := r.s.bytes()
			for _, s := range []string{env.Error.Message, env.Error.Details.Hint, env.Error.Code, readerToken, operatorToken} {
				if s != "" && strings.Contains(string(raw), s) {
					t.Errorf("the merged stream carries %q from the peer's refusal or a token:\n%s", s, raw)
				}
			}
			assertNoAddress(t, "the merged stream", raw, a, b, c)
			got := b.sw.peerRequests()
			if len(got) == 0 || got[0].auth != "Bearer "+operatorToken {
				t.Errorf("goldie-b saw %+v, want the caller's own credential to re-check", got)
			}
		})
	}
}

// Criterion 2: the serving replica checks the caller first: a reader is 403
// and an unknown token 401, with ?replicas=all too, and no peer is asked, so
// no credential is forwarded.
func TestAFederatedStreamIsForOperatorsAndForwardsNothingOtherwise(t *testing.T) {
	a, b, c := abc(t)
	for who, mutate := range map[string]func(*http.Request){
		"reader":   bearer(readerToken),
		"unknown":  bearer("unknown-token-0123456789abcdef0123456789"),
		"no token": nil,
	} {
		resp, body := do(t, http.MethodGet, a.srv.URL+federatedPath, mutate)
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401 or 403: %s", who, resp.StatusCode, body)
			continue
		}
		assertEnvelope(t, body, "")
	}
	time.Sleep(300 * time.Millisecond)
	for _, peer := range []*replicaNode{b, c} {
		if got := peer.sw.peerRequests(); len(got) != 0 {
			t.Errorf("%s was asked %d times for a caller the serving replica refused", peer.name, len(got))
		}
	}
}

// trap records every request: a credential must never reach it.
func newTrap(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { mu.Lock(); defer mu.Unlock(); return n }
}

// Criterion 4: the credential goes only to addresses resolved from the
// configured name. No part of the request moves it: not the Host header, a
// query parameter, an X-Forwarded-* or Forwarded header, nor a path-override
// header. Each peer gets it at its resolved address.
func TestTheForwardedCredentialGoesOnlyToResolvedPeers(t *testing.T) {
	a, b, c := abc(t)
	trap, trapHits := newTrap(t)
	trapHost := strings.TrimPrefix(trap.URL, "http://")
	record(t, b, "b-hist")
	record(t, c, "c-hist")

	hostile := url.Values{"replicas": {"all"}}
	for _, k := range []string{"peers", "peer", "addr", "address", "host", "replica", "url", "target", "GOLDIE_ACTIVITY_PEERS"} {
		hostile.Set(k, trap.URL)
	}
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, a.srv.URL+activityPath+"?"+hostile.Encode(), func(r *http.Request) {
		bearer(operatorToken)(r)
		r.Host = trapHost
		for _, h := range []string{"X-Forwarded-Host", "X-Forwarded-Server", "X-Original-Host", "X-Host"} {
			r.Header.Set(h, trapHost)
		}
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.Header.Set("X-Forwarded-Proto", "http")
		r.Header.Set("X-Forwarded-Port", trap.URL[strings.LastIndex(trap.URL, ":")+1:])
		r.Header.Set("Forwarded", "for=127.0.0.1;host="+trapHost+";proto=http")
		r.Header.Set("X-Original-URL", trap.URL+activityPath)
		r.Header.Set("X-Rewrite-URL", trap.URL+activityPath)
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	r := &fedReader{s: s}
	r.until(t, 10*time.Second, "b-hist", query("b-hist"))
	r.until(t, 10*time.Second, "c-hist", query("c-hist"))
	r.drain(500 * time.Millisecond)
	if n := trapHits(); n != 0 {
		t.Errorf("the address named in the request got %d requests: the request redirected the credential", n)
	}
	for _, peer := range []*replicaNode{b, c} {
		for _, got := range peer.sw.peerRequests() {
			if got.host != peer.host() {
				t.Errorf("%s got Host %q, want its resolved address", peer.name, got.host)
			}
		}
	}
	assertNoAddress(t, "the merged stream", r.s.bytes(), a, b, c)
	if strings.Contains(string(r.s.bytes()), trapHost) {
		t.Errorf("the merged stream echoes the address from the request")
	}
}

// Criterion 4: a peer that answers with a redirect does not get the
// credential sent on: the target sees nothing, the peer shows as down, the
// merged stream goes on, and nothing of the redirect reaches the client.
func TestAPeerRedirectNeverCarriesTheCredentialOn(t *testing.T) {
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			a, b, c := abc(t)
			trap, trapHits := newTrap(t)
			b.sw.redirect(code, trap.URL)
			record(t, c, "c-hist")
			r := openFederated(t, t.Context(), a, bearer(operatorToken))
			r.until(t, 10*time.Second, "goldie-b down", status("goldie-b", false))
			r.until(t, 5*time.Second, "c-hist", query("c-hist"))
			r.drain(500 * time.Millisecond)
			if n := trapHits(); n != 0 {
				t.Errorf("the redirect target got %d requests: the credential was carried on", n)
			}
			raw := r.s.bytes()
			assertNoAddress(t, "the merged stream", raw, a, b, c)
			if strings.Contains(string(raw), strings.TrimPrefix(trap.URL, "http://")) {
				t.Errorf("the merged stream carries the redirect target:\n%s", raw)
			}
		})
	}
}

// Criterion 4: peer requests are always local-only. Every replica is
// configured with the same name, which resolves to all three, a cycle; one
// merged stream on goldie-a costs exactly one stream on goldie-b and one on
// goldie-c, none on goldie-a, and the count does not grow.
func TestPeerRequestsAreLocalOnlySoACycleDoesNotFanOut(t *testing.T) {
	a, b, c := abc(t)
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	for _, n := range []*replicaNode{a, b, c} {
		record(t, n, n.name+"-live")
	}
	for _, n := range []*replicaNode{a, b, c} {
		r.until(t, 10*time.Second, n.name+"-live", query(n.name+"-live"))
	}
	r.drain(2 * time.Second)
	for _, n := range []*replicaNode{a, b, c} {
		want := 1
		if n == a {
			want = 0
		}
		got := n.sw.peerRequests()
		if len(got) != want {
			t.Errorf("%s saw %d peer requests, want %d: a peer request fanned out again", n.name, len(got), want)
		}
		for _, p := range got {
			if p.query.Get("replicas") == "all" {
				t.Errorf("%s was asked for replicas=all by a replica", n.name)
			}
		}
	}
	for _, n := range []*replicaNode{a, b, c} {
		if n := len(eventsFor(r.seen, n.name+"-live")); n != 1 {
			t.Errorf("an event shown %d times through the cycle, want once", n)
		}
	}
}

// Criterion 4: federated subscribers are capped below the local cap, and
// one over it is 503 in the error envelope with a hint, refused at once,
// asking no peer. The local stream still opens, a freed slot is taken again,
// and once every merged stream ends no peer holds a stream for it.
func TestFederatedSubscribersAreCappedWith503AndAHint(t *testing.T) {
	if api.MaxFederatedSubscribers < 1 || api.MaxFederatedSubscribers >= api.MaxActivitySubscribers {
		t.Fatalf("MaxFederatedSubscribers = %d, want at least 1 and below MaxActivitySubscribers %d (each costs a stream on every peer)",
			api.MaxFederatedSubscribers, api.MaxActivitySubscribers)
	}
	a, b, c := abc(t)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{Transport: tr}
	var cancels []context.CancelFunc
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()
	for i := range api.MaxFederatedSubscribers {
		ctx, cancel := context.WithCancel(t.Context())
		cancels = append(cancels, cancel)
		resp, _, body := openStream(t, ctx, client, a.srv.URL+federatedPath, bearer(operatorToken))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("merged stream %d of %d: status %d: %s", i+1, api.MaxFederatedSubscribers, resp.StatusCode, body)
		}
	}
	waitPeerRequests(t, b, api.MaxFederatedSubscribers)
	waitPeerRequests(t, c, api.MaxFederatedSubscribers)

	resp, s, body := openStream(t, t.Context(), client, a.srv.URL+federatedPath, bearer(operatorToken))
	if s != nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("one over the federated cap: status %d, want 503", resp.StatusCode)
	}
	contracttest.Conforms(t, resp, body)
	code, hint := errorHint(t, body)
	if code == "not_ready" || hint == "" {
		t.Errorf("over the cap: code %q hint %q, want a code that is not not_ready and a hint", code, hint)
	}
	for kind, re := range leaks {
		if m := re.FindString(string(body)); m != "" {
			t.Errorf("over the cap: the body contains a %s (%q): %s", kind, m, body)
		}
	}
	time.Sleep(300 * time.Millisecond)
	for _, peer := range []*replicaNode{b, c} {
		if got := len(peer.sw.peerRequests()); got != api.MaxFederatedSubscribers {
			t.Errorf("%s saw %d peer requests after a refused merged stream, want %d", peer.name, got, api.MaxFederatedSubscribers)
		}
	}
	if resp, s, body := openStream(t, t.Context(), client, a.srv.URL+activityPath, bearer(operatorToken)); resp.StatusCode != http.StatusOK || s == nil {
		t.Errorf("the local stream with the federated cap full: status %d, want 200: %s", resp.StatusCode, body)
	}

	cancels[0]()
	cancels = cancels[1:]
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithCancel(t.Context())
		resp, s, _ := openStream(t, ctx, client, a.srv.URL+federatedPath, bearer(operatorToken))
		if resp.StatusCode == http.StatusOK && s != nil {
			cancels = append(cancels, cancel)
			break
		}
		cancel()
		if time.Now().After(deadline) {
			t.Fatalf("a freed federated slot was not taken again: status %d", resp.StatusCode)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, c := range cancels {
		c()
	}
	cancels = nil
	waitHubEmpty(t, b.pub.Activity())
	waitHubEmpty(t, c.pub.Activity())
}

func waitPeerRequests(t *testing.T, n *replicaNode, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(n.sw.peerRequests()) < want {
		if time.Now().After(deadline) {
			t.Fatalf("%s saw %d peer requests, want %d", n.name, len(n.sw.peerRequests()), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Criteria 4 and 6: a replica name, never an address, reaches the client.
// A resolver failure leaves the local stream and shows none of its words; an
// unreachable peer and a hanging one are named by their replica names in
// replica_status; no event, status or error carries a peer's address, the
// configured name, or a dialer's words.
func TestNoPeerAddressAppearsInAnyClientFacingOutput(t *testing.T) {
	a, b, c := abc(t)
	record(t, a, "a-hist")

	useResolver(t, func(context.Context, string) ([]activityPeer, error) {
		return nil, errors.New(resolverFailure)
	})
	first, leave := context.WithCancel(t.Context())
	r := openFederated(t, first, a, bearer(operatorToken))
	r.until(t, 5*time.Second, "a-hist despite the resolver failing", query("a-hist"))
	r.drain(time.Second)
	assertNoAddress(t, "the merged stream with a failing resolver", r.s.bytes(), a, b, c)
	leave()
	waitReadEnds(t, r.s)
	waitHubEmpty(t, a.pub.Activity()) // the stream has ended on the server too

	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()
	c.sw.set(modeHang)
	useResolver(t, func(_ context.Context, name string) ([]activityPeer, error) {
		if name != peersName {
			return nil, errors.New(resolverFailure)
		}
		return []activityPeer{
			{Name: "goldie-a", URL: a.srv.URL},
			{Name: "goldie-b", URL: b.srv.URL},
			{Name: "goldie-gone", URL: goneURL},
			{Name: "goldie-c", URL: c.srv.URL},
		}, nil
	})
	record(t, b, "b-hist")
	r = openFederated(t, t.Context(), a, bearer(operatorToken))
	r.until(t, 10*time.Second, "goldie-gone down", status("goldie-gone", false))
	r.until(t, 10*time.Second, "goldie-c down", status("goldie-c", false))
	r.until(t, 5*time.Second, "b-hist", query("b-hist"))
	r.drain(time.Second)
	raw := r.s.bytes()
	assertNoAddress(t, "the merged stream", raw, a, b, c)
	if strings.Contains(string(raw), strings.TrimPrefix(goneURL, "http://")) {
		t.Errorf("the merged stream carries the unreachable peer's address:\n%s", raw)
	}
	names := map[string]bool{"goldie-a": true, "goldie-b": true, "goldie-c": true, "goldie-gone": true}
	for _, e := range r.seen {
		var name string
		switch e.Type {
		case "replica_status":
			name = *mustStatus(t, e).Replica
		case "message":
			name = mustEvent(t, e).replica()
		default:
			continue
		}
		if !names[name] {
			t.Errorf("a %s names %q, which is not a configured replica name", e.Type, name)
		}
		assertNameIsNoAddress(t, name)
	}
}

// Spoofing: a peer that stamps its events with another replica's name
// cannot hide that replica's events (ids collide, dedup is by replica and
// id) or pass its own off as that replica's.
func TestAPeerClaimingAnotherReplicasNameCannotHideItsEvents(t *testing.T) {
	n := startReplicas(t, nodeSpec{name: "goldie-a"}, nodeSpec{name: "goldie-b"}, nodeSpec{name: "goldie-c", claims: "goldie-a"})
	a, c := n[0], n[2]
	record(t, c, "c-spoof-1")
	record(t, a, "a-1")
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	r.until(t, 10*time.Second, "a-1", query("a-1"))
	record(t, c, "c-spoof-2")
	record(t, a, "a-2")
	r.until(t, 10*time.Second, "a-2", query("a-2"))
	r.drain(time.Second)
	for _, q := range []string{"a-1", "a-2"} {
		got := eventsFor(r.seen, q)
		if len(got) != 1 || got[0].replica() != "goldie-a" {
			t.Errorf("%s: %+v, want it once, from goldie-a", q, got)
		}
	}
	for _, q := range []string{"c-spoof-1", "c-spoof-2"} {
		for _, e := range eventsFor(r.seen, q) {
			if e.replica() == "goldie-a" {
				t.Errorf("%s is shown as goldie-a's: a peer passed its events off as another replica's", q)
			}
		}
	}
}

// A merged stream holds a stream on every peer only while it is open: when
// the client leaves, and when the server starts to stop (the merged stream
// ends cleanly), every peer stream ends with it.
func TestAFederatedStreamReleasesEveryPeerWhenItEnds(t *testing.T) {
	a, b, c := abc(t)
	ctx, cancel := context.WithCancel(t.Context())
	r := openFederated(t, ctx, a, bearer(operatorToken))
	record(t, b, "b-1")
	r.until(t, 10*time.Second, "b-1", query("b-1"))
	cancel()
	waitReadEnds(t, r.s)
	waitHubEmpty(t, b.pub.Activity())
	waitHubEmpty(t, c.pub.Activity())

	stopCtx, stop := context.WithCancel(t.Context())
	defer stop()
	a.pub.Start(stopCtx, platform.NewRegistry(0))
	r = openFederated(t, t.Context(), a, bearer(operatorToken))
	record(t, c, "c-1")
	r.until(t, 10*time.Second, "c-1", query("c-1"))
	stop()
	select {
	case err := <-r.s.done:
		if err != nil {
			t.Errorf("the merged stream ended with %v at shutdown, want a clean end", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the merged stream was still open 5s after Start's context ended")
	}
	waitHubEmpty(t, a.pub.Activity())
	waitHubEmpty(t, b.pub.Activity())
	waitHubEmpty(t, c.pub.Activity())
}

// Criteria 2 and 6, spec first: the served document declares the replicas
// query parameter (an enum of only "all"), the required replica and late
// fields of ActivityEvent, and among the stream's data the closed schemas
// ActivityReplicaStatus {replica, up, last_seen} and ActivityLive {}. Its
// description says clients dedupe by replica and id, not id alone, and its
// examples name no address.
func TestServedSpecDeclaresTheFederatedActivityShape(t *testing.T) {
	doc := specDoc(t)
	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths[activityPath].(map[string]any)
	op, _ := item["get"].(map[string]any)
	if op == nil {
		t.Fatalf("no GET %s in the spec", activityPath)
	}
	var replicas map[string]any
	params, _ := op["parameters"].([]any)
	if itemParams, ok := item["parameters"].([]any); ok {
		params = append(params, itemParams...)
	}
	for _, p := range params {
		pm := contracttest.ResolveRef(doc, p)
		if pm["name"] == "replicas" && pm["in"] == "query" {
			replicas = pm
		}
	}
	if replicas == nil {
		t.Fatalf("GET %s declares no replicas query parameter", activityPath)
	}
	if req, _ := replicas["required"].(bool); req {
		t.Error("the replicas parameter is required; without it a client gets the local stream")
	}
	schema := contracttest.ResolveRef(doc, replicas["schema"])
	if enum, _ := schema["enum"].([]any); len(enum) != 1 || enum[0] != "all" {
		t.Errorf("the replicas parameter's schema %v, want an enum of only \"all\"", schema)
	}

	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	event, _ := schemas["ActivityEvent"].(map[string]any)
	props, _ := event["properties"].(map[string]any)
	for field, typ := range map[string]string{"replica": "string", "late": "boolean"} {
		p := contracttest.ResolveRef(doc, props[field])
		if p == nil || p["type"] != typ {
			t.Errorf("ActivityEvent.%s is %v, want a %s", field, p, typ)
		}
	}
	checkExamples(t, "ActivityEvent.replica", contracttest.ResolveRef(doc, props["replica"]))
	componentSchema(t, "ActivityEvent", activityEventFields) // closed, every field required
	for _, f := range []string{"replica", "late"} {
		if !slices.Contains(activityEventFields, f) {
			t.Errorf("the wire fields %v do not include %s", activityEventFields, f)
		}
	}
	componentSchema(t, "ActivityReplicaStatus", []string{"last_seen", "replica", "up"})
	componentSchema(t, "ActivityLive", nil)

	responses, _ := op["responses"].(map[string]any)
	content, _ := contracttest.ResolveRef(doc, responses["200"])["content"].(map[string]any)
	stream, _ := content["text/event-stream"].(map[string]any)
	streamSchema, _ := stream["schema"].(map[string]any)
	cs, _ := streamSchema["contentSchema"].(map[string]any)
	oneOf, _ := cs["oneOf"].([]any)
	refs := map[string]bool{}
	for _, s := range oneOf {
		if m, _ := s.(map[string]any); m != nil {
			ref, _ := m["$ref"].(string)
			refs[ref] = true
		}
	}
	for _, name := range []string{"ActivityEvent", "ActivityDropped", "ActivityReplicaStatus", "ActivityLive"} {
		if !refs["#/components/schemas/"+name] {
			t.Errorf("the stream's contentSchema oneOf %v does not list %s", oneOf, name)
		}
	}
	status, _ := schemas["ActivityReplicaStatus"].(map[string]any)
	if status == nil {
		t.Fatal("the spec has no components/schemas/ActivityReplicaStatus")
	}
	sp, _ := status["properties"].(map[string]any)
	for field, typ := range map[string]string{"replica": "string", "up": "boolean", "last_seen": "string"} {
		p := contracttest.ResolveRef(doc, sp[field])
		if p == nil || p["type"] != typ {
			t.Errorf("replica_status.%s is %v, want a %s", field, p, typ)
		}
	}
	if f := contracttest.ResolveRef(doc, sp["last_seen"])["format"]; f != "date-time" {
		t.Errorf("replica_status.last_seen format %v, want date-time", f)
	}
	req, _ := status["required"].([]any)
	for _, field := range []string{"replica", "up", "last_seen"} {
		found := false
		for _, r := range req {
			found = found || r == field
		}
		if !found {
			t.Errorf("replica_status does not require %s", field)
		}
	}
	checkExamples(t, "replica_status.replica", contracttest.ResolveRef(doc, sp["replica"]))

	desc, _ := op["description"].(string)
	for _, want := range []string{"replicas=all", "replica_status", "late", "`live`", "`replica` and `id`"} {
		if !strings.Contains(desc, want) {
			t.Errorf("GET %s's description does not explain %q", activityPath, want)
		}
	}
	if strings.Contains(desc, "dedupe by `id`") {
		t.Errorf("GET %s's description still says clients dedupe by `id` alone; ids repeat across replicas", activityPath)
	}
	if m := leaks["IPv4 address"].FindString(desc); m != "" {
		t.Errorf("GET %s's description names an address (%q)", activityPath, m)
	}
}

// checkExamples fails when an example of a replica name looks like an
// address.
func checkExamples(t *testing.T, where string, schema map[string]any) {
	t.Helper()
	examples, _ := schema["examples"].([]any)
	if ex, ok := schema["example"]; ok {
		examples = append(examples, ex)
	}
	if len(examples) == 0 {
		t.Errorf("%s has no example", where)
	}
	for _, ex := range examples {
		s, _ := ex.(string)
		if host, _, err := net.SplitHostPort(s); err == nil || net.ParseIP(s) != nil || net.ParseIP(host) != nil {
			t.Errorf("%s example %q is an address", where, s)
		}
		assertNameIsNoAddress(t, s)
	}
}
