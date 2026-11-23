package platform

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
)

// The goldie.mode of each client role's telemetry, for SetupClientTelemetry.
const (
	ModeCLI = "cli" // the client commands
	ModeMCP = "mcp" // goldie mcp over stdio
)

// SetupClientTelemetry is SetupTelemetry for a client role: traces only, with
// goldie.mode set to mode (ModeCLI or ModeMCP). Logs and metrics are never
// exported, whatever their variables say, and GOLDIE_TELEMETRY_CLIENT_INFO
// does not apply. It is off unless an endpoint or OTEL_TRACES_EXPORTER
// selects traces.
func SetupClientTelemetry(ctx context.Context, lookup Lookup, mode string) (*Telemetry, error) {
	if mode != ModeCLI && mode != ModeMCP {
		return nil, errors.New("client telemetry: the mode must be cli or mcp")
	}
	return setupTelemetry(ctx, lookup, mode)
}

// WrapTransport traces each request through rt as a client span and sends it
// as W3C traceparent (TraceContext only, never baggage). The server starts its
// own trace and links to the client span (WrapHandler). The span is built from
// clientSpanView, so it carries no query, header, userinfo or article id; the
// request on the wire is unchanged but for traceparent. rt nil means
// http.DefaultTransport. Without traces it returns rt as is.
func (t *Telemetry) WrapTransport(rt http.RoundTripper) http.RoundTripper {
	if t == nil || t.tp == nil {
		return rt
	}
	base := rt
	if base == nil {
		base = http.DefaultTransport
	}
	traced := otelhttp.NewTransport(restoreClientRequest{base: base},
		otelhttp.WithTracerProvider(t.tp),
		otelhttp.WithMeterProvider(metricnoop.NewMeterProvider()),
		otelhttp.WithPropagators(propagation.TraceContext{}),
		otelhttp.WithSpanNameFormatter(spanName),
	)
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return traced.RoundTrip(clientSpanView(r))
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// clientArticleRoute is the article lookup's path, up to the id.
const clientArticleRoute = "/api/v1/articles/"

// clientSpanView returns the request otelhttp builds the client span from: no
// headers (Authorization among them), no userinfo, query or fragment, and an
// article lookup's path cut to the route template /api/v1/articles/{id}. The
// original request rides in its context for restoreClientRequest.
func clientSpanView(r *http.Request) *http.Request {
	v := r.WithContext(context.WithValue(r.Context(), originalKey{}, r))
	u := *r.URL
	u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
	p := path.Clean("/" + u.Path)
	if i := strings.Index(p, clientArticleRoute); i >= 0 { // a base URL may have a path prefix
		p = p[:i] + clientArticleRoute + "{id}"
	}
	u.Path, u.RawPath = p, ""
	v.URL = &u
	v.Header = http.Header{}
	return v
}

// restoreClientRequest sends the original request clientSpanView hid, with
// the context (and so the span) and the trace context headers otelhttp put
// on the view.
type restoreClientRequest struct{ base http.RoundTripper }

func (t restoreClientRequest) RoundTrip(view *http.Request) (*http.Response, error) {
	orig, ok := view.Context().Value(originalKey{}).(*http.Request)
	if !ok {
		return t.base.RoundTrip(view)
	}
	out := orig.Clone(view.Context())
	out.Body = view.Body // otelhttp's wrapper, as in restoreRequest
	for _, name := range (propagation.TraceContext{}).Fields() {
		if v := view.Header.Get(name); v != "" {
			out.Header.Set(name, v)
		}
	}
	return t.base.RoundTrip(out)
}
