package contract_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

// #281: the clients hand NewAPIClient an http.Client whose transport is
// wrapped for tracing and whose Timeout is 0. NewAPIClient applies its own
// 30-second request timeout whenever the given client has none, so a wrapped
// client keeps it; a client that sets its own timeout keeps that one. The
// timeout is observed as the deadline of the request the transport receives.
// It relies on newRecorder, newAPIClient and calls (client_test.go).

const wantRequestTimeout = 30 * time.Second

// deadlineTransport records the deadline of every request it passes on.
type deadlineTransport struct {
	base      http.RoundTripper
	mu        sync.Mutex
	deadlines []time.Duration // time left at RoundTrip; -1 for none
}

func (d *deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	left := time.Duration(-1)
	if dl, ok := r.Context().Deadline(); ok {
		left = time.Until(dl)
	}
	d.mu.Lock()
	d.deadlines = append(d.deadlines, left)
	d.mu.Unlock()
	return d.base.RoundTrip(r)
}

func (d *deadlineTransport) seen() []time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]time.Duration(nil), d.deadlines...)
}

// within reports whether left is want, give or take the time the call took.
func within(left, want time.Duration) bool {
	return left > want-5*time.Second && left <= want
}

func TestAPIClientAppliesItsTimeoutToAClientWithoutOne(t *testing.T) {
	rc := newRecorder(t)
	tr := &deadlineTransport{base: http.DefaultTransport}
	injected := &http.Client{Transport: tr} // Timeout 0, as the traced clients build it
	c := newAPIClient(t, rc.srv.URL, clientToken, injected)
	for op, call := range calls {
		if status, err := call(context.Background(), c); err != nil || status != http.StatusOK {
			t.Fatalf("%s: status %d, err %v", op, status, err)
		}
	}
	got := tr.seen()
	if len(got) != len(calls) {
		t.Fatalf("the transport saw %d requests, want %d", len(got), len(calls))
	}
	for i, left := range got {
		if left < 0 {
			t.Errorf("request %d has no deadline, want the %v request timeout", i, wantRequestTimeout)
		} else if !within(left, wantRequestTimeout) {
			t.Errorf("request %d: deadline in %v, want the %v request timeout", i, left, wantRequestTimeout)
		}
	}
	if injected.Timeout != 0 {
		t.Errorf("NewAPIClient changed the injected client's Timeout to %v; it must harden a copy", injected.Timeout)
	}
}

// A client that sets its own timeout keeps it, shorter or longer.
func TestAPIClientKeepsTheTimeoutOfAClientThatSetsOne(t *testing.T) {
	rc := newRecorder(t)
	for _, own := range []time.Duration{7 * time.Second, 90 * time.Second} {
		tr := &deadlineTransport{base: http.DefaultTransport}
		c := newAPIClient(t, rc.srv.URL, clientToken, &http.Client{Transport: tr, Timeout: own})
		if status, err := calls["search"](context.Background(), c); err != nil || status != http.StatusOK {
			t.Fatalf("search: status %d, err %v", status, err)
		}
		got := tr.seen()
		if len(got) != 1 || !within(got[0], own) {
			t.Errorf("own timeout %v: deadlines %v, want one of %v", own, got, own)
		}
	}
}
