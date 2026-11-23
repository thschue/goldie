package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/platform"
)

// Regression guard for #243, round 2: the history is written in chunks, and
// a busy server's history spans many of them (the Activity keeps 1000
// events). A history of more than two chunks, not a multiple of the chunk
// size, arrives whole: every id once, in order, no dropped notice, each event
// an ActivityEvent; and the first live event follows it with the next id.
func TestALongActivityHistoryArrivesWholeAndInOrderAcrossChunks(t *testing.T) {
	const history = 123
	pub := streamHandler(t, platform.Config{}, api.DefaultActivityKeepalive)
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	for i := range history {
		if rec := serveAs(t, pub, searchPath(fmt.Sprintf("history %d", i+1)), func(*http.Request) {}); rec.Code != http.StatusOK {
			t.Fatalf("fixture: search %d: status %d", i+1, rec.Code)
		}
	}
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	events := make([]sseEvent, 0, history+1)
	for range history {
		events = append(events, nextEvent(t, s, 5*time.Second))
	}
	if rec := serveAs(t, pub, searchPath("live"), func(*http.Request) {}); rec.Code != http.StatusOK {
		t.Fatalf("fixture: live search: status %d", rec.Code)
	}
	events = append(events, nextEvent(t, s, 5*time.Second))

	got := validateStream(t, events)
	for i, e := range events {
		if e.Type == "dropped" {
			t.Fatalf("event %d is a dropped notice: a subscriber that kept up lost events at a chunk boundary", i)
		}
		id, _ := got[i]["id"].(float64)
		want := fmt.Sprintf("history %d", i+1)
		if i == history {
			want = "live"
		}
		if id != float64(i+1) || got[i]["query"] != want {
			t.Errorf("event %d: id %v query %v, want id %d %q (each id once, in order, history then live)",
				i, got[i]["id"], got[i]["query"], i+1, want)
		}
	}
}
