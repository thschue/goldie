package platform

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
)

// The goldie.role of each client role's telemetry, for SetupClientTelemetry.
const (
	RoleCLI = "cli" // the client commands
	RoleMCP = "mcp" // goldie mcp over stdio
)

// SetupClientTelemetry is SetupTelemetry for a client role: traces only, with
// goldie.role set to role (RoleCLI or RoleMCP), and no goldie.mode. Logs and metrics are never
// exported, whatever their variables say, and GOLDIE_TELEMETRY_CLIENT_INFO
// does not apply. It is off unless an endpoint or OTEL_TRACES_EXPORTER
// selects traces. role is a plain string because callers range over the
// roles as strings; any other value is an error.
func SetupClientTelemetry(ctx context.Context, lookup Lookup, role string, opts ...telemetryOption) (*Telemetry, error) {
	if role != RoleCLI && role != RoleMCP {
		return nil, errors.New("client telemetry: the role must be cli or mcp")
	}
	return setupTelemetry(ctx, lookup, role, opts...)
}

// RouteNoticesTo sends what the OpenTelemetry SDK reports to l, as one fixed
// warning that never carries the SDK's text (see sdkNotice). A client role
// passes a stderr logger, so stdout keeps its own output. It does nothing
// when telemetry is off.
func (t *Telemetry) RouteNoticesTo(l *slog.Logger) {
	if !t.Enabled() {
		return
	}
	t.notice.install()
	t.notice.attach(l)
}

// WrapTransport traces each request through rt as a client span and sends it
// as W3C traceparent (TraceContext only, never baggage). The server starts its
// own trace and links to the client span (WrapHandler). The span is built from
// clientSpanView, so it carries no query, header, userinfo or id; the
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
		return traced.RoundTrip(t.clientSpanView(r))
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// clientSpanView returns the request otelhttp builds the client span from: no
// headers (Authorization among them), no userinfo, query or fragment, and the
// path from spanURLPath. The original request rides in its context for
// restoreClientRequest.
func (t *Telemetry) clientSpanView(r *http.Request) *http.Request {
	v := r.WithContext(context.WithValue(r.Context(), originalKey{}, r))
	u := *r.URL
	u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
	u.Path, u.RawPath = t.spanURLPath(r.URL), ""
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
	if out.Header == nil {
		out.Header = http.Header{}
	}
	for _, name := range (propagation.TraceContext{}).Fields() {
		if v := view.Header.Get(name); v != "" {
			out.Header.Set(name, v)
		}
	}
	return t.base.RoundTrip(out)
}
