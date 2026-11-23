package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const signInTestRemote = "192.0.2.7:4000"

func signInRequest(t *testing.T) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/session", nil)
	req.RemoteAddr = signInTestRemote
	return req
}

// Parallel wrong-token sign-ins from one address cannot get more than the cap
// through to the handler: the slot is taken before the handler runs, not
// counted after it answers.
func TestSignInLimiterHoldsUnderParallelFailures(t *testing.T) {
	const parallel = 60
	gate := make(chan struct{})
	var entered atomic.Int32
	h := NewSignInLimiter().Limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered.Add(1)
		<-gate // a slow body: the failure is reported only after the others arrived
		ReportSignInFailure(r.Context())
		w.WriteHeader(http.StatusUnauthorized)
	}))

	var refused atomic.Int32
	var wg sync.WaitGroup
	for range parallel {
		wg.Go(func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, signInRequest(t))
			if rec.Code == http.StatusTooManyRequests {
				refused.Add(1)
			}
		})
	}
	deadline := time.Now().Add(10 * time.Second)
	for refused.Load() < parallel-signInFailures && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(gate)
	wg.Wait()

	if got := entered.Load(); got != signInFailures {
		t.Errorf("%d requests reached the handler, want exactly the cap of %d", got, signInFailures)
	}
	if got := refused.Load(); got != parallel-signInFailures {
		t.Errorf("%d requests were refused, want %d", got, parallel-signInFailures)
	}
}

// At the end of a window the hint says "1 second", not "1 seconds".
func TestSignInLimiterHintIsSingularForOneSecond(t *testing.T) {
	l := NewSignInLimiter()
	h := l.Limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ReportSignInFailure(r.Context())
		w.WriteHeader(http.StatusUnauthorized)
	}))
	for range signInFailures {
		h.ServeHTTP(httptest.NewRecorder(), signInRequest(t))
	}
	l.mu.Lock()
	l.hosts[signInKey(signInTestRemote)].until = time.Now().Add(300 * time.Millisecond)
	l.mu.Unlock()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signInRequest(t))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", rec.Code)
	}
	var env struct {
		Error struct {
			Details struct {
				Hint string `json:"hint"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if want := "Wait 1 second, then retry."; env.Error.Details.Hint != want {
		t.Errorf("hint %q, want %q", env.Error.Details.Hint, want)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After %q, want 1", got)
	}
}
