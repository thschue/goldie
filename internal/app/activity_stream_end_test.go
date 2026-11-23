package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/platform"
)

// The server's read timeout (appReadTimeout in platform) bounds reading the
// request, not the stream: a stream open longer than it still gets events.
func TestActivityStreamOutlivesTheReadTimeout(t *testing.T) {
	pub := streamHandler(t, platform.Config{}, api.DefaultActivityKeepalive)
	srv := httptest.NewUnstartedServer(pub)
	srv.Config.ReadTimeout = 100 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	time.Sleep(400 * time.Millisecond)
	if rec := serveAs(t, pub, searchPath("late"), func(*http.Request) {}); rec.Code != http.StatusOK {
		t.Fatalf("fixture: status %d", rec.Code)
	}
	if e := decodeEvent(t, nextEvent(t, s, 5*time.Second)); e.Query != "late" {
		t.Errorf("event query %q, want \"late\"", e.Query)
	}
}

// When the context Start runs under ends (the first stop signal), open
// streams end cleanly, a new one is 503 shutting_down, and no slot is held.
func TestActivityStreamsEndWhenStartsContextEnds(t *testing.T) {
	pub := streamHandler(t, platform.Config{}, api.DefaultActivityKeepalive)
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	pub.Start(ctx, platform.NewRegistry(0))
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	stop()
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("the stream ended with %v, want a clean end", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream was still open 5s after Start's context ended")
	}
	resp, _, body = openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a stream after the stop: status %d, want 503", resp.StatusCode)
	}
	assertEnvelope(t, body, "shutting_down")
	waitHubEmpty(t, pub.Activity())
}
