package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/thschue/goldie/internal/contract"
)

// DefaultActivityKeepalive is how often an idle activity stream writes a
// keepalive comment unless KeepStreamsAlive sets another interval.
const DefaultActivityKeepalive = 15 * time.Second

// streamWriteTimeout bounds each write to an activity stream: a client that
// stops reading cannot hold its handler, or the shutdown, past it.
const streamWriteTimeout = 10 * time.Second

// tooManyStreams is the 503 envelope when the Activity refuses a subscriber.
var tooManyStreams = envelope("too_many_streams", "too many activity streams are open",
	"This server serves the most activity streams it allows. Close another stream, or retry in a minute.")

// shuttingDown is the 503 envelope for a stream asked for after EndStreams.
var shuttingDown = envelope("shutting_down", "the server is shutting down",
	"Reconnect in a few seconds; another server process will answer.")

// KeepStreamsAlive sets how often an idle activity stream writes a keepalive
// comment; zero or less keeps DefaultActivityKeepalive. Call it before
// Register.
func (s *Server) KeepStreamsAlive(every time.Duration) {
	if every > 0 {
		s.keepalive = every
	}
}

// EndStreams ends every open activity stream cleanly and answers new ones
// with 503 shutting_down. The composition root calls it when the server
// starts to stop (PC-08): http.Server.Shutdown waits for open requests but
// never cancels them. It is idempotent.
func (s *Server) EndStreams() { s.endOnce.Do(func() { close(s.streamsDone) }) }

// GetActivity answers GET /api/v1/activity with the Activity's events as
// server-sent events. The guard lets operators only through. Before the
// first load it is 503 not_ready like every read; a refused subscription is
// 503 too_many_streams at once, never a stream that waits.
func (s *Server) GetActivity(ctx context.Context, _ contract.GetActivityRequestObject) (contract.GetActivityResponseObject, error) {
	if s.provider() == nil {
		return contract.GetActivity503JSONResponse(notReady), nil
	}
	select {
	case <-s.streamsDone:
		return contract.GetActivity503JSONResponse(shuttingDown), nil
	default:
	}
	history, live, cancel, ok := s.activity.TrySubscribe()
	if !ok {
		return contract.GetActivity503JSONResponse(tooManyStreams), nil
	}
	return &activityStream{ctx: ctx, history: history, live: live, cancel: cancel,
		keepalive: s.keepalive, done: s.streamsDone}, nil
}

// headOnly marks an activity stream answering HEAD, so it sends its headers
// and ends: net/http discards the body of a HEAD response, so a stream would
// never end on its own. It is the one middleware that sees the method.
func headOnly(next contract.StrictHandlerFunc, _ string) contract.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		resp, err := next(ctx, w, r, req)
		if st, ok := resp.(*activityStream); ok && r.Method == http.MethodHead {
			st.head = true
		}
		return resp, err
	}
}

// activityStream is the 200 response of GetActivity: a subscription to the
// Activity, written as an event stream until the request ends, the live
// channel closes or EndStreams runs. The generated response type would copy a
// reader; this writes and flushes each event as it comes.
type activityStream struct {
	ctx       context.Context
	history   []Event
	live      <-chan Event
	cancel    func()
	keepalive time.Duration
	done      <-chan struct{}
	head      bool
	last      uint64 // id of the last event written
}

// streamEvent is an Event on the wire. json.Marshal escapes control
// characters, U+2028 and U+2029, and <, > and &, so every event is one line
// of JSON and no query, Project or client version can end an event or start
// one.
type streamEvent struct {
	ID            uint64    `json:"id"`
	Time          time.Time `json:"time"`
	Kind          EventKind `json:"kind"`
	Query         string    `json:"query"`
	Project       string    `json:"project"`
	Token         string    `json:"token"`
	ClientVersion string    `json:"client_version"`
	TraceID       string    `json:"trace_id"`
	Miss          bool      `json:"miss"`
}

// VisitGetActivityResponse sends the headers at once, then the history, then
// live events and keepalive comments. Once the headers are out it returns
// nil whatever happens: a failed write means the client left, and an error
// would have the strict handler write a second answer.
func (st *activityStream) VisitGetActivityResponse(w http.ResponseWriter) error {
	defer st.cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if !flush(rc) || st.head {
		return nil
	}
	var b []byte
	for _, e := range st.history {
		b = st.appendEvent(b, e)
	}
	if len(b) > 0 && !st.write(w, rc, b) {
		return nil
	}
	tick := time.NewTicker(st.keepalive)
	defer tick.Stop()
	for {
		select {
		case <-st.ctx.Done():
			return nil
		case <-st.done:
			return nil
		case e, open := <-st.live:
			if !open {
				return nil
			}
			b = st.appendEvent(b[:0], e)
		case <-tick.C:
			b = append(b[:0], ": keepalive\n\n"...)
		}
		if !st.write(w, rc, b) {
			return nil
		}
	}
}

// appendEvent appends e as one SSE event to b, after a dropped notice when
// events are missing between the last one written and e: ids from one
// Activity are consecutive, so a gap is what this subscriber lost.
func (st *activityStream) appendEvent(b []byte, e Event) []byte {
	if st.last != 0 && e.ID > st.last+1 {
		b = fmt.Appendf(b, "event: dropped\ndata: {\"dropped\":%d}\n\n", e.ID-st.last-1)
	}
	st.last = e.ID
	data, err := json.Marshal(streamEvent{
		ID: e.ID, Time: e.Time, Kind: e.Kind, Query: e.Query, Project: e.Project,
		Token: e.Token, ClientVersion: e.ClientVersion, TraceID: e.TraceID, Miss: e.Miss,
	})
	if err != nil { // strings, numbers and a time: it cannot fail
		return b
	}
	b = append(b, "data: "...)
	b = append(b, data...)
	return append(b, "\n\n"...)
}

// write writes b within streamWriteTimeout and flushes it, reporting whether
// the stream can go on.
func (st *activityStream) write(w http.ResponseWriter, rc *http.ResponseController, b []byte) bool {
	_ = rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
	if _, err := w.Write(b); err != nil {
		return false
	}
	return flush(rc)
}

// flush sends what is buffered to the client. A writer that cannot flush
// still streams, only later.
func flush(rc *http.ResponseController) bool {
	err := rc.Flush()
	return err == nil || errors.Is(err, http.ErrNotSupported)
}
