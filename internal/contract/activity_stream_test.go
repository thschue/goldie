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
// timeout would end it, so the client has a streaming method instead:
//
//	// StreamActivity opens the local activity stream (GET /api/v1/activity,
//	// never replicas=all) with the client's token and returns its events as
//	// they arrive. A non-200 is an *APIError, a 3xx ErrRedirect (never
//	// followed), a 200 that is not text/event-stream ErrNotAPIAnswer, and an
//	// unreachable server an error naming no address. The channel delivers
//	// every event without a type, skips comments and other event types, and
//	// closes when the body ends, ctx ends, no byte arrives for idle, or one
//	// event is too large. The stream is not capped at MaxResponseBytes and
//	// not ended by the injected client's timeout; idle governs it.
//	func (c *ClientWithResponses) StreamActivity(ctx context.Context, idle time.Duration) (<-chan ActivityEvent, error)
//
// ActivityEvent gains Replica (`replica` in the spec) through ./run regen.

// streamToken is the token the stream client sends.
const streamToken = "stream-client-token-0123456789abcdef01"

// eventData is one ActivityEvent as a goldie server writes it.
func eventData(id int, query string) string {
	return fmt.Sprintf(`{"id":%d,"time":"2026-11-23T10:00:%02dZ","kind":"search","query":%q,"project":"","token":"ci-operator","client_version":"","trace_id":"","miss":false,"replica":"goldie-1"}`, id, id%60, query)
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

func openActivity(t *testing.T, ctx context.Context, base string, httpc *http.Client, idle time.Duration) (<-chan contract.ActivityEvent, error) {
	t.Helper()
	ch, err := newAPIClient(t, base, streamToken, httpc).StreamActivity(ctx, idle)
	if err == nil && ch == nil {
		t.Fatal("StreamActivity returned neither a stream nor an error")
	}
	return ch, err
}

// recv returns the next event, or ok false when the channel closed; it fails
// after within.
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

// Events arrive as they are written, not when the body ends; comments,
// dropped notices and unknown event types are skipped; the request is the
// local stream with the token and the client header; the channel closes when
// the body ends.
func TestStreamActivityDeliversEventsAsTheyArrive(t *testing.T) {
	gate := make(chan struct{})
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(sseData(1, "first")))
		flush()
		select {
		case <-gate:
		case <-ctx.Done():
			return
		}
		_, _ = w.Write([]byte(": keepalive\n\n" +
			"event: dropped\ndata: {\"dropped\":3}\n\n" +
			"event: future\ndata: " + eventData(98, "unknown type") + "\n\n" +
			sseData(5, "second")))
		flush()
	})
	ch, err := openActivity(t, t.Context(), s.srv.URL, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("StreamActivity: %v", err)
	}
	e, ok := recv(t, ch, 3*time.Second)
	if !ok {
		t.Fatal("the stream closed before its first event")
	}
	if e.Id != 1 || e.Query != "first" || e.Replica != "goldie-1" || string(e.Kind) != "search" ||
		!e.Time.Equal(time.Date(2026, 11, 23, 10, 0, 1, 0, time.UTC)) {
		t.Errorf("first event %+v, want id 1, query first, replica goldie-1, the time sent", e)
	}
	close(gate)
	e, ok = recv(t, ch, 3*time.Second)
	if !ok || e.Id != 5 || e.Query != "second" {
		t.Errorf("next event %+v (open %v), want id 5 second: comments, dropped and unknown types are skipped", e, ok)
	}
	if rest := waitClosed(t, ch, 3*time.Second); len(rest) != 0 {
		t.Errorf("events after the last one: %+v", rest)
	}

	reqs := s.requests()
	if len(reqs) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodGet || r.URL.Path != "/api/v1/activity" {
		t.Errorf("request %s %s, want GET /api/v1/activity", r.Method, r.URL.Path)
	}
	if got := r.URL.Query()["replicas"]; len(got) > 0 && got[0] == "all" {
		t.Errorf("request asks replicas=%v: a peer request is always local-only", got)
	}
	if a := r.Header.Get("Authorization"); a != "Bearer "+streamToken {
		t.Errorf("Authorization = %q, want the client's bearer token", a)
	}
	if v := r.Header.Get(contract.ClientHeader); v != contract.ClientHeaderPrefix+"1.4.0" {
		t.Errorf("%s = %q, want %q", contract.ClientHeader, v, contract.ClientHeaderPrefix+"1.4.0")
	}
}

// The injected client's timeout bounds a call, not a stream: a stream with
// keepalives outlives it, and idle alone decides when silence ends it.
func TestStreamActivityOutlivesTheClientTimeout(t *testing.T) {
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
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
	ch, err := openActivity(t, t.Context(), s.srv.URL, &http.Client{Timeout: 200 * time.Millisecond}, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("StreamActivity: %v", err)
	}
	e, ok := recv(t, ch, 5*time.Second)
	if !ok || e.Query != "after the timeout" {
		t.Errorf("got %+v (open %v), want the event sent after 1s: the client's 200ms timeout ended the stream", e, ok)
	}
}

// The idle read deadline: keepalives keep a stream open; when nothing at all
// arrives for idle, the channel closes, so the merger sees the peer down.
func TestStreamActivityClosesWhenTheServerFallsSilent(t *testing.T) {
	const idle = 300 * time.Millisecond
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(sseData(1, "then silence")))
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
	ch, err := openActivity(t, t.Context(), s.srv.URL, nil, idle)
	if err != nil {
		t.Fatalf("StreamActivity: %v", err)
	}
	if e, ok := recv(t, ch, 3*time.Second); !ok || e.Query != "then silence" {
		t.Fatalf("first event %+v (open %v)", e, ok)
	}
	waitClosed(t, ch, 5*time.Second)
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
		_, _ = w.Write([]byte(sseData(1, "one")))
		flush()
		<-ctx.Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	ch, err := openActivity(t, ctx, s.srv.URL, nil, time.Minute)
	if err != nil {
		cancel()
		t.Fatalf("StreamActivity: %v", err)
	}
	if _, ok := recv(t, ch, 3*time.Second); !ok {
		cancel()
		t.Fatal("closed before the first event")
	}
	cancel()
	waitClosed(t, ch, 3*time.Second)
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
	ch, err := openActivity(t, ctx, s.srv.URL, nil, 10*time.Second)
	if err != nil {
		t.Fatalf("StreamActivity: %v", err)
	}
	n := 0
	for {
		select {
		case e, ok := <-ch:
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
// buffered; the events before it still arrive.
func TestStreamActivityEndsOnAnOversizedEvent(t *testing.T) {
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte(sseData(1, "before")))
		flush()
		_, _ = w.Write([]byte("data: "))
		chunk := []byte(strings.Repeat("x", 1<<20))
		for written := 0; written <= maxAnswer; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		flush()
		<-ctx.Done()
	})
	ch, err := openActivity(t, t.Context(), s.srv.URL, nil, 10*time.Second)
	if err != nil {
		t.Fatalf("StreamActivity: %v", err)
	}
	got := waitClosed(t, ch, 30*time.Second)
	if len(got) != 1 || got[0].Query != "before" {
		t.Errorf("got %d events %+v, want only the event before the oversized one", len(got), got)
	}
}

// A data line that is not an ActivityEvent is never delivered as one.
func TestStreamActivityNeverDeliversMalformedData(t *testing.T) {
	s := newSSEServer(t, func(_ context.Context, w http.ResponseWriter, flush func()) {
		_, _ = w.Write([]byte("data: not json\n\n" +
			"data: {\"id\":\"x\"}\n\n" +
			"data: [1,2]\n\n" +
			sseData(7, "valid")))
		flush()
	})
	ch, err := openActivity(t, t.Context(), s.srv.URL, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("StreamActivity: %v", err)
	}
	for _, e := range waitClosed(t, ch, 5*time.Second) {
		if e.Id != 7 || e.Query != "valid" {
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
			ch, err := openActivity(t, t.Context(), from.URL, injected, 5*time.Second)
			if got := target.requests(); len(got) != 0 {
				t.Errorf("%d, %s: the target saw %d requests, Authorization %q", code, clientName, len(got), got[0].Get("Authorization"))
			}
			if !errors.Is(err, contract.ErrRedirect) || ch != nil {
				t.Errorf("%d, %s: err %v, channel nil %v; want ErrRedirect and no stream", code, clientName, err, ch == nil)
				continue
			}
			for _, bad := range []string{streamToken, "leak-marker", target.srv.URL, strings.TrimPrefix(target.srv.URL, "http://"), strings.TrimPrefix(from.URL, "http://")} {
				if strings.Contains(err.Error(), bad) {
					t.Errorf("%d, %s: error %q carries %q", code, clientName, err, bad)
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
		ch, err := openActivity(t, t.Context(), srv.URL, nil, 5*time.Second)
		srv.Close()
		var apiErr *contract.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != status || ch != nil {
			t.Errorf("%d: err %v, channel nil %v; want an *APIError with status %d and no stream", status, err, ch == nil, status)
			continue
		}
		for _, bad := range []string{strings.TrimPrefix(srv.URL, "http://"), "127.0.0.1", "body-marker", "10.0.0.9", streamToken} {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("%d: error %q carries %q", status, err, bad)
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
	if ch, err := openActivity(t, t.Context(), page.URL, nil, 5*time.Second); !errors.Is(err, contract.ErrNotAPIAnswer) || ch != nil {
		t.Errorf("a text/html 200: err %v, channel nil %v; want ErrNotAPIAnswer", err, ch == nil)
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	base := gone.URL
	gone.Close()
	ch, err := openActivity(t, t.Context(), base, nil, 5*time.Second)
	if err == nil || ch != nil {
		t.Fatalf("an unreachable server: err %v, channel nil %v; want an error", err, ch == nil)
	}
	host := strings.TrimPrefix(base, "http://")
	_, port, _ := strings.Cut(host, ":")
	for _, bad := range []string{host, "127.0.0.1", ":" + port, "dial", "connect"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("an unreachable server: error %q carries %q", err, bad)
		}
	}
}
