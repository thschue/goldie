package api

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Limit belongs inside a Require guard. Wired without one, no request has a
// token name, so every request is the 500 envelope and next never runs: a
// wiring mistake fails closed instead of sharing one unnamed bucket.
func TestRateLimiterWithoutATokenNameFailsClosed(t *testing.T) {
	called := false
	h := NewRateLimiter(20, time.Second, 40).Limit(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/search?q=x", nil))
	if rec.Code != http.StatusInternalServerError || called {
		t.Errorf("status %d, next called %v: want 500 and next not called", rec.Code, called)
	}
}

// The largest values GOLDIE_RATE_LIMIT accepts neither overflow nor turn the
// limit into a rejection of every request.
func TestRateLimiterExtremeValuesDoNotOverflow(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	tokens, err := OperatorToken(secret)
	if err != nil {
		t.Fatal(err)
	}
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for name, l := range map[string]*RateLimiter{
		"max/m,burst=max": NewRateLimiter(math.MaxInt, time.Minute, math.MaxInt),
		"1/m,burst=max":   NewRateLimiter(1, time.Minute, math.MaxInt),
		"max/s,burst=1":   NewRateLimiter(math.MaxInt, time.Second, 1),
	} {
		h := tokens.Require(RoleReader, l.Limit(ok))
		for i := range 100 {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/search?q=x", nil)
			req.Header.Set("Authorization", "Bearer "+secret)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: request %d: status %d, want 200", name, i+1, rec.Code)
			}
		}
	}
}
