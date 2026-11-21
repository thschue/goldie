package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Check reports whether one dependency is ready. A nil error means ready.
type Check func(ctx context.Context) error

// DefaultCheckTimeout bounds each readiness check.
const DefaultCheckTimeout = 2 * time.Second

// Registry holds the readiness checks. Checks may be added while the server
// runs. Each runs under its own timeout.
type Registry struct {
	timeout  time.Duration
	mu       sync.RWMutex
	checks   map[string]Check
	draining atomic.Bool
}

// NewRegistry returns an empty registry. An empty registry is ready.
func NewRegistry(timeout time.Duration) *Registry {
	if timeout <= 0 {
		timeout = DefaultCheckTimeout
	}
	return &Registry{timeout: timeout, checks: map[string]Check{}}
}

// Add registers (or replaces) the named check.
func (r *Registry) Add(name string, c Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = c
}

// Drain makes the registry report not ready from now on (PC-08).
func (r *Registry) Drain() { r.draining.Store(true) }

type checkResult struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | failed | timeout
}

type readyBody struct {
	Status string        `json:"status"` // ready | not_ready | draining
	Checks []checkResult `json:"checks"`
}

// run executes every check concurrently and returns the JSON body and whether
// the server is ready. Check errors are not echoed: they may carry addresses
// or paths.
func (r *Registry) run(ctx context.Context) (readyBody, bool) {
	r.mu.RLock()
	checks := make(map[string]Check, len(r.checks))
	names := make([]string, 0, len(r.checks))
	for n, c := range r.checks {
		checks[n] = c
		names = append(names, n)
	}
	r.mu.RUnlock()
	sort.Strings(names)

	results := make([]checkResult, len(names))
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, r.timeout)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- checks[n](cctx) }()
			res := checkResult{Name: n, Status: "ok"}
			select {
			case err := <-done:
				if err != nil {
					res.Status = "failed"
				}
			case <-cctx.Done():
				res.Status = "timeout"
			}
			results[i] = res
		}()
	}
	wg.Wait()

	body := readyBody{Status: "ready", Checks: results}
	ok := true
	for _, res := range results {
		if res.Status != "ok" {
			ok = false
			body.Status = "not_ready"
		}
	}
	if r.draining.Load() {
		ok = false
		body.Status = "draining"
	}
	return body, ok
}

func (r *Registry) handler(w http.ResponseWriter, req *http.Request) {
	body, ok := r.run(req.Context())
	code := http.StatusOK
	if !ok {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, body)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
