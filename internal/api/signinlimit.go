package api

import (
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// Sign-in failures a host may make per window before sign-in answers 429, and
// how many hosts the limiter remembers at once.
const (
	signInFailures = 10
	signInWindow   = time.Minute
	signInMaxHosts = 10000
)

// SignInLimiter caps failed sign-ins per remote host: after signInFailures
// 401s from one host within signInWindow, every sign-in from it is refused
// with 429 and Retry-After until the window ends, a valid token included, so
// the answer reveals nothing about a guess. The host is the connection's
// remote address (IPv6 by its /64); no header is trusted, as there is no
// trusted-proxy setting. Only failures count. Memory is bounded: at
// signInMaxHosts hosts the expired ones go first, then an arbitrary one. The
// state is per process, like RateLimiter's. Its methods are safe for
// concurrent use.
type SignInLimiter struct {
	mu    sync.Mutex
	hosts map[string]*signInHost
}

type signInHost struct {
	fails int
	until time.Time // the window's end
}

// NewSignInLimiter returns an empty SignInLimiter.
func NewSignInLimiter() *SignInLimiter {
	return &SignInLimiter{hosts: map[string]*signInHost{}}
}

// signInKey is the host a request counts against: its remote address, with an
// IPv6 address reduced to its /64.
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

// Limit guards next, the sign-in handler: a limited host is answered 429, and
// a 401 from next counts as a failure for its host.
func (l *SignInLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := signInKey(r.RemoteAddr)
		if wait, limited := l.blocked(key); limited {
			w.Header().Set("Retry-After", strconv.FormatInt(max(int64((wait+time.Second-1)/time.Second), 1), 10))
			WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many failed sign-ins from this host",
				"Wait the number of seconds in the Retry-After header, then retry.")
			return
		}
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == http.StatusUnauthorized {
			l.fail(key)
		}
	})
}

// statusWriter remembers the status the handler wrote.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// blocked reports whether key is at its cap, and how long until its window ends.
func (l *SignInLimiter) blocked(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.hosts[key]
	if !ok {
		return 0, false
	}
	now := time.Now()
	if !now.Before(h.until) {
		delete(l.hosts, key)
		return 0, false
	}
	return h.until.Sub(now), h.fails >= signInFailures
}

// fail counts one failed sign-in for key.
func (l *SignInLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	h, ok := l.hosts[key]
	if !ok || !now.Before(h.until) {
		if !ok && len(l.hosts) >= signInMaxHosts {
			l.makeRoom(now)
		}
		h = &signInHost{until: now.Add(signInWindow)}
		l.hosts[key] = h
	}
	h.fails++
}

// makeRoom drops the expired hosts, and one arbitrary host when none expired.
func (l *SignInLimiter) makeRoom(now time.Time) {
	for k, h := range l.hosts {
		if !now.Before(h.until) {
			delete(l.hosts, k)
		}
	}
	if len(l.hosts) >= signInMaxHosts {
		for k := range l.hosts {
			delete(l.hosts, k)
			break
		}
	}
}
