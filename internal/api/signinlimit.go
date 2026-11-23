package api

import (
	"context"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// Sign-in failures an address may make per window before sign-in answers 429,
// and how many addresses the limiter remembers at once.
const (
	signInFailures = 10
	signInWindow   = time.Minute
	signInMaxHosts = 10000
)

// SignInLimiter caps failed sign-ins per remote address: after signInFailures
// failures from one address within signInWindow, every sign-in from it is
// refused with 429 and Retry-After until the window ends, a valid token
// included, so the answer reveals nothing about a guess. The key is the
// connection's remote address (IPv6 by its /64); no header is trusted, as
// there is no trusted-proxy setting. Only failures count.
//
// It is not a RateLimiter. That one is a token bucket over token names that
// admits requests and refills with time; this one is a fixed lockout window
// over addresses that counts only failures, so a success hands its slot back,
// and it must bound its own memory because a client chooses its address. Like
// RateLimiter it takes the slot under the lock before the handler runs, so
// concurrent requests cannot skip the cap.
//
// Memory is bounded: when signInMaxHosts addresses are held and none has
// expired, new addresses share one overflow entry. The state is per process.
// Its methods are safe for concurrent use.
type SignInLimiter struct {
	mu        sync.Mutex
	hosts     map[string]*signInHost
	overflow  signInHost
	lastSweep time.Time
}

type signInHost struct {
	fails int
	until time.Time // the window's end
}

// NewSignInLimiter returns an empty SignInLimiter.
func NewSignInLimiter() *SignInLimiter {
	return &SignInLimiter{hosts: map[string]*signInHost{}}
}

// signInKey is the address a request counts against: its remote address, with
// an IPv6 address reduced to its /64.
func signInKey(remote string) string {
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		return remote
	}
	a := ap.Addr().Unmap()
	if a.Is6() {
		if p, err := a.Prefix(64); err == nil {
			return p.String()
		}
	}
	return a.String()
}

type signInAttemptKey struct{}

// signInAttempt is what the sign-in handler tells Limit about one request.
type signInAttempt struct{ failed bool }

// ReportSignInFailure tells the SignInLimiter guarding the request that the
// sign-in failed, so its slot stays spent. Without a report the slot goes
// back. Outside a guarded request it does nothing.
func ReportSignInFailure(ctx context.Context) {
	if a, ok := ctx.Value(signInAttemptKey{}).(*signInAttempt); ok {
		a.failed = true
	}
}

// Limit guards next, the sign-in handler: a limited address is answered 429,
// and any other request spends a slot that is given back unless next calls
// ReportSignInFailure.
func (l *SignInLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, wait := l.take(signInKey(r.RemoteAddr), time.Now())
		if h == nil {
			secs := strconv.FormatInt(max(int64((wait+time.Second-1)/time.Second), 1), 10)
			w.Header().Set("Retry-After", secs)
			WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many failed sign-ins from this address",
				"Wait "+secs+" seconds, then retry.")
			return
		}
		attempt := &signInAttempt{}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), signInAttemptKey{}, attempt)))
		if !attempt.failed {
			l.giveBack(h)
		}
	})
}

// take spends one slot of key's window and returns its entry, or nil and how
// long until the window ends when the key is at its cap.
func (l *SignInLimiter) take(key string, now time.Time) (*signInHost, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.hosts[key]
	if !ok {
		if len(l.hosts) >= signInMaxHosts {
			l.sweep(now)
		}
		if len(l.hosts) >= signInMaxHosts {
			h = &l.overflow
		} else {
			h = &signInHost{}
			l.hosts[key] = h
		}
	}
	if !now.Before(h.until) {
		h.fails, h.until = 0, now.Add(signInWindow)
	}
	if h.fails >= signInFailures {
		return nil, h.until.Sub(now)
	}
	h.fails++
	return h, 0
}

// giveBack returns the slot take spent on h.
func (l *SignInLimiter) giveBack(h *signInHost) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if h.fails > 0 {
		h.fails--
	}
}

// sweep drops the expired addresses, at most once a second so a full table
// does not make every request scan it.
func (l *SignInLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Second {
		return
	}
	l.lastSweep = now
	for k, h := range l.hosts {
		if !now.Before(h.until) {
			delete(l.hosts, k)
		}
	}
}
