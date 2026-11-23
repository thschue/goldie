package app

import (
	"net/url"
	"strings"
	"sync"

	"github.com/thschue/goldie/internal/contract"
)

// spanPathPlaceholder is what a span carries for a path that is no route of
// the contract. It is no template and holds nothing of the request.
const spanPathPlaceholder = "/{unknown}"

// maxSpanPath bounds the work for one path; a longer one is no route.
const maxSpanPath = 2048

// spanRoute is a path template split into segments, lowercased; "" after the
// leading one marks a {parameter} segment.
type spanRoute struct {
	template string
	segments []string
}

var (
	spanRoutesOnce sync.Once
	spanRoutes     []spanRoute
)

// loadSpanRoutes reads the contract's route templates once. When the spec
// cannot be read there are no routes, so every path gets the placeholder.
func loadSpanRoutes() []spanRoute {
	spanRoutesOnce.Do(func() {
		templates, err := contract.RouteTemplates()
		if err != nil {
			return
		}
		for _, t := range templates {
			segs := strings.Split(strings.ToLower(t), "/")
			for i, s := range segs {
				if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
					segs[i] = ""
				}
			}
			spanRoutes = append(spanRoutes, spanRoute{template: t, segments: segs})
		}
	})
	return spanRoutes
}

// SpanPath is the path a span may carry for the request path p (escaped, no
// query): the contract's template for a known route, whatever the case of the
// request, and a fixed placeholder for anything else. The route is matched on
// the decoded segments of p as sent, before any cleaning, so an encoded slash
// stays inside one id segment and a dot segment makes p no route. It is the
// function platform's telemetry is given (platform.WithSpanPath).
func SpanPath(p string) string {
	segs, ok := spanSegments(p)
	if !ok {
		return spanPathPlaceholder
	}
	return matchSpanRoute(segs)
}

// ClientSpanPath is SpanPath for a client, whose request path starts with the
// path of the API's base URL (a reverse proxy may serve goldie under a
// prefix): the route is the longest tail of p that is one, and the prefix is
// not exported.
func ClientSpanPath(p string) string {
	segs, ok := spanSegments(p)
	if !ok {
		return spanPathPlaceholder
	}
	for i := range segs {
		if i > 0 {
			if got := matchSpanRoute(append([]string{""}, segs[i:]...)); got != spanPathPlaceholder {
				return got
			}
		} else if got := matchSpanRoute(segs); got != spanPathPlaceholder {
			return got
		}
	}
	return spanPathPlaceholder
}

// spanSegments splits p on "/" and decodes each segment, lowercased. It
// fails for a path that is too long, is not escaped correctly or has a dot
// segment.
func spanSegments(p string) ([]string, bool) {
	if len(p) > maxSpanPath {
		return nil, false
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		d, err := url.PathUnescape(s)
		if err != nil || d == "." || d == ".." {
			return nil, false
		}
		segs[i] = strings.ToLower(d)
	}
	return segs, true
}

// matchSpanRoute returns the template whose segments match segs, the first of
// which is the empty one before the leading slash. A {parameter} needs a
// non-empty segment.
func matchSpanRoute(segs []string) string {
	if len(segs) == 0 || segs[0] != "" {
		return spanPathPlaceholder
	}
next:
	for _, r := range loadSpanRoutes() {
		if len(r.segments) != len(segs) {
			continue
		}
		for i := 1; i < len(segs); i++ {
			if want := r.segments[i]; (want == "" && segs[i] == "") || (want != "" && want != segs[i]) {
				continue next
			}
		}
		return r.template
	}
	return spanPathPlaceholder
}
