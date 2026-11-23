package api

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/thschue/goldie/internal/contract"
)

const (
	// MaxFederatedSubscribers caps the concurrent ?replicas=all streams of one
	// replica: each costs a stream on every peer.
	MaxFederatedSubscribers = 8
	// MaxFederatedPeers is the most peers one merged stream opens.
	MaxFederatedPeers = 16

	// peerOpenWait is how long the headers wait for the peers to open; a
	// slower one starts as down. peerSettle is how long a peer that breaks
	// right after its live marker gets to show as down before the history.
	peerOpenWait   = 2 * time.Second
	peerSettle     = 300 * time.Millisecond
	resolveTimeout = 5 * time.Second
	// The backoff between attempts to reopen a down peer; a connection that
	// lasted reconnectStable starts it over.
	reconnectMin    = 250 * time.Millisecond
	reconnectMax    = 5 * time.Second
	reconnectStable = 10 * time.Second
)

// Peer is another replica: Name is all a client ever sees of it. Open opens
// the peer's own stream (never its merged one) with the caller's token; live
// closes when that stream ends or falls silent. Its error names no address.
type Peer struct {
	Name string
	Open func(ctx context.Context, token string) (history []contract.ActivityEvent, live <-chan contract.ActivityEvent, err error)
}

// Federation is what a Server needs to serve ?replicas=all. Resolve runs on
// every federated request and every reconnect, with no input of the request;
// its error is never shown. Window is the Merger's reorder window.
type Federation struct {
	Resolve func(ctx context.Context) ([]Peer, error)
	Window  time.Duration
}

// FederateActivity makes the Server merge the peers' streams for
// ?replicas=all. Without it, or without peers, that is the local stream.
func FederateActivity(f Federation) ServerOption { return func(s *Server) { s.fed = &f } }

// tooManyFederated is the 503 envelope at MaxFederatedSubscribers.
var tooManyFederated = envelope("too_many_streams", "too many merged activity streams are open",
	"Each stream with replicas=all costs a stream on every replica. Close another one, or retry in a minute.")

// badReplicas is the 400 envelope for a replicas that is not one "all".
var badReplicas = envelope("bad_request", "invalid replicas",
	"Use replicas=all, once, or leave it out for this replica's stream.")

// activityCall is what activityRequest hands the handler: replicas=all, and
// the caller's bearer token, which only a merged stream forwards, only to the
// peers the operator configured.
type activityCall struct {
	all   bool
	token string
}

type activityCallKey struct{}

// activityRequest checks replicas on the raw query (the generated binder
// takes any string, and one value of a repeated one) and hands the handler
// the caller's token. The route's guard has checked the operator role.
func activityRequest(next contract.StrictHandlerFunc, op string) contract.StrictHandlerFunc {
	if op != "GetActivity" {
		return next
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		var call activityCall
		if vals, ok := r.URL.Query()["replicas"]; ok {
			if len(vals) != 1 || vals[0] != string(contract.All) {
				return contract.GetActivity400JSONResponse(badReplicas), nil
			}
			call.all = r.Method != http.MethodHead // a HEAD only needs the headers
		}
		call.token, _ = bearerToken(r.Header.Get("Authorization"))
		return next(context.WithValue(ctx, activityCallKey{}, call), w, r, req)
	}
}

// federatedStream answers ?replicas=all: the local subscription and one per
// peer, merged, holding a federated slot until the stream ends. With no peer
// it is the plain local stream.
func (s *Server) federatedStream(ctx context.Context, token string) (contract.GetActivityResponseObject, error) {
	if s.fedOpen.Add(1) > MaxFederatedSubscribers {
		s.fedOpen.Add(-1)
		return contract.GetActivity503JSONResponse(tooManyFederated), nil
	}
	var once sync.Once
	release := func() { once.Do(func() { s.fedOpen.Add(-1) }) }
	history, live, cancel, ok := s.activity.TrySubscribe()
	if !ok {
		release()
		return contract.GetActivity503JSONResponse(tooManyStreams), nil
	}
	local := s.activity.Replica()
	peers := usablePeers(ctx, s.fed.Resolve, local)
	if len(peers) == 0 {
		release()
		return &activityStream{ctx: ctx, history: history, live: live, cancel: cancel,
			keepalive: s.keepalive, done: s.streamsDone}, nil
	}
	life, stop := context.WithCancel(ctx)
	f := &federation{life: life, token: token, merger: NewMerger(MergeConfig{Window: s.fed.Window}), peers: peers, local: local, resolve: s.fed.Resolve}
	inputs := append([]MergeInput{{Name: local, History: history, Live: live}}, f.openAll()...)
	merged, out := f.merger.Start(life, inputs...)
	for _, p := range peers {
		go f.supervise(p.Name, f.conns[p.Name])
	}
	sleep(ctx, peerSettle) // a peer that breaks as it opens is down before the history, not after it
	st := &federatedStream{merger: f.merger, out: out, merged: merged}
	st.ctx, st.keepalive, st.done = ctx, s.keepalive, s.streamsDone
	st.cancel = func() { stop(); cancel(); release() }
	for done := false; !done; { // the down peers' statuses, emitted as it started
		select {
		case it, open := <-out:
			if done = !open; open {
				st.first = append(st.first, it)
			}
		default:
			done = true
		}
	}
	return st, nil
}

// usablePeers resolves the peers: at most MaxFederatedPeers, each name once,
// never this replica. A failed resolution is no peers and shows a client
// nothing.
func usablePeers(ctx context.Context, resolve func(context.Context) ([]Peer, error), local string) []Peer {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	all, err := resolve(ctx)
	if err != nil {
		return nil
	}
	var peers []Peer
	for _, p := range all {
		if p.Name == "" || p.Name == local || p.Open == nil || slices.ContainsFunc(peers, func(q Peer) bool { return q.Name == p.Name }) {
			continue
		}
		if len(peers) == MaxFederatedPeers {
			break
		}
		peers = append(peers, p)
	}
	return peers
}

// federation is the running side of one merged stream: it opens the peers,
// and opens a down one again, until life ends.
type federation struct {
	life    context.Context
	token   string
	merger  *Merger
	peers   []Peer
	local   string
	resolve func(context.Context) ([]Peer, error)
	conns   map[string]*peerConn // the first connections, by peer name
}

// peerConn is one open peer stream, relayed so its end is seen.
type peerConn struct {
	history []Event
	live    chan Event
	done    chan struct{} // closed when the peer's stream has ended
	at      time.Time
}

// openAll opens every peer at once, waiting at most peerOpenWait: one that
// has not answered is an input without a Live, down until supervise reopens it.
func (f *federation) openAll() []MergeInput {
	conns := make([]*peerConn, len(f.peers))
	done := make([]chan struct{}, len(f.peers))
	cancels := make([]context.CancelFunc, len(f.peers))
	for i, p := range f.peers {
		var ctx context.Context
		ctx, cancels[i] = context.WithCancel(f.life)
		done[i] = make(chan struct{})
		go func() { defer close(done[i]); conns[i] = f.open(ctx, cancels[i], p) }()
	}
	wait, stop := context.WithTimeout(f.life, peerOpenWait)
	defer stop()
	f.conns = map[string]*peerConn{}
	inputs := make([]MergeInput, 0, len(f.peers))
	for i, p := range f.peers {
		select {
		case <-done[i]:
		case <-wait.Done():
			cancels[i]() // still opening: give up
			<-done[i]
		}
		in := MergeInput{Name: p.Name}
		if c := conns[i]; c != nil {
			in.History, in.Live = c.history, c.live
			f.conns[p.Name] = c
		}
		inputs = append(inputs, in)
	}
	return inputs
}

// open opens p's stream under ctx, nil when it cannot be opened.
func (f *federation) open(ctx context.Context, cancel context.CancelFunc, p Peer) *peerConn {
	history, live, err := p.Open(ctx, f.token)
	if err != nil {
		cancel()
		return nil
	}
	c := &peerConn{live: make(chan Event, ActivitySubscriberBuffer), done: make(chan struct{}), at: time.Now()}
	for _, e := range history {
		c.history = append(c.history, peerEvent(e))
	}
	go func() {
		defer close(c.done)
		defer close(c.live)
		defer cancel()
		for e := range live {
			select {
			case c.live <- peerEvent(e):
			case <-ctx.Done():
				return
			}
		}
	}()
	return c
}

// supervise waits for the peer's stream to end, then reopens it with backoff
// until the Merger takes it (Merger.Add), resolving the peers anew each time:
// a restarted pod keeps its name and gets a new address.
func (f *federation) supervise(name string, conn *peerConn) {
	backoff := reconnectMin
	for {
		if conn != nil {
			select {
			case <-conn.done:
			case <-f.life.Done():
				return
			}
			if time.Since(conn.at) >= reconnectStable {
				backoff = reconnectMin
			}
		}
		for conn = nil; conn == nil; conn = f.reopen(name) {
			if !sleep(f.life, backoff) {
				return
			}
			backoff = min(2*backoff, reconnectMax)
		}
	}
}

// reopen resolves the peers again, finds name among them and opens its
// stream; nil when that fails or the Merger does not take it.
func (f *federation) reopen(name string) *peerConn {
	peers := usablePeers(f.life, f.resolve, f.local)
	i := slices.IndexFunc(peers, func(p Peer) bool { return p.Name == name })
	if i < 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(f.life)
	c := f.open(ctx, cancel, peers[i])
	if c != nil && !f.merger.Add(MergeInput{Name: name, History: c.history, Live: c.live}) {
		cancel()
		<-c.done
		return nil
	}
	return c
}

// peerEvent is a peer's ActivityEvent as an Event, untrusted: the Merger
// checks its Replica against the peer's name and clears Late.
func peerEvent(e contract.ActivityEvent) Event {
	return Event{
		ID: uint64(max(e.Id, 0)), Replica: e.Replica, Time: e.Time, Kind: EventKind(e.Kind),
		Query: e.Query, Token: e.Token, ClientVersion: e.ClientVersion, Project: e.Project,
		TraceID: e.TraceId, Miss: e.Miss,
	}
}

// sleep waits d, and reports false when ctx ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// federatedStream is the 200 response of a merged stream.
type federatedStream struct {
	activityStream
	merger  *Merger
	out     <-chan MergedItem
	merged  []Event
	first   []MergedItem // emitted while the Merger started: the statuses of down peers
	dropped uint64       // the Merger's drops already reported
}

// VisitGetActivityResponse sends the headers, the statuses of the peers that
// are down, the merged history, the live marker, then merged items and
// keepalives, until the request ends, the Merger stops or EndStreams runs.
func (st *federatedStream) VisitGetActivityResponse(w http.ResponseWriter) error {
	defer st.cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if !flush(rc) || st.head {
		return nil
	}
	var b []byte
	var held []MergedItem // emitted before the live marker, written after it
	for _, it := range st.first {
		if it.Status == nil {
			held = append(held, it)
		} else if !st.write(w, rc, appendStatus(b[:0], it.Status)) {
			return nil
		}
	}
	for chunk := range slices.Chunk(st.merged, historyChunk) {
		b = b[:0]
		for _, e := range chunk {
			b = appendData(b, "", activityEventData(e))
		}
		if !st.write(w, rc, b) {
			return nil
		}
	}
	if !st.write(w, rc, []byte(liveMarker)) {
		return nil
	}
	tick := time.NewTicker(st.keepalive)
	defer tick.Stop()
	for {
		var it MergedItem
		b = b[:0]
		if len(held) > 0 {
			it, held = held[0], held[1:]
		} else {
			select {
			case <-st.ctx.Done():
				return nil
			case <-st.done:
				return nil
			case got, open := <-st.out:
				if !open {
					return nil
				}
				it = got
			case <-tick.C:
				b = append(b, ": keepalive\n\n"...)
			}
		}
		if d := st.merger.Dropped(); d > st.dropped && (it.Event != nil || it.Status != nil) {
			b = appendData(b, "event: dropped\n", contract.ActivityDropped{Dropped: int64(d - st.dropped)}) //nolint:gosec // counts stay far below 2^63
			st.dropped = d
		}
		if it.Status != nil {
			b = appendStatus(b, it.Status)
		} else if it.Event != nil {
			b = appendData(b, "", activityEventData(*it.Event))
		}
		if !st.write(w, rc, b) {
			return nil
		}
	}
}

// appendStatus appends a replica_status event: the replica's name, never an
// address.
func appendStatus(b []byte, s *ReplicaStatus) []byte {
	return appendData(b, "event: replica_status\n", contract.ActivityReplicaStatus{Replica: s.Replica, Up: s.Up, LastSeen: s.LastSeen})
}
