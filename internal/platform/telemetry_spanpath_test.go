package platform

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// #327: the url.path of a span is whatever the injected path function returns
// for the request path. Platform names no API route and exports no request
// path of its own: without a path function every span carries one fixed
// placeholder, so nothing raw is exported. The function is injected as an
// option of newTelemetry and, from outside the package, of SetupTelemetry and
// SetupClientTelemetry (the way internal/app and cmd/goldie pass it), so
// existing callers of those keep compiling.
//
// The names the implementation must provide, as seen from here:
//
//	func WithSpanPath(fn func(path string) string) <the option type newTelemetry takes>
//	func SetupTelemetry(ctx, lookup, opts ...<that option type>) (*Telemetry, error)
//	func SetupClientTelemetry(ctx, lookup, role string, opts ...<that option type>) (*Telemetry, error)
//
// fn receives the request's escaped path, exactly as sent: no query string,
// and no path.Clean, so a dot segment reaches it intact and it can find the
// route in the decoded path. A nil fn means "no function".

// pathPlaceholder is what the stub below returns for a path it does not know.
const pathPlaceholder = "/{unknown}"

const articlesRoute = "/api/v1/articles/"

// stubSpanPath is what a contract-driven function is: the template for a
// known route whatever its case, a placeholder for anything else, and no part
// of the input in either.
func stubSpanPath(p string) string {
	if len(p) > len(articlesRoute) && strings.EqualFold(p[:len(articlesRoute)], articlesRoute) &&
		!strings.Contains(p[len(articlesRoute):], "/") {
		return "/api/v1/articles/{id}"
	}
	return pathPlaceholder
}

// articlePrefixStub templates every path under the article route, nested ones
// too: for the older tests that make article requests of several shapes.
func articlePrefixStub(p string) string {
	if strings.HasPrefix(p, articlesRoute) {
		return "/api/v1/articles/{id}"
	}
	return pathPlaceholder
}

// pathRequest is a GET whose URL has exactly the escaped path rawPath and the
// query rawQuery, as a client or server would see it. It is built from the
// URL fields, so nothing is cleaned or redirected on the way.
func pathRequest(t *testing.T, base, rawPath, rawQuery string) *http.Request {
	t.Helper()
	u, err := url.Parse(base + rawPath)
	if err != nil {
		t.Fatalf("%q: %v", rawPath, err)
	}
	u.RawQuery = rawQuery
	req := httptest.NewRequest(http.MethodGet, "http://goldie.test/", nil)
	req.URL = u
	return req
}

// serverSpanText runs req through a handler wrapped by a telemetry that has
// fn (with the option only when withOption), and returns the exported span's
// url.path and all its text.
func serverSpanText(t *testing.T, fn func(string) string, withOption bool, req *http.Request) (urlPath string, text []string) {
	t.Helper()
	spans := newSpanSink()
	var opts []telemetryOption
	if withOption {
		opts = append(opts, WithSpanPath(fn))
	}
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), spans, nil, opts...)
	h := tel.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	h.ServeHTTP(httptest.NewRecorder(), req)
	shutdownWithin(t, tel, 5*time.Second)
	got := spans.spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	for _, kv := range got[0].Attributes {
		if string(kv.Key) == "url.path" {
			urlPath = kv.Value.Emit()
		}
	}
	if urlPath == "" {
		t.Fatalf("the span has no url.path: %v", got[0].Attributes)
	}
	return urlPath, spanText(got[0])
}

func assertNoneOf(t *testing.T, label string, text []string, bad ...string) {
	t.Helper()
	for _, s := range text {
		for _, b := range bad {
			if strings.Contains(s, b) {
				t.Errorf("%s: the span carries %q in %.200q", label, b, s)
			}
		}
	}
}

// A known route is exported as its template, whatever its case; an unknown
// path as the placeholder. No id, query or raw path survives, on the server.
func TestServerSpanURLPathIsWhatThePathFunctionReturns(t *testing.T) {
	for _, tc := range []struct {
		name, path, query, want string
		secrets                 []string
	}{
		{"known route", "/api/v1/articles/id-marker-1", "", "/api/v1/articles/{id}", []string{"id-marker-1"}},
		{"wrong case", "/API/V1/articles/id-marker-1", "", "/api/v1/articles/{id}", []string{"id-marker-1", "/API/V1"}},
		{"unknown path", "/foo/bar", "", pathPlaceholder, []string{"foo", "bar"}},
		{"unknown path with an id", "/internal/users/42/tokens", "", pathPlaceholder, []string{"internal", "users", "42", "tokens"}},
		{"encoded slash in the id", "/api/v1/articles/team%2Fid-marker-1", "", "/api/v1/articles/{id}", []string{"team", "id-marker-1"}},
		{"query string", "/api/v1/articles/id-marker-1", "q=hunter2&token=s3cret", "/api/v1/articles/{id}", []string{"id-marker-1", "hunter2", "s3cret", "token"}},
		{"query on an unknown path", "/foo/bar", "q=hunter2", pathPlaceholder, []string{"foo", "hunter2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, text := serverSpanText(t, stubSpanPath, true, pathRequest(t, "http://goldie.test", tc.path, tc.query))
			if got != tc.want {
				t.Errorf("url.path = %q, want %q", got, tc.want)
			}
			assertNoneOf(t, tc.name, text, tc.secrets...)
		})
	}
}

// The function sees the escaped path without the query and without any
// cleaning, so a dot segment, an encoded dot segment, a doubled slash and an
// encoded slash all arrive as sent. This is what lets it find the route in the
// decoded path before anything is cleaned (#342). Neither the id nor any
// segment of such a path reaches the span.
func TestPathFunctionReceivesTheEscapedPathUncleanedAndWithoutTheQuery(t *testing.T) {
	for _, p := range []string{
		"/api/v1/articles/team%2Fid-marker-1",
		"/api/v1/articles/../id-marker-1",
		"/api/v1/articles/..%2Fid-marker-1",
		"/api/v1/articles/a/../id-marker-1",
		"/api/v1/articles/%2e%2e/id-marker-1",
		"/api/v1/articles/%2e%2e%2fid-marker-1",
		"/api/v1/articles/./id-marker-1",
		"/id-marker-1/../api/v1/articles/x",
		"//api/v1/articles/id-marker-1",
		"/api/v1/articles//id-marker-1",
	} {
		t.Run(p, func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			fn := func(in string) string {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, in)
				return stubSpanPath(in)
			}
			got, text := serverSpanText(t, fn, true, pathRequest(t, "http://goldie.test", p, "q=hunter2"))
			mu.Lock()
			defer mu.Unlock()
			if len(seen) == 0 {
				t.Fatal("the path function was never called")
			}
			for _, in := range seen {
				if in != p {
					t.Errorf("the path function got %q, want the escaped path as sent %q", in, p)
				}
			}
			if got != pathPlaceholder && got != "/api/v1/articles/{id}" {
				t.Errorf("url.path = %q: neither a template nor the placeholder", got)
			}
			assertNoneOf(t, p, text, "id-marker-1", "hunter2")
		})
	}
}

// Hostile paths reach no attribute raw, and the span keeps its url.path.
func TestServerSpanCarriesNothingRawFromAHostilePath(t *testing.T) {
	long := strings.Repeat("A", 8000)
	for name, req := range map[string]*http.Request{
		"control characters": func() *http.Request {
			r := pathRequest(t, "http://goldie.test", "/", "")
			r.URL.Path, r.URL.RawPath = "/foo/\x00\x1b[31mred\r\ninjected: 1\x7f/‮evil", ""
			return r
		}(),
		"long unknown segment":  pathRequest(t, "http://goldie.test", "/"+long, ""),
		"long segment as an id": pathRequest(t, "http://goldie.test", articlesRoute+long, ""),
		"percent escapes":       pathRequest(t, "http://goldie.test", "/foo/%00%1b%0d%0a/%e2%80%ae", ""),
	} {
		t.Run(name, func(t *testing.T) {
			got, text := serverSpanText(t, stubSpanPath, true, req)
			if got != pathPlaceholder && got != "/api/v1/articles/{id}" {
				t.Errorf("url.path = %q: neither a template nor the placeholder", got)
			}
			assertNoneOf(t, name, text, "\x00", "\x1b", "\r", "\n", "\x7f", "‮", "evil", "injected", "AAAA", "foo", "%00", "%1b")
		})
	}
}

// Fail closed: with no path function (never set, or set to nil) no span
// carries any part of a request path, and every one carries the same value.
func TestServerWithoutAPathFunctionExportsNoRawPath(t *testing.T) {
	var first string
	for name, withOption := range map[string]bool{"no option": false, "nil function": true} {
		for _, p := range []string{"/api/v1/articles/id-marker-1", "/API/V1/articles/id-marker-1", "/foo/bar", "/"} {
			got, text := serverSpanText(t, nil, withOption, pathRequest(t, "http://goldie.test", p, "q=hunter2"))
			if first == "" {
				first = got
			}
			if got != first {
				t.Errorf("%s: url.path = %q for %s, want one fixed value %q", name, got, p, first)
			}
			assertNoneOf(t, name+" "+p, text, "id-marker-1", "foo", "bar", "hunter2", "articles", "api/v1", "API")
		}
	}
	if first == "" {
		t.Error("url.path is empty: want a fixed placeholder")
	}
	if first == "/" {
		t.Error("the placeholder is the root path, which is a real route")
	}
}

// The path function changes what is exported, not what is served: the handler
// behind the span still gets the real path and query.
func TestPathFunctionDoesNotChangeTheRequestTheHandlerSees(t *testing.T) {
	spans := newSpanSink()
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), spans, nil, WithSpanPath(stubSpanPath))
	var path, query string
	tel.WrapHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		path, query = r.URL.EscapedPath(), r.URL.RawQuery
	})).ServeHTTP(httptest.NewRecorder(), pathRequest(t, "http://goldie.test", "/api/v1/articles/..%2Fid-marker-1", "q=1"))
	if path != "/api/v1/articles/..%2Fid-marker-1" || query != "q=1" {
		t.Errorf("handler saw %q?%q, want the original request", path, query)
	}
}

// clientSpanText sends a request for rawPath through WrapTransport of a
// telemetry with fn (with the option only when withOption) and returns all
// text of the one client span.
func clientSpanText(t *testing.T, fn func(string) string, withOption bool, rawPath, rawQuery string) []string {
	t.Helper()
	spans := newSpanSink()
	var opts []telemetryOption
	if withOption {
		opts = append(opts, WithSpanPath(fn))
	}
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), spans, nil, opts...)
	api := newAPIStub(t, nil)
	req := pathRequest(t, api.srv.URL, rawPath, rawQuery)
	req.Header.Set("Authorization", "Bearer "+plantedToken)
	doAll(t, tel.WrapTransport(http.DefaultTransport), []*http.Request{req})
	shutdownWithin(t, tel, 5*time.Second)
	got := spans.spans()
	if len(got) != 1 {
		t.Fatalf("got %d client spans, want 1", len(got))
	}
	// The request itself went out whole.
	seen := api.requests()
	if len(seen) != 1 || seen[0].escapedPath != rawPath || seen[0].rawQuery != rawQuery {
		t.Errorf("the API saw %+v, want the request as sent (%s?%s)", seen, rawPath, rawQuery)
	}
	return spanText(got[0])
}

// A client span carries the function's template for a known route, and
// neither the id, the wrong-case spelling, the unknown path nor the query.
func TestClientSpanPathIsWhatThePathFunctionReturns(t *testing.T) {
	for _, tc := range []struct {
		name, path, want string
		secrets          []string
	}{
		{"known route", "/api/v1/articles/id-marker-1", "/api/v1/articles/{id}", []string{"id-marker-1"}},
		{"wrong case", "/API/V1/articles/id-marker-1", "/api/v1/articles/{id}", []string{"id-marker-1", "/API/V1"}},
		{"encoded slash in the id", "/api/v1/articles/team%2Fid-marker-1", "/api/v1/articles/{id}", []string{"team", "id-marker-1"}},
		{"unknown path", "/foo/bar", pathPlaceholder, []string{"foo", "bar"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := clientSpanText(t, stubSpanPath, true, tc.path, "q=hunter2")
			all := strings.Join(text, "\x01")
			// url.full has the path percent-encoded.
			encoded := strings.NewReplacer("{", "%7B", "}", "%7D").Replace(tc.want)
			if !strings.Contains(all, tc.want) && !strings.Contains(all, encoded) {
				t.Errorf("the span carries neither %q nor %q: %.300q", tc.want, encoded, all)
			}
			assertNoneOf(t, tc.name, text, append(tc.secrets, "hunter2", "Bearer", plantedToken)...)
		})
	}
}

// Dot-segment ids never reach a client span (#342): the function gets the path
// as sent, and nothing but its answer is exported.
func TestClientSpanCarriesNoDotSegmentID(t *testing.T) {
	for _, p := range []string{
		"/api/v1/articles/../id-marker-1",
		"/api/v1/articles/..%2Fid-marker-1",
		"/api/v1/articles/a/../id-marker-1",
		"/api/v1/articles/%2e%2e/id-marker-1",
		"/api/v1/articles/%2e%2e%2fid-marker-1",
		"/api/v1/articles/./id-marker-1",
		"/id-marker-1/../api/v1/articles/x",
	} {
		t.Run(p, func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			fn := func(in string) string {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, in)
				return stubSpanPath(in)
			}
			text := clientSpanText(t, fn, true, p, "")
			assertNoneOf(t, p, text, "id-marker-1")
			mu.Lock()
			defer mu.Unlock()
			if len(seen) == 0 {
				t.Fatal("the path function was never called")
			}
			for _, in := range seen {
				if in != p {
					t.Errorf("the path function got %q, want the escaped path as sent %q", in, p)
				}
			}
		})
	}
}

// Fail closed on the client too: no function, no request path in the span
// (the host stays, as it does today).
func TestClientWithoutAPathFunctionExportsNoRawPath(t *testing.T) {
	for name, withOption := range map[string]bool{"no option": false, "nil function": true} {
		for _, p := range []string{"/api/v1/articles/id-marker-1", "/API/V1/articles/id-marker-1", "/foo/bar"} {
			text := clientSpanText(t, nil, withOption, p, "q=hunter2")
			assertNoneOf(t, name+" "+p, text, "id-marker-1", "foo", "bar", "hunter2", "articles", "api/v1", "API", "Bearer", plantedToken)
		}
	}
}

// Platform names no API route: its telemetry sources hold none, so the one
// place a route is known is the contract, through internal/app.
func TestPlatformTelemetrySourcesNameNoAPIRoute(t *testing.T) {
	for _, name := range []string{"telemetry_client.go", "telemetry_clientinfo.go", "telemetry.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"/api/v1", "api/v1/articles", "articleRoute", "{id}"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s names an API route (%q): the path function comes from internal/app", name, bad)
			}
		}
	}
}
