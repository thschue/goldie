package contract_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/contract"
)

// Acceptance tests for the history byte budget (#245, PR #378 round 1,
// security-reviewer LOW): before its live marker, StreamActivity reads at
// most contract.MaxResponseBytes in total, counting every byte (events,
// comments, other event types and malformed data alike), not only
// MaxActivityHistory events, each of which may be up to MaxResponseBytes.
// Past the budget it fails as a stream without a live marker does: an error,
// never a partial history, naming no address, and well before idle.

// budgetMargin keeps the cases clear of the exact boundary, so what counts
// toward the budget (field names, newlines, the marker itself) is left to the
// implementation.
const budgetMargin = 1 << 20

// paddedEvent is one ActivityEvent line of about size bytes.
func paddedEvent(id, size int) string {
	return sseData(id, strings.Repeat("q", size))
}

// floodThenLive answers with chunks until more than total bytes are written,
// then sends the live marker and holds the stream open: a reader without a
// byte budget gets a history and succeeds.
func floodThenLive(total int, chunk func(i int) string) func(ctx context.Context, w http.ResponseWriter, flush func()) {
	return func(ctx context.Context, w http.ResponseWriter, flush func()) {
		written := 0
		for i := 1; written <= total; i++ {
			n, err := w.Write([]byte(chunk(i)))
			if err != nil {
				return
			}
			written += n
		}
		_, _ = w.Write([]byte(liveMarker))
		flush()
		<-ctx.Done()
	}
}

// assertBudgetRefusal checks a StreamActivity past the budget: an error and
// no history, quickly (idle is a minute), naming no address, with the
// connection closed.
func assertBudgetRefusal(t *testing.T, s *sseServer) {
	t.Helper()
	start := time.Now()
	r := openActivity(t, t.Context(), s.srv.URL, nil, time.Minute, 30*time.Second)
	if r.err == nil {
		t.Fatalf("a history past the %d-byte budget: %d events and a channel, want an error", contract.MaxResponseBytes, len(r.history))
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("the refusal took %v: the budget, not idle (1m), must end the read", d)
	}
	host := strings.TrimPrefix(s.srv.URL, "http://")
	for _, bad := range []string{host, "127.0.0.1", "qqqq", "pad"} {
		if strings.Contains(r.err.Error(), bad) {
			t.Errorf("error %q carries %q", r.err, bad)
		}
	}
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Error("the connection is still open after StreamActivity refused the history")
	}
}

// Events each far below the per-event cap, and far fewer than
// MaxActivityHistory, whose total passes MaxResponseBytes before the marker:
// an error, not a history.
func TestStreamActivityRefusesAHistoryPastTheByteBudget(t *testing.T) {
	const eventSize = 64 << 10 // 64 KiB: 1/256 of the per-event cap
	n := (contract.MaxResponseBytes + budgetMargin) / eventSize
	if n >= contract.MaxActivityHistory {
		t.Fatalf("fixture: %d events would reach the event cap; the test must pass the byte budget alone", n)
	}
	s := newSSEServer(t, floodThenLive(contract.MaxResponseBytes+budgetMargin, func(i int) string { return paddedEvent(i, eventSize) }))
	assertBudgetRefusal(t, s)
}

// Skipped data counts too: comments, events of other types and malformed
// data lines totalling past MaxResponseBytes before the marker are refused,
// so a flood of skipped lines cannot evade the budget.
func TestStreamActivityCountsSkippedDataAgainstTheByteBudget(t *testing.T) {
	pad := strings.Repeat("p", 4<<10)
	for name, chunk := range map[string]func(int) string{
		"comments":       func(int) string { return ": pad " + pad + "\n\n" },
		"other types":    func(i int) string { return "event: future\ndata: " + eventData(i, pad) + "\n\n" },
		"dropped":        func(int) string { return "event: dropped\ndata: {\"dropped\":1,\"pad\":\"" + pad + "\"}\n\n" },
		"malformed data": func(int) string { return "data: not json " + pad + "\n\n" },
		"unknown fields": func(int) string { return "x-" + pad + ": y\n\n" },
	} {
		t.Run(name, func(t *testing.T) {
			s := newSSEServer(t, floodThenLive(contract.MaxResponseBytes+budgetMargin, chunk))
			assertBudgetRefusal(t, s)
		})
	}
}

// The honest case: a history under the budget, followed by the marker, is
// read whole, in order, whatever was skipped on the way, and the live events
// follow.
func TestStreamActivityReadsAnHonestHistoryUnderTheByteBudget(t *testing.T) {
	const eventSize = 64 << 10
	n := (contract.MaxResponseBytes - budgetMargin) / eventSize
	s := newSSEServer(t, func(ctx context.Context, w http.ResponseWriter, flush func()) {
		for i := 1; i <= n; i++ {
			if _, err := w.Write([]byte(": keepalive\n\n" + paddedEvent(i, eventSize))); err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(liveMarker + sseData(n+1, "live")))
		flush()
		<-ctx.Done()
	})
	r := open(t, t.Context(), s.srv.URL, nil, time.Minute)
	if len(r.history) != n {
		t.Fatalf("history of %d events, want all %d (under the budget by %d bytes)", len(r.history), n, budgetMargin)
	}
	for i, e := range r.history {
		if e.Id != int64(i+1) {
			t.Fatalf("history[%d] has id %d, want %d", i, e.Id, i+1)
		}
	}
	if e, ok := recv(t, r.live, 5*time.Second); !ok || e.Query != "live" {
		t.Errorf("live event %+v (open %v)", e, ok)
	}
}
