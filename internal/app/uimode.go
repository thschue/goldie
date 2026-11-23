package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/platform"
)

// upstreamCheck is the one readiness check of a GOLDIE_MODE=ui server (PC-02).
const upstreamCheck = "upstream"

// upstreamHeaderTimeout bounds the wait for an upstream's response headers; a
// stream (the activity feed) may run on after them.
const upstreamHeaderTimeout = 30 * time.Second

// uiUpstreamPaths are the paths a ui-mode server proxies. A path with a
// trailing slash is a subtree.
var uiUpstreamPaths = []string{"/api", "/api/", "/openapi.json", "/openapi.yaml", "/swagger-ui", "/swagger-ui/"}

// newPublicUI builds the public handler of GOLDIE_MODE=ui: the embedded UI,
// with /api, the OpenAPI documents and Swagger UI reverse-proxied to
// cfg.UpstreamAddr and /mcp answering the 404 envelope. It holds no token, no
// rate limit and no session key: the caller's own credential, Host, Origin
// and cookies reach the upstream unchanged (ADR 0003, ADR 0027), and no
// forwarding header is trusted or added. Errors name the variable, never the
// address.
func newPublicUI(cfg platform.Config, logger *slog.Logger) (*Public, error) {
	if logger == nil {
		logger = discardLogger
	}
	if err := checkUpstreamAddr(platform.EnvUpstreamAddr, cfg.UpstreamAddr); err != nil {
		return nil, &ConfigError{err}
	}
	target, err := url.Parse(cfg.UpstreamAddr)
	if err != nil {
		return nil, &ConfigError{fmt.Errorf("%s: not a URL", platform.EnvUpstreamAddr)}
	}
	// life ends with the server (Public.Start), which cancels the held streams
	// among the requests, so they do not stall the drain (PC-08).
	life, endStreams := context.WithCancel(context.Background())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = upstreamHeaderTimeout
	failures := &failureLog{}
	proxy := &httputil.ReverseProxy{
		// Rewrite sees the request without Forwarded or X-Forwarded-*, and
		// adds none: SetXForwarded is deliberately not called.
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = r.In.Host
		},
		FlushInterval: -1,
		Transport:     transport,
		ModifyResponse: func(resp *http.Response) error {
			// Only a stream (the activity feed) is held open past the first
			// stop signal; any other request finishes during the drain.
			if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
				if hold, ok := resp.Request.Context().Value(streamHoldKey{}).(func()); ok {
					hold()
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// The error holds the upstream's address: it reaches the
			// operator's log and never the client.
			failures.log(logger, r, err)
			api.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "the upstream is not reachable",
				"Retry shortly; if it persists, check that the server behind GOLDIE_UPSTREAM_ADDR is running.")
		},
	}
	mux := http.NewServeMux()
	m := &publicMux{mux: mux, endStreams: endStreams}
	toUpstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An encoded dot segment (/api/%2e%2e/x) would be resolved by the
		// upstream or a proxy in front of it, outside the allowlist: answer
		// 404. A trailing slash is a subtree and an encoded slash inside a
		// segment (a Project ID) stays.
		if !cleanPath(r.URL.Path) {
			api.WriteError(w, http.StatusNotFound, "not_found", "no such endpoint", "A path with a . or .. segment is not served.")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		var release func() bool
		hold := func() { release = context.AfterFunc(life, cancel) }
		ctx = context.WithValue(ctx, streamHoldKey{}, hold)
		proxy.ServeHTTP(w, r.WithContext(ctx))
		if release != nil {
			release()
		}
	})
	for _, p := range uiUpstreamPaths {
		mux.Handle(p, toUpstream)
	}
	notFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, http.StatusNotFound, "not_found", "no such endpoint",
			"This server runs with GOLDIE_MODE=ui and does not serve /mcp.")
	})
	mux.Handle("/mcp", notFound)
	mux.Handle("/mcp/", notFound)
	addUI(m, notFound, logger)
	return &Public{
		publicMux: m,
		source:    source{check: upstreamCheck},
		probe:     upstreamProbe(target),
	}, nil
}

// upstreamProbe is the readiness check of the upstream: it asks for an /api/
// path without a credential and is ready when the upstream answers over HTTP
// with a JSON body, which a goldie server's 401 envelope is. Redirects are not
// followed, and the error never holds the address.
func upstreamProbe(target *url.URL) func(context.Context) error {
	client := &http.Client{
		Timeout:       platform.DefaultCheckTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	probe := target.JoinPath("/api/")
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.String(), nil)
		if err != nil {
			return errors.New("bad probe request")
		}
		resp, err := client.Do(req) //nolint:gosec // the operator's own GOLDIE_UPSTREAM_ADDR
		if err != nil {
			return errors.New("upstream not reachable")
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= http.StatusInternalServerError || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
			return errors.New("upstream is not a goldie server")
		}
		return nil
	}
}

// streamHoldKey carries, in a proxied request's context, the func that ties
// the request to the server's life. ModifyResponse calls it for a stream.
type streamHoldKey struct{}

// cleanPath reports whether p holds no "." or ".." segment, which path.Clean
// would resolve. A trailing slash is allowed.
func cleanPath(p string) bool {
	clean := path.Clean(p)
	if strings.HasSuffix(p, "/") && clean != "/" {
		clean += "/"
	}
	return clean == p
}

// failureInterval is the least time between two logged upstream failures: the
// UI polls, so an outage would otherwise fill the log.
const failureInterval = 30 * time.Second

// failureLog logs a failed proxied request as one warn line per interval.
type failureLog struct {
	mu         sync.Mutex
	last       time.Time
	suppressed int
}

func (f *failureLog) log(logger *slog.Logger, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return // the client left; the upstream did nothing wrong
	}
	f.mu.Lock()
	if !f.last.IsZero() && time.Since(f.last) < failureInterval {
		f.suppressed++
		f.mu.Unlock()
		return
	}
	suppressed := f.suppressed
	f.last, f.suppressed = time.Now(), 0
	f.mu.Unlock()
	class := "unreachable"
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		class = "timeout"
	}
	// The operator's own log may hold the address.
	logger.Warn("upstream request failed", "class", class, "error", err.Error(), "suppressed", suppressed)
}
