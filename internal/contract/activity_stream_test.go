package contract_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/contract"
)

// Acceptance tests for #245: the client a replica uses to read a peer's local
// activity stream for GET /api/v1/activity?replicas=all (ADR 0022). The
// answer cap of NewAPIClient would cut a stream short, and its request
// timeout would end it, so the client has a streaming method instead. The
// local stream marks the end of its retained history with an SSE event of
// type `live` whose data is `{}` (the ActivityLive schema), sent even when
// there is no history, so a reader can tell history from live:
//
//	// StreamActivity opens the local activity stream (GET /api/v1/activity,
//	// never replicas=all) with the client's token. It reads the history up to
//	// the live marker, which must arrive within idle of the answer
//	// (keepalives do not extend it: an older server sends them but never the
//	// marker) and after at most historyCap events, and returns it with a
//	// channel of the live events after it. A non-200 is an *APIError, a 3xx ErrRedirect (never
//	// followed), a 200 that is not text/event-stream ErrNotAPIAnswer, a
//	// stream that ends, falls silent for idle, or passes the history cap
//	// before its live marker (an older server) an error, and an unreachable
//	// server an error naming no address. History and live hold every event
//	// without a type, skipping comments and other event types. The channel
//	// closes when the body ends, ctx ends, no byte arrives for idle, or one
//	// event is too large. The stream is not capped at MaxResponseBytes and
//	// not ended by the injected client's timeout; idle governs it.
//	func (c *ClientWithResponses) StreamActivity(ctx context.Context, idle time.Duration) (history []ActivityEvent, live <-chan ActivityEvent, err error)
//
// ActivityEvent gains Replica and Late (`replica`, `late`) through ./run
// regen. Revised for the live marker (architect, PR #378): the first version
// of these tests pinned a single channel.

// streamToken is the token the stream client sends.
const streamToken = "stream-client-token-0123456789abcdef01"

// historyCap mirrors api.MaxActivityCapacity, the most events a replica
// retains, so the most a live marker can follow.
const historyCap = 10_000

// liveMarker ends the history.
const liveMarker = "event: live\ndata: {}\n\n"

// eventData is one ActivityEvent as a goldie server writes it.
func eventData(id int, query string) string {
	return fmt.Sprintf(`{"id":%d,"time":"2026-11-23T10:00:%02dZ","kind":"search","query":%q,"project":"","token":"ci-operator","client_version":"","trace_id":"","miss":false,"replica":"goldie-1","late":false}`, id, id%60, query)
}

func sseData(id int, query string) string { return "data: " + eventData(id, query) + "\n\n" }

// sseServer answers every request with 200 text/event-stream, flushes the
// headers, records the request and hands the writer to body.
type sseServer struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []*http.Request
	done chan struct{} // closed when a handler returns
	once sync.Once
}

func newSSEServer(t *testing.T, body func(ctx context.Context, w http.ResponseWriter, flush func())) *sseServer {
	t.Helper()
	s := &sseServer{done: make(chan struct{})}
	stop := make(chan struct{})
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer s.once.Do(func() { close(s.done) })
		s.mu.Lock()
		s.seen = append(s.seen, r.Clone(context.Background()))
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		flush := func() { w.(http.Flusher).Flush() }
		flush()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() {
			select {
			case <-stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		body(ctx, w, flush)
	}))
	t.Cleanup(s.srv.Close)
	t.Cleanup(func() { close(stop); s.srv.CloseClientConnections() })
	return s
}

func (s *sseServer) requests() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request(nil), s.seen...)
}

// streamResult is what StreamActivity returned.
type streamResult struct {
	history []contract.ActivityEvent
	live    <-chan contract.ActivityEvent
	err     error
}

// openActivity calls StreamActivity and fails when it does not return within
// within: reading the history is bounded.
func openActivity(t *testing.T, ctx context.Context, base string, httpc *http.Client, idle, within time.Duration) streamResult {
	t.Helper()
	c := newAPIClient(t, base, streamToken, httpc)
	got := make(chan streamResult, 1)
	go func() {
		h, l, err := c.StreamActivity(ctx, idle)
		got <- streamResult{h, l, err}
	}()
	select {
	case r := <-got:
		if r.err == nil && r.live == nil {
			t.Fatal("StreamActivity returned neither a live channel nor an error")
		}
		if r.err != nil && (r.live != nil || r.history != nil) {
			t.Errorf("StreamActivity returned an error with a history or a channel: %v", r.err)
		}
		return r
	case <-time.After(within):
		t.Fatalf("StreamActivity did not return within %v (idle %v): reading the history is not bounded", within, idle)
		return streamResult{}
	}
}

// open is openActivity expecting a stream.
func open(t *testing.T, ctx context.Context, base string, httpc *http.Client, idle time.Duration) streamResult {
	t.Helper()
	r := openActivity(t, ctx, base, httpc, idle, 10*time.Second+3*idle)
	if r.err != nil {
		t.Fatalf("StreamActivity: %v", r.err)
	}
	return r
}

// recv returns the next live event, or ok false when the channel closed; it
// fails after within.
func recv(t *testing.T, ch <-chan contract.ActivityEvent, within time.Duration) (contract.ActivityEvent, bool) {
	t.Helper()
	select {
	case e, ok := <-ch:
		return e, ok
	case <-time.After(within):
		t.Fatalf("nothing on the stream within %v", within)
		return contract.ActivityEvent{}, false
	}
}

// waitClosed drains ch and fails unless it closes within the time given.
func waitClosed(t *testing.T, ch <-chan contract.ActivityEvent, within time.Duration) []contract.ActivityEvent {
	t.Helper()
	var got []contract.ActivityEvent
	deadline := time.After(within)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, e)
		case <-deadline:
			t.Fatalf("the stream did not close within %v (got %d events)", within, len(got))
		}
	}
}

func queries(events []contract.ActivityEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Query
	}
	return out
}

// The history is the events before the live marker, in the order sent; the
// live events arrive on the channel as they are written, not when the body
// ends. Comments, dropped notices and unknown event types are skipped in
// both; the request is the local stream with the token and the client
// header; the channel closes when the body ends.
func TestStreamActivitySplitsHistoryFromLiveAtTheMarker(t *testing.T) {
	gate := make(chan struct{})
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(sseData(1, "hist-1") +
			": keepalive\n\n" +
			"event: dropped\ndata: {\"dropped\":2}\n\n" +
			"event: future\ndata: " + eventData(97, "unknown type in history") + "\n\n" +
			sseData(4, "hist-2") +
			liveMarker +
			sseData(5, "first live")))
		flush()
		select {
		case <-gate:
		case <-ctx.Done():
			return
		}
		_, _ = w.Write([]byte(": keepalive\n\n" +
			"event: dropped\ndata: {\"dropped\":3}\n\n" +
			"event: future\ndata: " + eventData(98, "unknown type") + "\n\n" +
			"event: live\ndata: {}\n\n" +
			sseData(9, "second live")))
		flush()
	})
	r := open(t, t.Context(), s.srv.URL, nil, 5*time.Second)
	if got := queries(r.history); len(got) != 2 || got[0] != "hist-1" || got[1] != "hist-2" {
		t.Fatalf("history %v, want [hist-1 hist-2]: the events before the live marker, nothing else", got)
	}
	h := r.history[0]
	if h.Id != 1 || h.Replica != "goldie-1" || string(h.Kind) != "search" || h.Late ||
		!h.Time.Equal(time.Date(2026, 11, 23, 10, 0, 1, 0, time.UTC)) {
		t.Errorf("history event %+v, want id 1, replica goldie-1, the time sent, not late", h)
	}
	e, ok := recv(t, r.live, 3*time.Second)
	if !ok || e.Id != 5 || e.Query != "first live" {
		t.Fatalf("first live event %+v (open %v), want id 5 \"first live\" before the body ends", e, ok)
	}
	close(gate)
	e, ok = recv(t, r.live, 3*time.Second)
	if !ok || e.Id != 9 || e.Query != "second live" {
		t.Errorf("next live event %+v (open %v), want id 9: comments, dropped, a second marker and unknown types are skipped", e, ok)
	}
	if rest := waitClosed(t, r.live, 3*time.Second); len(rest) != 0 {
		t.Errorf("events after the last one: %+v", rest)
	}

	reqs := s.requests()
	if len(reqs) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Method != http.MethodGet || req.URL.Path != "/api/v1/activity" {
		t.Errorf("request %s %s, want GET /api/v1/activity", req.Method, req.URL.Path)
	}
	if got := req.URL.Query()["replicas"]; len(got) > 0 && got[0] == "all" {
		t.Errorf("request asks replicas=%v: a peer request is always local-only", got)
	}
	if a := req.Header.Get("Authorization"); a != "Bearer "+streamToken {
		t.Errorf("Authorization = %q, want the client's bearer token", a)
	}
	if v := req.Header.Get(contract.ClientHeader); v != contract.ClientHeaderPrefix+"1.4.0" {
		t.Errorf("%s = %q, want %q", contract.ClientHeader, v, contract.ClientHeaderPrefix+"1.4.0")
	}
}

// No history: the marker comes first, and the history is empty.
func TestStreamActivityWithNoHistoryReturnsAnEmptyOne(t *testing.T) {
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(liveMarker))
		flush()
		_, _ = w.Write([]byte(sseData(1, "live")))
		flush()
		<-ctx.Done()
	})
	r := open(t, t.Context(), s.srv.URL, nil, 5*time.Second)
	if len(r.history) != 0 {
		t.Errorf("history %v, want none", queries(r.history))
	}
	if e, ok := recv(t, r.live, 3*time.Second); !ok || e.Query != "live" {
		t.Errorf("live event %+v (open %v)", e, ok)
	}
}

// A server that sends no live marker is an older version, or not a goldie
// server: it counts as down. StreamActivity returns an error, bounded by
// idle when the stream stays open with keepalives or falls silent, at once
// when the body ends, and by the history cap when it keeps sending events.
// A full history followed by the marker is accepted.
func TestStreamActivityWithoutALiveMarkerIsAnError(t *testing.T) {
	const idle = 300 * time.Millisecond
	for name, body := range map[string]func(ctx context.Context, w http.ResponseWriter, flush func()){
		"keepalives only": func(ctx context.Context, w http.ResponseWriter, flush func()) {
			_, _ = w.Write([]byte(sseData(1, "old server")))
			flush()
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					_, _ = w.Write([]byte(": keepalive\n\n"))
					flush()
				}
			}
		},
		"silent": func(ctx context.Context, w http.ResponseWriter, flush func()) {
			_, _ = w.Write([]byte(sseData(1, "old server")))
			flush()
			<-ctx.Done()
		},
		"headers only": func(ctx context.Context, _ http.ResponseWriter, _ func()) {
			<-ctx.Done()
		},
		"body ends": func(_ context.Context, w http.ResponseWriter, flush func()) {
			_, _ = w.Write([]byte(sseData(1, "old server")))
			flush()
		},
		"past the history cap": func(ctx context.Context, w http.ResponseWriter, flush func()) {
			for id := 1; id <= historyCap+1; id++ {
				if _, err := w.Write([]byte(sseData(id, "h"))); err != nil {
					return
				}
			}
			flush()
			// Then a stream that would never end: only the cap stops it.
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for id := historyCap + 2; ; id++ {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					_, _ = w.Write([]byte(sseData(id, "h")))
					flush()
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSSEServer(t, body)
			r := openActivity(t, t.Context(), s.srv.URL, nil, idle, 15*time.Second)
			if r.err == nil {
				t.Fatalf("a stream without a live marker: history of %d events and a channel, want an error", len(r.history))
			}
			host := strings.TrimPrefix(s.srv.URL, "http://")
			if strings.Contains(r.err.Error(), host) || strings.Contains(r.err.Error(), "127.0.0.1") {
				t.Errorf("error %q names the address", r.err)
			}
			select {
			case <-s.done:
			case <-time.After(3 * time.Second):
				t.Error("the connection is still open after StreamActivity gave up")
			}
		})
	}

	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		for id := 1; id <= historyCap; id++ {
			if _, err := w.Write([]byte(sseData(id, fmt.Sprintf("h%d", id)))); err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(liveMarker))
		flush()
		<-ctx.Done()
	})
	r := open(t, t.Context(), s.srv.URL, nil, 5*time.Second)
	if len(r.history) != historyCap || r.history[historyCap-1].Query != fmt.Sprintf("h%d", historyCap) {
		t.Errorf("a full history of %d events: got %d, want all of them in order", historyCap, len(r.history))
	}
}

// The injected client's timeout bounds a call, not a stream: a stream with
// keepalives outlives it, and idle alone decides when silence ends it.
func TestStreamActivityOutlivesTheClientTimeout(t *testing.T) {
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(liveMarker))
		flush()
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		end := time.After(time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flush()
			case <-end:
				_, _ = w.Write([]byte(sseData(1, "after the timeout")))
				flush()
				<-ctx.Done()
				return
			}
		}
	})
	r := open(t, t.Context(), s.srv.URL, &http.Client{Timeout: 200 * time.Millisecond}, 500*time.Millisecond)
	e, ok := recv(t, r.live, 5*time.Second)
	if !ok || e.Query != "after the timeout" {
		t.Errorf("got %+v (open %v), want the event sent after 1s: the client's 200ms timeout ended the stream", e, ok)
	}
}

// The idle read deadline: keepalives keep a live stream open; when nothing at
// all arrives for idle, the channel closes, so the merger sees the peer down.
func TestStreamActivityClosesWhenTheServerFallsSilent(t *testing.T) {
	const idle = 300 * time.Millisecond
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(liveMarker + sseData(1, "then silence")))
		flush()
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		quiet := time.After(1200 * time.Millisecond)
		for {
			select {
			case <-ctx.Done():
				return
			case <-quiet:
				<-ctx.Done() // silent: no keepalive, no event, no end
				return
			case <-tick.C:
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flush()
			}
		}
	})
	start := time.Now()
	r := open(t, t.Context(), s.srv.URL, nil, idle)
	if e, ok := recv(t, r.live, 3*time.Second); !ok || e.Query != "then silence" {
		t.Fatalf("first live event %+v (open %v)", e, ok)
	}
	waitClosed(t, r.live, 5*time.Second)
	if d := time.Since(start); d < time.Second {
		t.Errorf("the stream closed after %v while keepalives were flowing every 50ms (idle %v)", d, idle)
	}
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Error("the server's request did not end after the client gave up: the connection is still open")
	}
}

// Cancelling ctx closes the channel and the connection.
func TestStreamActivityEndsWithItsContext(t *testing.T) {
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(liveMarker + sseData(1, "one")))
		flush()
		<-ctx.Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := open(t, ctx, s.srv.URL, nil, time.Minute)
	if _, ok := recv(t, r.live, 3*time.Second); !ok {
		t.Fatal("closed before the first event")
	}
	cancel()
	waitClosed(t, r.live, 3*time.Second)
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Error("the server's request did not end after ctx was cancelled")
	}
}

// A stream longer than the answer cap is read to its end: the cap is for
// answers, and a stream runs for hours.
func TestStreamActivityIsNotCutAtTheAnswerCap(t *testing.T) {
	query := strings.Repeat("q", 1000)
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(liveMarker))
		written := 0
		for id := 1; written <= maxAnswer+(1<<20); id++ {
			n, err := w.Write([]byte(sseData(id, query)))
			if err != nil {
				return
			}
			written += n
		}
		_, _ = w.Write([]byte(sseData(999_999, "sentinel")))
		flush()
		<-ctx.Done()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	r := open(t, ctx, s.srv.URL, nil, 10*time.Second)
	n := 0
	for {
		select {
		case e, ok := <-r.live:
			if !ok {
				t.Fatalf("the stream closed after %d events, before the sentinel past %d bytes: it is capped like an answer", n, maxAnswer)
			}
			n++
			if e.Query == "sentinel" {
				return
			}
		case <-ctx.Done():
			t.Fatalf("no sentinel within 60s (%d events read)", n)
		}
	}
}

// One event larger than the answer cap ends the stream rather than being
// buffered: in the live part the channel closes after the events before it;
// in the history it is an error.
func TestStreamActivityEndsOnAnOversizedEvent(t *testing.T) {
	oversized := func(w http.ResponseWriter) {
		_, _ = w.Write([]byte("data: "))
		chunk := []byte(strings.Repeat("x", 1<<20))
		for written := 0; written <= maxAnswer; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(liveMarker + sseData(1, "before")))
		flush()
		oversized(w)
		flush()
		<-ctx.Done()
	})
	r := open(t, t.Context(), s.srv.URL, nil, 10*time.Second)
	got := waitClosed(t, r.live, 30*time.Second)
	if len(got) != 1 || got[0].Query != "before" {
		t.Errorf("got %d events %+v, want only the event before the oversized one", len(got), got)
	}

	s = newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(sseData(1, "before")))
		oversized(w)
		flush()
		<-ctx.Done()
	})
	if r := openActivity(t, t.Context(), s.srv.URL, nil, 10*time.Second, 30*time.Second); r.err == nil {
		t.Errorf("an oversized event in the history: history of %d and a channel, want an error", len(r.history))
	}
}

// A data line that is not an ActivityEvent is never delivered as one, in the
// history or live.
func TestStreamActivityNeverDeliversMalformedData(t *testing.T) {
	malformed := "data: not json\n\n" + "data: {\"id\":\"x\"}\n\n" + "data: [1,2]\n\n"
	s := newSSEServer(t, func(_ context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(malformed + sseData(6, "valid") + liveMarker + malformed + sseData(7, "valid")))
		flush()
	})
	r := open(t, t.Context(), s.srv.URL, nil, 5*time.Second)
	all := append(append([]contract.ActivityEvent(nil), r.history...), waitClosed(t, r.live, 5*time.Second)...)
	for _, e := range all {
		if e.Query != "valid" || (e.Id != 6 && e.Id != 7) {
			t.Errorf("delivered %+v from malformed data", e)
		}
	}
}

// No redirect is followed, so the token never goes to a Location the peer
// picks: each of 301, 302, 307 and 308 is ErrRedirect, the target sees
// nothing, and the error carries nothing of the answer.
func TestStreamActivityRefusesEveryRedirectAndNeverSendsTheTokenOn(t *testing.T) {
	target := newRecorder(t)
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		from := redirectTo(t, code, target.srv.URL)
		for clientName, injected := range map[string]*http.Client{"own client": nil, "injected following client": {}} {
			target.reset()
			r := openActivity(t, t.Context(), from.URL, injected, 5*time.Second, 10*time.Second)
			if got := target.requests(); len(got) != 0 {
				t.Errorf("%d, %s: the target saw %d requests, Authorization %q", code, clientName, len(got), got[0].Get("Authorization"))
			}
			if !errors.Is(r.err, contract.ErrRedirect) {
				t.Errorf("%d, %s: err %v, want ErrRedirect and no stream", code, clientName, r.err)
				continue
			}
			for _, bad := range []string{streamToken, "leak-marker", target.srv.URL, strings.TrimPrefix(target.srv.URL, "http://"), strings.TrimPrefix(from.URL, "http://")} {
				if strings.Contains(r.err.Error(), bad) {
					t.Errorf("%d, %s: error %q carries %q", code, clientName, r.err, bad)
				}
			}
		}
	}
}

// A refusal is the *APIError of its status: a peer that refuses the
// forwarded token (401, 403), or has no slot (503), is told apart from a
// stream; the error names no address and carries no body.
func TestStreamActivityReturnsTheAPIErrorOfARefusal(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"refused","message":"body-marker at 10.0.0.9:8080","details":{"hint":"hint-marker"}}}`))
		}))
		r := openActivity(t, t.Context(), srv.URL, nil, 5*time.Second, 10*time.Second)
		srv.Close()
		var apiErr *contract.APIError
		if !errors.As(r.err, &apiErr) || apiErr.Status != status {
			t.Errorf("%d: err %v; want an *APIError with status %d and no stream", status, r.err, status)
			continue
		}
		for _, bad := range []string{strings.TrimPrefix(srv.URL, "http://"), "127.0.0.1", "body-marker", "10.0.0.9", streamToken} {
			if strings.Contains(r.err.Error(), bad) {
				t.Errorf("%d: error %q carries %q", status, r.err, bad)
			}
		}
	}
}

// A 200 that is not an event stream (a proxy's login page) is
// ErrNotAPIAnswer, and an unreachable server is an error naming no address.
func TestStreamActivityRefusesANonStreamAndNamesNoAddress(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>login</html>"))
	}))
	t.Cleanup(page.Close)
	if r := openActivity(t, t.Context(), page.URL, nil, 5*time.Second, 10*time.Second); !errors.Is(r.err, contract.ErrNotAPIAnswer) {
		t.Errorf("a text/html 200: err %v; want ErrNotAPIAnswer", r.err)
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	base := gone.URL
	gone.Close()
	r := openActivity(t, t.Context(), base, nil, 5*time.Second, 10*time.Second)
	if r.err == nil {
		t.Fatal("an unreachable server: no error")
	}
	host := strings.TrimPrefix(base, "http://")
	_, port, _ := strings.Cut(host, ":")
	for _, bad := range []string{host, "127.0.0.1", ":" + port, "dial", "connect"} {
		if strings.Contains(r.err.Error(), bad) {
			t.Errorf("an unreachable server: error %q carries %q", r.err, bad)
		}
	}
}
