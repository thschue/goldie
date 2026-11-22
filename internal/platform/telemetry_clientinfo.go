package platform

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"strings"
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
// with clientInfo reduced to the peer IP (no port). The view is only for the
// span: restoreRequest gives the handler the original request.
func spanView(r *http.Request, clientInfo bool) *http.Request {
	v := r.WithContext(context.WithValue(r.Context(), originalKey{}, r))
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

// restoreRequest runs h with the original request that spanView hid, keeping
// the context (and so the span) of the view otelhttp passed on.
func restoreRequest(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, view *http.Request) {
		if orig, ok := view.Context().Value(originalKey{}).(*http.Request); ok {
			view = orig.WithContext(view.Context())
		}
		h.ServeHTTP(w, view)
	})
}
