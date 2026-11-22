package api

import (
	"maps"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter limits requests per token name (TokenName) with one token
// bucket each, held in memory. It never keys on an address or a header the
// client controls. The buckets are a GCRA: each name has a theoretical
// arrival time that every allowed request moves one interval ahead, and a
// request is rejected while that time is more than the burst tolerance in the
// future. Its methods are safe for concurrent use.
type RateLimiter struct {
	interval  time.Duration // time between requests at the sustained rate
	tolerance time.Duration // how far ahead of now a bucket may run: the burst
	start     time.Time     // monotonic origin of the arrival times

	mu         sync.Mutex
	tat        map[string]time.Duration // theoretical arrival time per name, since start
	rejections map[string]uint64
}

// maxTolerance keeps the arithmetic far from overflow for any burst.
const maxTolerance = time.Duration(math.MaxInt64 / 4)

// NewRateLimiter allows requests per per for each token name, in bursts of up
// to burst requests. requests and burst are positive.
func NewRateLimiter(requests int, per time.Duration, burst int) *RateLimiter {
	interval := per / time.Duration(max(requests, 1))
	tolerance := maxTolerance
	if interval > 0 && int64(max(burst, 1)-1) < int64(maxTolerance/interval) {
		tolerance = interval * time.Duration(max(burst, 1)-1)
	}
	return &RateLimiter{
		interval: interval, tolerance: tolerance, start: time.Now(),
		tat: map[string]time.Duration{}, rejections: map[string]uint64{},
	}
}

// Limit lets a request through to next while its token's bucket has room,
// and otherwise answers 429 in the error envelope with Retry-After in whole
// seconds, rounded up and at least 1. It goes inside a Require guard, which
// sets the token name; a request without one is an internal error, so a
// wiring mistake fails closed.
func (l *RateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := TokenName(r.Context())
		if !ok || name == "" {
			responseError(w, r, nil)
			return
		}
		wait, allowed := l.take(name)
		if !allowed {
			secs := max(int64((wait+time.Second-1)/time.Second), 1)
			w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
			WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests for this API token",
				"Wait the number of seconds in the Retry-After header, then retry.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// take spends one request from name's bucket, or reports how long until it
// may send again and counts the rejection.
func (l *RateLimiter) take(name string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Since(l.start)
	tat := max(l.tat[name], now)
	if ahead := tat - now; ahead > l.tolerance {
		l.rejections[name]++
		return ahead - l.tolerance, false
	}
	l.tat[name] = tat + l.interval
	return 0, true
}

// Rejections returns a copy of the rejected requests per token name since the
// limiter was built, for the goldie.ratelimit.rejections{token} metric.
func (l *RateLimiter) Rejections() map[string]uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return maps.Clone(l.rejections)
}
