package platform

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// EnvTelemetryClientInfo opts server spans in to the caller's address and user
// agent (#89). Off by default: both identify a person's device.
const EnvTelemetryClientInfo = "GOLDIE_TELEMETRY_CLIENT_INFO"

// clientInfoEnabled reads and validates GOLDIE_TELEMETRY_CLIENT_INFO, which
// also accepts a _FILE form (PC-05). Errors name the variable, never the value.
func clientInfoEnabled(lookup Lookup) (bool, error) {
	v, err := Setting(lookup, os.ReadFile, EnvTelemetryClientInfo)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	}
	return false, fmt.Errorf("%s: must be true or false", EnvTelemetryClientInfo)
}

// originalKey carries the original request inside a span view's context.
type originalKey struct{}

// spanView returns the request otelhttp builds its span attributes from.
// otelhttp reads the client address from X-Forwarded-For, which any caller can
// forge, and adds the peer address and port and the user agent. A span
// processor cannot delete attributes, so the view never carries them: the
// forwarding and user agent headers are removed, and RemoteAddr is emptied, or
// with clientInfo reduced to the peer IP (no port). otelhttp also records the
// path as url.path, so it is replaced by what the path function returns
// (spanURLPath). The view is only for the span: restoreRequest gives the
// handler the original request.
func (t *Telemetry) spanView(r *http.Request, clientInfo bool) *http.Request {
	v := r.WithContext(context.WithValue(r.Context(), originalKey{}, r))
	v.URL = t.redactedURL(r.URL)
	v.Header = r.Header.Clone()
	v.Header.Del("X-Forwarded-For")
	v.RemoteAddr = ""
	if !clientInfo {
		v.Header.Del("User-Agent")
		return v
	}
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		ip := ap.Addr().WithZone("")
		if ip.Is6() {
			v.RemoteAddr = "[" + ip.String() + "]" // otelhttp reads a bare IPv6 address only in brackets
		} else {
			v.RemoteAddr = ip.String()
		}
	}
	return v
}

// hostFreeExporter drops server.address and server.port from server spans.
// otelhttp fills both from the Host header, which any caller sets, and always
// emits server.address, even for an empty Host, so spanView cannot hide them.
// Client spans keep theirs: they name the API the client calls, not a caller's
// choice.
type hostFreeExporter struct{ sdktrace.SpanExporter }

func (e hostFreeExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	out := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, sp := range spans {
		out[i] = sp
		if sp.SpanKind() == oteltrace.SpanKindServer {
			out[i] = hostFreeSpan{sp}
		}
	}
	return e.SpanExporter.ExportSpans(ctx, out)
}

type hostFreeSpan struct{ sdktrace.ReadOnlySpan }

func (s hostFreeSpan) Attributes() []attribute.KeyValue {
	in := s.ReadOnlySpan.Attributes()
	out := make([]attribute.KeyValue, 0, len(in))
	for _, kv := range in {
		if k := string(kv.Key); k != "server.address" && k != "server.port" {
			out = append(out, kv)
		}
	}
	return out
}

// noSpanPath is the path of a span when no path function was given: no part
// of the request path is exported.
const noSpanPath = "/{unknown}"

// WithSpanPath sets the function that turns a request path into the path a
// span carries, so platform names no route: internal/app builds it from the
// contract's templates. fn gets the escaped path as sent (no query, no
// cleaning). Without it, or with nil, spans carry one fixed placeholder.
func WithSpanPath(fn func(path string) string) telemetryOption {
	return func(c *telemetryConfig) { c.spanPath = fn }
}

// spanURLPath is the one place a span's path is decided: the path function's
// answer for u's escaped path, or the placeholder when there is none or it
// answers with nothing.
func (t *Telemetry) spanURLPath(u *url.URL) string {
	if t == nil || t.spanPath == nil {
		return noSpanPath
	}
	if p := t.spanPath(u.EscapedPath()); p != "" {
		return p
	}
	return noSpanPath
}

// redactedURL is a copy of u whose path is spanURLPath of it, so no id or raw
// path reaches a span. The query string is dropped too: otelhttp does not
// record it, and the view has no use for search words.
func (t *Telemetry) redactedURL(u *url.URL) *url.URL {
	c := *u
	c.RawQuery, c.ForceQuery = "", false
	c.Path, c.RawPath = t.spanURLPath(u), ""
	return &c
}

// restoreRequest runs h with the original request that spanView hid, keeping
// the context (and so the span) of the view otelhttp passed on.
func restoreRequest(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, view *http.Request) {
		if orig, ok := view.Context().Value(originalKey{}).(*http.Request); ok {
			// Body is otelhttp's wrapper: it counts request bytes for the span
			// and metrics, so the handler must read through it, not orig.Body.
			restored := orig.WithContext(view.Context())
			restored.Body = view.Body
			view = restored
			// A form the handler parses lands on the copy; net/http removes
			// its temp files only through the original, so copy it back (as
			// otelhttp does).
			defer func() {
				if restored.MultipartForm != nil {
					orig.MultipartForm = restored.MultipartForm
				}
			}()
		}
		h.ServeHTTP(w, view)
	})
}
