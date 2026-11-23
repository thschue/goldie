package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
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
	// life ends with the server (Public.Start), which cancels the requests
	// still open so a held stream does not stall the drain (PC-08).
	life, endStreams := context.WithCancel(context.Background())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = upstreamHeaderTimeout
	proxy := &httputil.ReverseProxy{
		// Rewrite sees the request without Forwarded or X-Forwarded-*, and
		// adds none: SetXForwarded is deliberately not called.
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = r.In.Host
		},
		FlushInterval: -1,
		Transport:     transport,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			// The error holds the upstream's address: it is not echoed.
			api.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "the upstream is not reachable",
				"Retry shortly; if it persists, check that the server behind GOLDIE_UPSTREAM_ADDR is running.")
		},
	}
	mux := http.NewServeMux()
	m := &publicMux{mux: mux, endStreams: endStreams}
	toUpstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer context.AfterFunc(life, cancel)()
		proxy.ServeHTTP(w, r.WithContext(ctx))
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
