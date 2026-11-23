package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #243: GET /api/v1/activity streams the Activity as
// server-sent events, to operators only. The seam is the public handler
// PublicHandler builds, served over a real listener and parsed the way an SSE
// client parses it (WHATWG HTML, "Server-sent events": lines end at CRLF, LF
// or CR, a blank line dispatches, a line starting with ":" is a comment). The
// contract they fix, beyond the spec:
//
//	// activityKeepalive is how often an open stream with nothing to send
//	// writes a keepalive comment. A package variable so tests can shorten it,
//	// like platform's connection timeouts; the handler reads it when the
//	// public handler is built or when a stream opens, never later.
//	var activityKeepalive time.Duration
//
//	// The guard puts the paths in operatorPaths behind api.RoleOperator,
//	// mirroring docPaths; /api/v1/activity is the first.
//	var operatorPaths map[string]bool
//
// The stream's wire format:
//   - every Event is one SSE event whose data is one line of JSON: an object
//     with at least "id" (the Event's id, a number), "kind" ("search" or
//     "lookup"), "query" and "project" (the Event's fields, as text);
//   - a subscriber that lost events gets an SSE event of type "dropped"
//     (`event: dropped`) whose data is one line of JSON;
//   - the keepalive is an SSE comment line (starting with ":").
//
// Until activityKeepalive exists this package's tests do not compile.

const activityPath = "/api/v1/activity"

// shortKeepalive sets activityKeepalive for one test. Set it before building
// the handler.
func shortKeepalive(t *testing.T, d time.Duration) {
	t.Helper()
	old := activityKeepalive
	activityKeepalive = d
	t.Cleanup(func() { activityKeepalive = old })
}

// roomyRateLimit lets a test send many requests on one token.
var roomyRateLimit = platform.RateLimit{Requests: 100_000, Per: time.Second, Burst: 100_000}

// streamHandler is activityHandler with a roomy rate limit unless cfg sets one.
func streamHandler(t *testing.T, cfg platform.Config) *Public {
	t.Helper()
	if cfg.RateLimit == (platform.RateLimit{}) {
		cfg.RateLimit = roomyRateLimit
	}
	return activityHandler(t, cfg)
}

// sseEvent is one dispatched server-sent event.
type sseEvent struct {
	Type      string // "message" unless an event: field set it
	ID        string
	Data      string // the data lines joined with "\n"
	DataLines int
}

// sseParser parses an event stream as a browser's EventSource does.
type sseParser struct {
	line      []byte
	afterCR   bool
	data      []string
	typ, id   string
	onEvent   func(sseEvent)
	onComment func(string)
}

func (p *sseParser) feed(b []byte) {
	for _, c := range b {
		if p.afterCR {
			p.afterCR = false
			if c == '\n' {
				continue
			}
		}
		switch c {
		case '\r':
			p.afterCR = true
			p.endLine()
		case '\n':
			p.endLine()
		default:
			p.line = append(p.line, c)
		}
	}
}

func (p *sseParser) endLine() {
	line := string(p.line)
	p.line = p.line[:0]
	switch {
	case line == "":
		if len(p.data) > 0 && p.onEvent != nil {
			typ := p.typ
			if typ == "" {
				typ = "message"
			}
			p.onEvent(sseEvent{Type: typ, ID: p.id, Data: strings.Join(p.data, "\n"), DataLines: len(p.data)})
		}
		p.data, p.typ = nil, ""
	case strings.HasPrefix(line, ":"):
		if p.onComment != nil {
			p.onComment(line[1:])
		}
	default:
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			p.data = append(p.data, value)
		case "event":
			p.typ = value
		case "id":
			if !strings.Contains(value, "\x00") {
				p.id = value
			}
		}
	}
}

// parseSSE parses a whole captured stream.
func parseSSE(raw []byte) (events []sseEvent, comments int) {
	p := &sseParser{
		onEvent:   func(e sseEvent) { events = append(events, e) },
		onComment: func(string) { comments++ },
	}
	p.feed(raw)
	return events, comments
}

// sseItem is an event or a comment, in arrival order.
type sseItem struct {
	comment bool
	event   sseEvent
}

// sseLive reads an open stream in the background.
type sseLive struct {
	items chan sseItem
	done  chan error // the read error, nil for a clean end; sent once
	mu    sync.Mutex
	raw   []byte
}

func (s *sseLive) bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.raw)
}

// openStream sends GET path to srv with mutate and ctx. A 200 answer comes
// back with its body read by an sseLive; any other answer with its body.
func openStream(t *testing.T, ctx context.Context, client *http.Client, url string, mutate func(*http.Request)) (*http.Response, *sseLive, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(req)
	}
	type answer struct {
		resp *http.Response
		err  error
	}
	got := make(chan answer, 1)
	go func() {
		resp, err := client.Do(req) //nolint:gosec // test server URL
		got <- answer{resp, err}
	}()
	var a answer
	select {
	case a = <-got:
	case <-time.After(10 * time.Second):
		t.Fatalf("GET %s: no response headers within 10s: the handler blocks before answering", url)
	}
	if a.err != nil {
		t.Fatalf("GET %s: %v", url, a.err)
	}
	if a.resp.StatusCode != http.StatusOK {
		defer func() { _ = a.resp.Body.Close() }()
		body, err := io.ReadAll(a.resp.Body)
		if err != nil {
			t.Fatalf("GET %s: reading the %d body: %v", url, a.resp.StatusCode, err)
		}
		return a.resp, nil, body
	}
	s := &sseLive{items: make(chan sseItem, 16384), done: make(chan error, 1)}
	p := &sseParser{
		onEvent:   func(e sseEvent) { s.items <- sseItem{event: e} },
		onComment: func(string) { s.items <- sseItem{comment: true} },
	}
	go func() {
		defer func() { _ = a.resp.Body.Close() }()
		buf := make([]byte, 4096)
		for {
			n, err := a.resp.Body.Read(buf)
			if n > 0 {
				s.mu.Lock()
				s.raw = append(s.raw, buf[:n]...)
				s.mu.Unlock()
				p.feed(buf[:n])
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
				}
				close(s.items)
				s.done <- err
				return
			}
		}
	}()
	t.Cleanup(func() { _ = a.resp.Body.Close() })
	return a.resp, s, nil
}

// nextEvent returns the next dispatched event, skipping comments.
func nextEvent(t *testing.T, s *sseLive, within time.Duration) sseEvent {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case it, ok := <-s.items:
			if !ok {
				t.Fatalf("the stream ended while an event was expected; so far:\n%s", s.bytes())
			}
			if !it.comment {
				return it.event
			}
		case <-deadline:
			t.Fatalf("no event within %v; so far:\n%s", within, s.bytes())
		}
	}
}

// waitReadEnds waits up to five seconds for the client's read of s to end
// after the client cut the stream.
func waitReadEnds(t *testing.T, s *sseLive) {
	t.Helper()
	select {
	case err := <-s.done:
		_ = err // the client cut the stream, so its read ends with its own error
	case <-time.After(5 * time.Second):
		t.Fatal("the client's read did not end after it disconnected")
	}
}

// wireEvent is the part of an event's JSON these tests read.
type wireEvent struct {
	ID      *uint64 `json:"id"`
	Kind    string  `json:"kind"`
	Query   string  `json:"query"`
	Project string  `json:"project"`
}

// decodeEvent checks e is an activity event whose data is one line of JSON
// with an id, and decodes it.
func decodeEvent(t *testing.T, e sseEvent) wireEvent {
	t.Helper()
	if e.Type == "dropped" {
		t.Fatalf("got a dropped notice where an activity event was expected: %+v", e)
	}
	if e.DataLines != 1 {
		t.Fatalf("event data spans %d lines, want exactly one line of JSON: %q", e.DataLines, e.Data)
	}
	var w wireEvent
	if err := json.Unmarshal([]byte(e.Data), &w); err != nil {
		t.Fatalf("event data is not JSON: %v: %q", err, e.Data)
	}
	if w.ID == nil {
		t.Fatalf("event data has no id: %q", e.Data)
	}
	return w
}

// holdAllSlots fills a's subscriber cap through TrySubscribe and returns the
// cancels, failing when the hub refuses one: it is a check that no stream
// still holds a slot.
func holdAllSlots(t *testing.T, a *api.Activity) []func() {
	t.Helper()
	cancels := make([]func(), 0, api.MaxActivitySubscribers)
	for i := range api.MaxActivitySubscribers {
		_, _, c, ok := a.TrySubscribe()
		if !ok {
			for _, c := range cancels {
				c()
			}
			t.Fatalf("the hub refused subscriber %d of %d: a stream still holds a slot", i+1, api.MaxActivitySubscribers)
		}
		cancels = append(cancels, c)
	}
	return cancels
}

// waitHubEmpty waits up to five seconds for a to hold no subscriber.
func waitHubEmpty(t *testing.T, a *api.Activity) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ok := true
		var cancels []func()
		for range api.MaxActivitySubscribers {
			_, _, c, got := a.TrySubscribe()
			if !got {
				ok = false
				break
			}
			cancels = append(cancels, c)
		}
		for _, c := range cancels {
			c()
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the hub still holds a subscriber 5s after every stream ended: a subscriber leaked")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// streamsEvents reports whether the spec's 200 for the operation "METHOD
// path" is text/event-stream. TestRoutesAndSpecAgree reads only the status
// of such an operation: the stream never ends on its own.
func streamsEvents(t *testing.T, operation string) bool {
	t.Helper()
	method, path, _ := strings.Cut(operation, " ")
	doc := specDoc(t)
	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths[path].(map[string]any)
	op, _ := item[strings.ToLower(method)].(map[string]any)
	responses, _ := op["responses"].(map[string]any)
	content, _ := contracttest.ResolveRef(doc, responses["200"])["content"].(map[string]any)
	_, ok := content["text/event-stream"]
	return ok
}

func searchPath(q string) string {
	return "/api/v1/search?" + url.Values{"q": {q}}.Encode()
}

// Criterion: an operator token gets the history, then live events, in id
// order, as text/event-stream with Cache-Control: no-store. A subscriber
// that keeps up gets no dropped notice, and the stream is not an event of
// its own.
func TestActivityStreamGivesAnOperatorHistoryThenLiveEventsInIDOrder(t *testing.T) {
	pub := streamHandler(t, platform.Config{})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	for _, path := range []string{searchPath("kubernetes"), searchPath("cobol"), "/api/v1/articles/agent-loops"} {
		if rec := serveAs(t, pub, path, func(*http.Request) {}); rec.Code != http.StatusOK {
			t.Fatalf("fixture: %s: status %d", path, rec.Code)
		}
	}

	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s as an operator: status %d, want 200: %s", activityPath, resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !slices.Contains(strings.Split(strings.ReplaceAll(cc, " ", ""), ","), "no-store") {
		t.Errorf("Cache-Control %q, want no-store", cc)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options %q, want nosniff", got)
	}

	want := []struct {
		kind, query string
	}{{"search", "kubernetes"}, {"search", "cobol"}, {"lookup", "agent-loops"}}
	for i, w := range want {
		e := decodeEvent(t, nextEvent(t, s, 5*time.Second))
		if *e.ID != uint64(i+1) || e.Kind != w.kind || e.Query != w.query {
			t.Errorf("history event %d: id %d kind %q query %q, want id %d %s %q", i, *e.ID, e.Kind, e.Query, i+1, w.kind, w.query)
		}
	}

	for _, q := range []string{"agents loops", "deploy"} {
		if r, b := do(t, http.MethodGet, srv.URL+searchPath(q), bearer(readerToken)); r.StatusCode != http.StatusOK {
			t.Fatalf("search %q: status %d: %s", q, r.StatusCode, b)
		}
	}
	for i, q := range []string{"agents loops", "deploy"} {
		e := decodeEvent(t, nextEvent(t, s, 5*time.Second))
		if *e.ID != uint64(len(want)+i+1) || e.Kind != "search" || e.Query != q {
			t.Errorf("live event %d: id %d kind %q query %q, want id %d search %q (the stream itself is no event)",
				i, *e.ID, e.Kind, e.Query, len(want)+i+1, q)
		}
	}
	events, _ := parseSSE(s.bytes())
	for _, e := range events {
		if e.Type == "dropped" {
			t.Errorf("a subscriber that kept up got a dropped notice: %+v", e)
		}
	}
}

// Criterion: an open stream with nothing to send writes a keepalive comment
// every activityKeepalive, and a comment carries no event.
func TestActivityStreamSendsAKeepaliveCommentEveryInterval(t *testing.T) {
	shortKeepalive(t, 50*time.Millisecond)
	pub := streamHandler(t, platform.Config{})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	comments := 0
	deadline := time.After(3 * time.Second)
	for comments < 4 {
		select {
		case it, ok := <-s.items:
			if !ok {
				t.Fatalf("the stream ended after %d keepalives; so far:\n%s", comments, s.bytes())
			}
			if !it.comment {
				t.Errorf("an idle stream dispatched an event: %+v", it.event)
				continue
			}
			comments++
		case <-deadline:
			t.Fatalf("%d keepalive comments in 3s with a 50ms interval, want at least 4; so far:\n%s", comments, s.bytes())
		}
	}
}

// Criterion: the stream is for operators only, through the public mux: no
// token or an unknown one is a 401, a reader token (plaintext or hashed) a
// 403, both in the error envelope, with no stream and no secret echoed.
func TestActivityStreamIsForOperatorsOnly(t *testing.T) {
	pub := streamHandler(t, platform.Config{})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	for who, c := range map[string]struct {
		mutate func(*http.Request)
		status int
		code   string
	}{
		"no token":      {nil, http.StatusUnauthorized, "unauthorized"},
		"unknown token": {bearer("unknown-token-0123456789abcdef0123456789"), http.StatusUnauthorized, "unauthorized"},
		"reader":        {bearer(readerToken), http.StatusForbidden, "forbidden"},
		"hashed reader": {bearer(hashedSecret), http.StatusForbidden, "forbidden"},
		"operator in case": {
			bearer(strings.ToUpper(operatorToken)), http.StatusUnauthorized, "unauthorized",
		},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		resp, body := do(t, http.MethodGet, srv.URL+activityPath, func(r *http.Request) {
			*r = *r.WithContext(ctx)
			if c.mutate != nil {
				c.mutate(r)
			}
		})
		cancel()
		if resp.StatusCode != c.status {
			t.Errorf("%s: status %d, want %d: %.200s", who, resp.StatusCode, c.status, body)
			continue
		}
		assertEnvelope(t, body, c.code)
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: Content-Type %q, want the JSON envelope", who, ct)
		}
		for _, secret := range []string{readerToken, operatorToken, hashedSecret, sha256Hex(hashedSecret)} {
			if strings.Contains(string(body), secret) {
				t.Errorf("%s: the body echoes %q: %s", who, secret, body)
			}
		}
	}
	// The operator gets the stream from the same handler.
	resp, _, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("operator: status %d, want 200: %s", resp.StatusCode, body)
	}
	// operatorPaths does not reach past the stream: readers still read.
	for _, path := range []string{searchPath("kubernetes"), "/api/v1/articles/agent-loops", "/api/v1/projects", "/openapi.json"} {
		if r, b := do(t, http.MethodGet, srv.URL+path, bearer(readerToken)); r.StatusCode != http.StatusOK {
			t.Errorf("reader GET %s: status %d, want 200: %.200s", path, r.StatusCode, b)
		}
	}
}

// Abuse: no spelling of the path lets a reader in. Each variant under /api/
// is a 403, a 404, a 405 or a redirect, and none, there or outside /api/
// (the UI's catch-all), is the stream.
func TestNoSpellingOfTheActivityPathStreamsToAReader(t *testing.T) {
	pub := streamHandler(t, platform.Config{})
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, activityPath},
		{http.MethodHead, activityPath},
		{http.MethodGet, activityPath + "?replicas=all"},
		{http.MethodGet, "/api/v1/%61ctivity"},
		{http.MethodGet, "/api/v1/activity/"},
		{http.MethodGet, "/api/v1//activity"},
		{http.MethodGet, "/api/v1/./activity"},
		{http.MethodGet, "/api/v1/x/../activity"},
		{http.MethodGet, "/API/v1/activity"},
		{http.MethodGet, "/api/v1/Activity"},
		{http.MethodPost, activityPath},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		req := httptest.NewRequestWithContext(ctx, c.method, c.path, nil)
		bearer(readerToken)(req)
		rec := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			pub.ServeHTTP(rec, req)
		}()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Fatalf("%s %s as a reader: still streaming after 6s", c.method, c.path)
		}
		cancel()
		events, _ := parseSSE(rec.Body.Bytes())
		if (strings.HasPrefix(c.path, "/api/") && rec.Code == http.StatusOK) ||
			strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") || len(events) > 0 {
			t.Errorf("%s %s as a reader: status %d type %q: a reader got the stream", c.method, c.path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

// hostileQueries try to end the event a query is in and start one of their
// own, or to hide in the text. Each is a valid search, so it is recorded.
var hostileQueries = []string{
	"kubernetes\n\ndata: {\"id\":999,\"kind\":\"search\",\"query\":\"forged\"}\n\n",
	"cr\r\rdata: {\"id\":998,\"query\":\"forged\"}\r\r",
	"crlf\r\n\r\nevent: dropped\r\ndata: {}\r\n\r\n",
	"comment\n: keepalive\n\nid: 997\n",
	"separators\u2028\u2029data: {\"id\":996}",
	"hidden\u202e\u200b\ufeff\u2066runes\u2069",
	"nul\x00byte and \x1b[31mescape",
	"</script><script>alert(1)</script>",
	"retry: 1\n\nevent: message\ndata: x\n\n",
}

// Criterion: every event is one line of JSON, so no query can forge an event,
// end one early or inject a dropped notice: an SSE client sees exactly one
// event per search, each with the query it sent, as text.
func TestActivityEventsAreOneLineOfJSONSoAQueryCannotForgeAnEvent(t *testing.T) {
	pub := streamHandler(t, platform.Config{})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	// Half go into the history, half arrive live.
	half := len(hostileQueries) / 2
	for _, q := range hostileQueries[:half] {
		if rec := serveAs(t, pub, searchPath(q), func(*http.Request) {}); rec.Code != http.StatusOK {
			t.Fatalf("fixture: search %q: status %d: %s", q, rec.Code, rec.Body.String())
		}
	}
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	for _, q := range hostileQueries[half:] {
		if rec := serveAs(t, pub, searchPath(q), func(*http.Request) {}); rec.Code != http.StatusOK {
			t.Fatalf("fixture: search %q: status %d: %s", q, rec.Code, rec.Body.String())
		}
	}
	// A plain sentinel last: every hostile event is before it.
	if rec := serveAs(t, pub, searchPath("sentinel"), func(*http.Request) {}); rec.Code != http.StatusOK {
		t.Fatalf("fixture: sentinel: status %d", rec.Code)
	}
	var got []sseEvent
	for {
		e := nextEvent(t, s, 5*time.Second)
		got = append(got, e)
		if e.Type != "dropped" && strings.Contains(e.Data, "sentinel") {
			break
		}
		if len(got) > 3*len(hostileQueries) {
			t.Fatalf("no sentinel after %d events: %+v", len(got), got)
		}
	}
	if len(got) != len(hostileQueries)+1 {
		t.Errorf("an SSE client saw %d events for %d searches: a query forged or split an event: %+v",
			len(got), len(hostileQueries)+1, got)
	}
	for i, e := range got {
		w := decodeEvent(t, e)
		if *w.ID != uint64(i+1) {
			t.Errorf("event %d has id %d, want %d (a forged id?)", i, *w.ID, i+1)
		}
		if i < len(hostileQueries) && w.Query != hostileQueries[i] {
			t.Errorf("event %d: query %q, want the text sent, %q", i, w.Query, hostileQueries[i])
		}
	}
	raw := s.bytes()
	for _, r := range []string{"\u2028", "\u2029"} {
		if bytes.Contains(raw, []byte(r)) {
			t.Errorf("the stream carries a raw %U: a JSON line separator must be escaped", []rune(r)[0])
		}
	}
	for i, b := range raw {
		if b < 0x20 && b != '\n' && b != '\r' {
			t.Errorf("the stream carries a raw control byte 0x%02x at %d: query text is not escaped", b, i)
			break
		}
	}
}

// stallWriter is a ResponseWriter whose Write blocks until gate is closed or
// ctx ends: a subscriber that stops reading.
type stallWriter struct {
	ctx    context.Context
	gate   chan struct{}
	wrote  chan struct{} // closed at the first Write
	once   sync.Once
	header http.Header
	mu     sync.Mutex
	buf    bytes.Buffer
	code   int
}

func newStallWriter(ctx context.Context) *stallWriter {
	return &stallWriter{ctx: ctx, gate: make(chan struct{}), wrote: make(chan struct{}), header: http.Header{}}
}

func (w *stallWriter) Header() http.Header { return w.header }

func (w *stallWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.code == 0 {
		w.code = code
	}
}

func (w *stallWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.wrote) })
	select {
	case <-w.gate:
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.buf.Write(p)
}

func (w *stallWriter) Flush() {}

func (w *stallWriter) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.buf.Bytes())
}

// Criterion: a slow subscriber never stalls requests, and when it reads
// again it gets a dropped notice, then the events that still reached it, in
// id order.
func TestASlowActivitySubscriberGetsADroppedNoticeAndNeverStallsRequests(t *testing.T) {
	pub := streamHandler(t, platform.Config{})
	if rec := serveAs(t, pub, searchPath("before"), func(*http.Request) {}); rec.Code != http.StatusOK {
		t.Fatalf("fixture: status %d", rec.Code)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := newStallWriter(ctx)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, activityPath, nil)
	bearer(operatorToken)(req)
	served := make(chan struct{})
	go func() {
		defer close(served)
		pub.ServeHTTP(w, req)
	}()
	select {
	case <-w.wrote:
	case <-served:
		t.Fatalf("the stream ended before writing: status %d: %s", w.code, w.bytes())
	case <-time.After(5 * time.Second):
		t.Fatal("the stream wrote nothing within 5s, not even the history")
	}
	w.mu.Lock()
	code := w.code
	w.mu.Unlock()
	if code != 0 && code != http.StatusOK {
		t.Fatalf("GET %s as an operator: status %d, want the stream", activityPath, code)
	}

	// The subscriber is stuck in its first write. Requests go on.
	const n = api.ActivitySubscriberBuffer + 100
	start := time.Now()
	for i := range n {
		reqStart := time.Now()
		if rec := serveAs(t, pub, searchPath("while stalled"), func(*http.Request) {}); rec.Code != http.StatusOK {
			t.Fatalf("search %d while the subscriber stalls: status %d", i, rec.Code)
		}
		if d := time.Since(reqStart); d > 2*time.Second {
			t.Fatalf("search %d took %v while the subscriber stalls: a slow subscriber stalls requests", i, d)
		}
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("%d searches took %v while the subscriber stalls", n, d)
	}
	if pub.Activity().Dropped() == 0 {
		t.Fatalf("%d events past a stalled subscriber, none dropped", n)
	}

	// The subscriber reads again. Searches go on until one recorded after it
	// caught up reaches it (one recorded while it drains its backlog may be
	// dropped too).
	close(w.gate)
	recorded := n + 1
	var events []sseEvent
	deadline := time.Now().Add(5 * time.Second)
	for {
		if rec := serveAs(t, pub, searchPath("after"), func(*http.Request) {}); rec.Code != http.StatusOK {
			t.Fatalf("fixture: status %d", rec.Code)
		}
		recorded++
		time.Sleep(20 * time.Millisecond)
		events, _ = parseSSE(w.bytes())
		if slices.ContainsFunc(events, func(e sseEvent) bool { return strings.Contains(e.Data, `"after"`) }) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the subscriber never caught up once it read again; it got:\n%s", w.bytes())
		}
	}
	cancel()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end within 5s of its request ending")
	}

	dropped := slices.IndexFunc(events, func(e sseEvent) bool { return e.Type == "dropped" })
	if dropped < 0 {
		t.Fatalf("a subscriber that lost events got no dropped notice; it got:\n%s", w.bytes())
	}
	if d := events[dropped]; d.DataLines != 1 || !json.Valid([]byte(d.Data)) {
		t.Errorf("the dropped notice's data is not one line of JSON: %q", d.Data)
	}
	var last uint64
	activity := 0
	for _, e := range events {
		if e.Type == "dropped" {
			continue
		}
		activity++
		id := *decodeEvent(t, e).ID
		if id <= last {
			t.Errorf("event id %d after %d: not in id order", id, last)
		}
		last = id
	}
	if activity >= recorded {
		t.Errorf("the stalled subscriber got %d events of %d: none were dropped from it", activity, recorded)
	}
	if events[0].Type == "dropped" || *decodeEvent(t, events[0]).ID != 1 {
		t.Errorf("the history (event 1) does not come first: %+v", events[0])
	}
	waitHubEmpty(t, pub.Activity())
}

// Criteria (#243, #310): over the subscriber cap the stream is refused at
// once with 503 in the error envelope and a hint, never left blocking on a
// refused subscription, and the refusal holds no slot; once a slot frees, an
// operator gets the stream.
func TestActivityStreamOverTheSubscriberCapIs503WithAHint(t *testing.T) {
	pub := streamHandler(t, platform.Config{})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	cancels := holdAllSlots(t, pub.Activity())
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if s != nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("over the cap: status %d, want 503", resp.StatusCode)
	}
	contracttest.Conforms(t, resp, body)
	code, hint := errorHint(t, body)
	if code == "not_ready" {
		t.Errorf("over the cap: code %q says the content is loading; the hub is full", code)
	}
	if hint == "" {
		t.Error("over the cap: no hint")
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("over the cap: Content-Type %q, want the JSON envelope", ct)
	}
	for name, re := range leaks {
		if m := re.FindString(string(body)); m != "" {
			t.Errorf("over the cap: the body contains a %s (%q): %s", name, m, body)
		}
	}
	// The refusal took no slot: the cap is still exactly full.
	if _, _, c, ok := pub.Activity().TrySubscribe(); ok {
		c()
		t.Error("a refused stream freed or added a slot")
	}

	cancels[0]()
	cancels = cancels[1:]
	resp, s, body = openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK || s == nil {
		t.Fatalf("with a free slot: status %d, want 200: %s", resp.StatusCode, body)
	}
}

// Criterion: a stream costs one rate-limit token, once: not zero, not one
// per keepalive or per event. Over the limit it is the 429 envelope with
// Retry-After and takes no slot. A reader's 403 costs the reader nothing.
func TestAnActivityStreamCostsOneRateLimitToken(t *testing.T) {
	shortKeepalive(t, 20*time.Millisecond)
	pub := streamHandler(t, platform.Config{RateLimit: platform.RateLimit{Requests: 1, Per: time.Hour, Burst: 2}})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)

	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK || s == nil {
		t.Fatalf("stream: status %d, want 200: %s", resp.StatusCode, body)
	}
	time.Sleep(300 * time.Millisecond) // keepalives flow; they must not spend tokens
	if r, b := do(t, http.MethodGet, srv.URL+searchPath("kubernetes"), bearer(operatorToken)); r.StatusCode != http.StatusOK {
		t.Fatalf("the operator's second request (burst 2, one spent on the stream): status %d, want 200: %s", r.StatusCode, b)
	}
	r, b := do(t, http.MethodGet, srv.URL+searchPath("kubernetes"), bearer(operatorToken))
	if r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the operator's third request: status %d, want 429: the stream cost no token", r.StatusCode)
	}
	assertEnvelope(t, b, "rate_limited")

	resp, s, body = openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusTooManyRequests || s != nil {
		t.Fatalf("a stream over the limit: status %d, want 429", resp.StatusCode)
	}
	contracttest.Conforms(t, resp, body)
	assertEnvelope(t, body, "rate_limited")
	if resp.Header.Get("Retry-After") == "" {
		t.Error("a stream over the limit has no Retry-After")
	}

	for range 5 {
		if r, _ := do(t, http.MethodGet, srv.URL+activityPath, bearer(readerToken)); r.StatusCode != http.StatusForbidden {
			t.Fatalf("reader stream: status %d, want 403", r.StatusCode)
		}
	}
	for i := range 2 {
		if r, b := do(t, http.MethodGet, srv.URL+searchPath("kubernetes"), bearer(readerToken)); r.StatusCode != http.StatusOK {
			t.Fatalf("reader search %d after five 403s: status %d, want 200 (a 403 spends no token): %s", i+1, r.StatusCode, b)
		}
	}
}

// Criterion: the stream ends when the client disconnects, leaking no
// subscriber and no goroutine, and requests that are refused (401, 403, 429,
// 503) never hold a slot.
func TestActivityStreamEndsWhenTheClientDisconnects(t *testing.T) {
	shortKeepalive(t, 20*time.Millisecond)
	pub := streamHandler(t, platform.Config{})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	tr := &http.Transport{}
	client := &http.Client{Transport: tr}
	t.Cleanup(tr.CloseIdleConnections)
	before := runtime.NumGoroutine()

	// One at a time, then the whole cap at once.
	for range 5 {
		ctx, cancel := context.WithCancel(t.Context())
		resp, s, body := openStream(t, ctx, client, srv.URL+activityPath, bearer(operatorToken))
		if resp.StatusCode != http.StatusOK {
			cancel()
			t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
		}
		cancel()
		waitReadEnds(t, s)
	}
	waitHubEmpty(t, pub.Activity())

	var cancels []context.CancelFunc
	var streams []*sseLive
	for i := range api.MaxActivitySubscribers {
		ctx, cancel := context.WithCancel(t.Context())
		cancels = append(cancels, cancel)
		resp, s, body := openStream(t, ctx, client, srv.URL+activityPath, bearer(operatorToken))
		if resp.StatusCode != http.StatusOK {
			for _, c := range cancels {
				c()
			}
			t.Fatalf("stream %d of the cap: status %d, want 200: %s", i+1, resp.StatusCode, body)
		}
		streams = append(streams, s)
	}
	for _, c := range cancels {
		c()
	}
	for _, s := range streams {
		waitReadEnds(t, s)
	}
	waitHubEmpty(t, pub.Activity())

	tr.CloseIdleConnections()
	var after int
	for range 250 {
		runtime.GC()
		if after = runtime.NumGoroutine(); after <= before {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after > before {
		buf := make([]byte, 1<<20)
		t.Errorf("goroutines went from %d to %d after every stream disconnected:\n%s", before, after, buf[:runtime.Stack(buf, true)])
	}

	// Refused requests take no slot.
	for _, mutate := range []func(*http.Request){nil, bearer(readerToken), bearer("unknown-token-0123456789abcdef0123456789")} {
		if r, _ := do(t, http.MethodGet, srv.URL+activityPath, mutate); r.StatusCode == http.StatusOK {
			t.Fatalf("a refused request was streamed")
		}
	}
	cancelsHeld := holdAllSlots(t, pub.Activity())
	if r, _, _ := openStream(t, t.Context(), client, srv.URL+activityPath, bearer(operatorToken)); r.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("over the cap: status %d, want 503", r.StatusCode)
	}
	for _, c := range cancelsHeld {
		c()
	}
	waitHubEmpty(t, pub.Activity())
}

// HEAD is answered wherever GET is: HEAD /api/v1/activity answers with the
// stream's headers and ends, so the connection is free for the next request.
func TestHeadActivityEndsAndFreesTheConnection(t *testing.T) {
	pub := streamHandler(t, platform.Config{})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	tr := &http.Transport{MaxConnsPerHost: 1}
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodHead, srv.URL+activityPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer(operatorToken)(req)
	resp, err := client.Do(req) //nolint:gosec // test server URL
	if err != nil {
		t.Fatalf("HEAD %s: %v", activityPath, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Errorf("HEAD: status %d type %q, want 200 text/event-stream", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+searchPath("kubernetes"), nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer(readerToken)(req)
	resp, err = client.Do(req) //nolint:gosec // test server URL
	if err != nil {
		t.Fatalf("the request after HEAD on the one connection: %v: HEAD left the stream running", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the request after HEAD: status %d, want 200", resp.StatusCode)
	}
	waitHubEmpty(t, pub.Activity())
}
