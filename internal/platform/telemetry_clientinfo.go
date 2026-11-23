package platform

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
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
// with clientInfo reduced to the peer IP (no port). otelhttp also records the
// path as url.path, so an article id in it is replaced by the route template
// (articlePath). The view is only for the span: restoreRequest gives the
// handler the original request.
func spanView(r *http.Request, clientInfo bool) *http.Request {
	v := r.WithContext(context.WithValue(r.Context(), originalKey{}, r))
	v.URL = redactedURL(r.URL)
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

// articlePath is the route template of the article lookup. The id after it
// is what a caller asked for, so it never becomes a span attribute.
const articlePath = "/api/v1/articles/"

// redactedURL is a copy of u whose path, when it is under articlePath (after
// cleaning, as the router matches it), is the route template instead. The
// query string is dropped too: otelhttp does not record it, and the view has
// no use for search words.
func redactedURL(u *url.URL) *url.URL {
	c := *u
	c.RawQuery, c.ForceQuery = "", false
	if strings.HasPrefix(path.Clean("/"+c.Path), articlePath) {
		c.Path, c.RawPath = articlePath+"{id}", ""
	}
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
