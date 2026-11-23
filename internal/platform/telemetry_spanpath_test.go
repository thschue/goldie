package platform

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// #327: the url.path of a server span is whatever the injected path function
// (SetSpanPath, supplied by internal/app from the contract's route templates)
// returns for the request path. Platform names no API route and exports no
// request path of its own: without a path function every span carries the
// same fixed placeholder, so nothing raw is exported.
//
// The names the implementation must provide, as seen from here:
//
//	func (t *Telemetry) SetSpanPath(fn func(path string) string)
//
// SetSpanPath is safe on a nil *Telemetry and on a disabled one, replaces any
// earlier function, and takes effect for handlers WrapHandler returned before
// or after the call. fn receives the request's escaped path (the URL's
// EscapedPath, so "%2F" in an article id stays one segment), never the query
// string. A nil fn means "no function": every span carries the placeholder.

// routePlaceholder stands for any path the stub below does not know.
const routePlaceholder = "/{unknown}"

// stubSpanPath is the path function a contract-driven injector would be: the
// template for a known route (ignoring case), a placeholder for the rest.
func stubSpanPath(path string) string {
	if strings.HasPrefix(strings.ToLower(path), "/api/v1/articles/") && len(path) > len("/api/v1/articles/") &&
		!strings.Contains(path[len("/api/v1/articles/"):], "/") {
		return "/api/v1/articles/{id}"
	}
	return routePlaceholder
}

// spanPathAttr runs one request for target through a handler wrapped by tel
// and returns the exported span: its url.path, and every attribute value and
// the span name joined, to search for leaks.
func spanPathAttr(t *testing.T, tel *Telemetry, spans *spanSink, req *http.Request) (urlPath, everything string) {
	t.Helper()
	h := tel.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if err := tel.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := spans.spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	var all []string
	all = append(all, got[0].Name)
	found := false
	for _, kv := range got[0].Attributes {
		all = append(all, string(kv.Key), kv.Value.Emit())
		if string(kv.Key) == "url.path" {
			urlPath, found = kv.Value.Emit(), true
		}
	}
	if !found {
		t.Fatalf("the span has no url.path attribute: %v", got[0].Attributes)
	}
	return urlPath, strings.Join(all, "\x01")
}

func newSpanPathTelemetry(spans *spanSink, fn func(string) string) *Telemetry {
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), spans, &logSink{})
	tel.SetSpanPath(fn)
	return tel
}

func requestFor(rawPath string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://goldie.test/", nil)
	u, err := url.Parse("http://goldie.test" + rawPath)
	if err != nil {
		panic(err)
	}
	req.URL = u
	return req
}

// A known route is exported as its template, whatever its case; an unknown
// path is exported as the placeholder. Neither id nor raw path survives.
func TestSpanURLPathIsWhatThePathFunctionReturns(t *testing.T) {
	for _, tc := range []struct {
		name, path, want string
		secrets          []string
	}{
		{"known route", "/api/v1/articles/secret-id", "/api/v1/articles/{id}", []string{"secret-id"}},
		{"wrong case", "/API/V1/articles/secret-id", "/api/v1/articles/{id}", []string{"secret-id", "/API/V1"}},
		{"mixed case", "/Api/v1/ARTICLES/secret-id", "/api/v1/articles/{id}", []string{"secret-id", "ARTICLES"}},
		{"unknown path", "/foo/bar", routePlaceholder, []string{"/foo/bar", "foo", "bar"}},
		{"unknown path with an id", "/internal/users/42/tokens", routePlaceholder, []string{"users", "42", "tokens", "internal"}},
		{"encoded slash in the id", "/api/v1/articles/platform%2Fdeploy", "/api/v1/articles/{id}", []string{"platform", "deploy"}},
		{"trailing slash", "/api/v1/articles/secret-id/", routePlaceholder, []string{"secret-id"}},
		{"query string", "/api/v1/articles/secret-id?q=hunter2&token=s3cret", "/api/v1/articles/{id}", []string{"secret-id", "hunter2", "s3cret", "token"}},
		{"query on an unknown path", "/foo/bar?q=hunter2", routePlaceholder, []string{"foo", "hunter2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spans := newSpanSink()
			got, all := spanPathAttr(t, newSpanPathTelemetry(spans, stubSpanPath), spans, requestFor(tc.path))
			if got != tc.want {
				t.Errorf("url.path = %q, want %q", got, tc.want)
			}
			for _, s := range tc.secrets {
				if strings.Contains(all, s) {
					t.Errorf("the span carries %q somewhere: %q", s, all)
				}
			}
		})
	}
}

// The function sees the escaped path without the query, so an id with an
// encoded slash is one segment and a query string is never offered to it.
func TestPathFunctionReceivesTheEscapedPathWithoutTheQuery(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	spans := newSpanSink()
	tel := newSpanPathTelemetry(spans, func(p string) string {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, p)
		return "/x"
	})
	spanPathAttr(t, tel, spans, requestFor("/api/v1/articles/platform%2Fdeploy?q=hunter2"))
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("the path function was never called")
	}
	for _, p := range seen {
		if p != "/api/v1/articles/platform%2Fdeploy" {
			t.Errorf("the path function got %q, want the escaped path without the query", p)
		}
	}
}

// Hostile paths: control characters, an escape sequence, and a very long
// segment reach no attribute raw, and the span still has its url.path.
func TestSpanCarriesNothingRawFromAHostilePath(t *testing.T) {
	long := strings.Repeat("A", 8000)
	for name, req := range map[string]*http.Request{
		"control characters": func() *http.Request {
			r := requestFor("/")
			r.URL.Path = "/foo/\x00\x1b[31mred\r\ninjected: 1\x7f/‮evil"
			r.URL.RawPath = ""
			return r
		}(),
		"long unknown segment":  requestFor("/" + long),
		"long segment as an id": requestFor("/api/v1/articles/" + long),
		"percent escapes":       requestFor("/foo/%00%1b%0d%0a/%e2%80%ae"),
	} {
		t.Run(name, func(t *testing.T) {
			spans := newSpanSink()
			got, all := spanPathAttr(t, newSpanPathTelemetry(spans, stubSpanPath), spans, req)
			if got != routePlaceholder && got != "/api/v1/articles/{id}" {
				t.Errorf("url.path = %q: neither a template nor the placeholder", got)
			}
			for _, bad := range []string{"\x00", "\x1b", "\r", "\n", "\x7f", "‮", "evil", "injected", "AAAA", "foo", "%00", "%1b"} {
				if strings.Contains(all, bad) {
					t.Errorf("the span carries %q: %.200q", bad, all)
				}
			}
		})
	}
}

// Fail closed: with no path function, whether never set or set to nil, no
// span carries a request path; every one carries the same fixed value.
func TestWithoutAPathFunctionNoRawPathIsExported(t *testing.T) {
	var first string
	for i, mk := range []func(*spanSink) *Telemetry{
		func(s *spanSink) *Telemetry {
			return newTelemetry(telemetryResource("", nil), defaultTraceSettings(), s, &logSink{})
		},
		func(s *spanSink) *Telemetry { return newSpanPathTelemetry(s, nil) },
	} {
		for _, path := range []string{"/api/v1/articles/secret-id", "/API/V1/articles/secret-id", "/foo/bar"} {
			spans := newSpanSink()
			got, all := spanPathAttr(t, mk(spans), spans, requestFor(path+"?q=hunter2"))
			if i == 0 && first == "" {
				first = got
			}
			if got != first {
				t.Errorf("url.path = %q for %s, want one fixed value (%q)", got, path, first)
			}
			for _, bad := range []string{"secret-id", "foo", "bar", "hunter2", "articles", "api/v1", "API"} {
				if strings.Contains(all, bad) {
					t.Errorf("no path function, yet the span carries %q: %q", bad, all)
				}
			}
		}
	}
	if first == "" {
		t.Error("url.path is empty: want a fixed placeholder")
	}
}

// The path function can be set on a nil or disabled Telemetry, and the
// wrapped handler still serves.
func TestSetSpanPathOnNilAndDisabledTelemetryIsHarmless(t *testing.T) {
	var nilTel *Telemetry
	nilTel.SetSpanPath(stubSpanPath)
	(&Telemetry{}).SetSpanPath(stubSpanPath)
	for _, tel := range []*Telemetry{nilTel, {}} {
		rec := httptest.NewRecorder()
		tel.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })).
			ServeHTTP(rec, requestFor("/foo"))
		if rec.Code != http.StatusTeapot {
			t.Errorf("status = %d, want the handler's own", rec.Code)
		}
	}
}

// The handler behind the span keeps the real request: the path function
// changes what is exported, not what is served.
func TestPathFunctionDoesNotChangeTheRequestTheHandlerSees(t *testing.T) {
	spans := newSpanSink()
	tel := newSpanPathTelemetry(spans, stubSpanPath)
	var path, query string
	tel.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.EscapedPath(), r.URL.RawQuery
	})).ServeHTTP(httptest.NewRecorder(), requestFor("/api/v1/articles/secret-id?q=1"))
	if path != "/api/v1/articles/secret-id" || query != "q=1" {
		t.Errorf("handler saw %q?%q, want the original request", path, query)
	}
}
