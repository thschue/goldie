package contract

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sync/atomic"
	"time"
)

// MaxActivityHistory is the most events one replica retains, so the most
// that can come before a stream's live marker (api.MaxActivityCapacity).
const MaxActivityHistory = 10_000

// streamBuffer is the capacity of the live channel StreamActivity returns.
const streamBuffer = 64

// ErrNoLiveMarker is StreamActivity's answer for a stream that ended, fell
// silent or passed MaxActivityHistory before its live marker: an older
// server, or not a goldie server.
var ErrNoLiveMarker = errors.New("the server's activity stream sent no live marker")

// errNotStreamable is StreamActivity on a client NewAPIClient did not build.
var errNotStreamable = errors.New("the activity stream needs a client built by NewAPIClient")

// StreamActivity opens the local activity stream (GET /api/v1/activity,
// never replicas=all) with the client's token. It reads the history up to
// the live marker, which must arrive within idle of the answer (keepalives
// do not extend it: an older server sends them but never the marker) and
// after at most MaxActivityHistory events and MaxResponseBytes read in all
// (every byte up to and including the marker counts), and returns it with a
// channel of the live events after it. A non-200 is an *APIError, a 3xx ErrRedirect
// (never followed), a 200 that is not text/event-stream ErrNotAPIAnswer, a
// stream that ends, falls silent or passes either history bound before its
// live marker an error, and an unreachable server an error naming no address.
// History and live hold every event without a type that is an
// ActivityEvent, skipping comments, other event types and malformed data.
// The channel closes when the body ends, ctx ends, no byte arrives for idle,
// or one event is larger than MaxResponseBytes. The stream is not capped at
// MaxResponseBytes after the marker and not ended by the client's timeout;
// idle governs it.
func (c *ClientWithResponses) StreamActivity(ctx context.Context, idle time.Duration) (history []ActivityEvent, live <-chan ActivityEvent, err error) {
	inner, ok := c.ClientInterface.(*Client)
	if !ok {
		return nil, nil, errNotStreamable
	}
	hardened, ok := inner.Client.(*http.Client)
	if !ok {
		return nil, nil, errNotStreamable
	}
	limited, ok := hardened.Transport.(limitTransport)
	if !ok {
		return nil, nil, errNotStreamable
	}
	streaming := *hardened
	streaming.Timeout = 0 // idle governs a stream, not the call timeout
	streaming.Transport = redirectRefusal(limited)

	parent := ctx
	ctx, cancel := context.WithCancel(parent)
	deadline := time.AfterFunc(idle, cancel) // the answer, then the history, within idle
	fail := func(err error) ([]ActivityEvent, <-chan ActivityEvent, error) {
		deadline.Stop()
		cancel()
		return nil, nil, err
	}
	req, err := NewGetActivityRequest(inner.Server, nil)
	if err != nil {
		return fail(errNoAnswer)
	}
	req = req.WithContext(ctx)
	if err := inner.applyEditors(ctx, req, nil); err != nil {
		return fail(errNoAnswer)
	}
	resp, err := streaming.Do(req)
	if err != nil {
		return fail(streamError(parent, err))
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
		_ = resp.Body.Close()
		e := DecodeAPIError(resp.StatusCode, resp.Header, body)
		return fail(&e)
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "text/event-stream" {
		_ = resp.Body.Close()
		return fail(ErrNotAPIAnswer)
	}
	deadline.Reset(idle)
	body := &idleBody{rc: resp.Body, deadline: deadline, idle: idle}
	events := &sseReader{r: bufio.NewReader(body), budget: MaxResponseBytes}
	for {
		typ, data, err := events.next()
		if err != nil {
			_ = body.Close()
			return fail(streamError(parent, ErrNoLiveMarker))
		}
		if typ == "live" {
			break
		}
		e, ok := activityEvent(typ, data)
		if !ok {
			continue
		}
		if len(history) == MaxActivityHistory {
			_ = body.Close()
			return fail(ErrNoLiveMarker)
		}
		history = append(history, e)
	}
	events.budget = 0 // the history's budget; the live part runs for hours
	body.live.Store(true)
	deadline.Reset(idle)
	ch := make(chan ActivityEvent, streamBuffer)
	go func() {
		defer close(ch)
		defer cancel()
		defer deadline.Stop()
		defer func() { _ = body.Close() }()
		for {
			typ, data, err := events.next()
			if err != nil {
				return
			}
			e, ok := activityEvent(typ, data)
			if !ok {
				continue
			}
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return history, ch, nil
}

// streamError is the error of a failed stream: the caller's context error
// when it ended, else the sentinel of err's class, dropping any cause that
// could carry an address.
func streamError(caller context.Context, err error) error {
	switch {
	case caller.Err() != nil:
		return caller.Err()
	case errors.Is(err, ErrRedirect):
		return ErrRedirect
	case errors.Is(err, ErrNoLiveMarker):
		return ErrNoLiveMarker
	}
	return errNoAnswer
}

// activityEvent decodes the data of an event without a type as an
// ActivityEvent; anything else, or data that is not one with an id and a
// time, is not an event.
func activityEvent(typ string, data []byte) (ActivityEvent, bool) {
	var e ActivityEvent
	if (typ != "" && typ != "message") || json.Unmarshal(data, &e) != nil || e.Id < 1 || e.Time.IsZero() {
		return ActivityEvent{}, false
	}
	return e, true
}

// redirectRefusal refuses every 3xx answer, like limitTransport, without
// capping the body: a stream runs for hours.
type redirectRefusal struct{ base http.RoundTripper }

func (t redirectRefusal) RoundTrip(r *http.Request) (*http.Response, error) {
	return refuseRedirect(t.base, r)
}

// idleBody pushes the stream's deadline out by idle on every byte read once
// live is set, so silence for idle cancels the request.
type idleBody struct {
	rc       io.ReadCloser
	deadline *time.Timer
	idle     time.Duration
	live     atomic.Bool
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 && b.live.Load() {
		b.deadline.Reset(b.idle)
	}
	return n, err
}

func (b *idleBody) Close() error { return b.rc.Close() }

// sseReader reads server-sent events (WHATWG HTML, "Server-sent events"),
// failing with ErrTooLarge when one event is larger than MaxResponseBytes,
// or when more than budget bytes have been read in all (no limit when
// budget is 0). Every byte counts: events, comments, skipped fields and
// types, malformed data, line ends and the live marker.
type sseReader struct {
	r      *bufio.Reader
	line   []byte
	budget int
	read   int
}

// next returns the type and data of the next event that has data, skipping
// comments and fields other than event and data.
func (s *sseReader) next() (typ string, data []byte, err error) {
	size, hasData := 0, false
	for {
		line, err := s.readLine(&size)
		if err != nil {
			return "", nil, err
		}
		if len(line) == 0 {
			if hasData {
				return typ, data, nil
			}
			typ, size = "", 0
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			typ = string(value)
		case "data":
			if hasData {
				data = append(data, '\n')
			}
			data, hasData = append(data, value...), true
		}
	}
}

// readLine reads one line without its end (LF or CRLF), adding its length to
// size; the line is valid until the next call.
func (s *sseReader) readLine(size *int) ([]byte, error) {
	s.line = s.line[:0]
	for {
		chunk, err := s.r.ReadSlice('\n')
		*size += len(chunk)
		s.read += len(chunk)
		if *size > MaxResponseBytes || (s.budget > 0 && s.read > s.budget) {
			return nil, ErrTooLarge
		}
		s.line = append(s.line, chunk...)
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
	return bytes.TrimSuffix(s.line[:len(s.line)-1], []byte("\r")), nil
}
