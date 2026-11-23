package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/platform"
)

// #327: internal/app gives platform the function that turns a request path
// into what a span may carry, built from the contract's route templates, so
// platform names no API route.
//
// The names the implementation must provide, as seen from here:
//
//	func SpanPath(path string) string                (package app)
//	func (t *platform.Telemetry) SetSpanPath(func(string) string)
//
// SpanPath takes the request's escaped path (no query string), returns the
// spec's template for any known route whatever the case of the request, and
// one fixed placeholder, which is no template, for every other input.
// Local.Start applies it to Local.Telemetry, as goldie server does for the
// Telemetry it serves with; a real server run is tested in cmd/goldie.

var spanPathMethods = []string{"get", "put", "post", "delete", "patch", "head", "options"}

// specRoutes returns, for each path template of the contract, a request path
// that matches it with the examples the spec declares.
func specRoutes(t *testing.T) map[string][]string {
	t.Helper()
	doc, err := contracttest.Spec()
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatal("the spec has no paths")
	}
	routes := map[string][]string{}
	for tmpl, item := range paths {
		if !strings.Contains(tmpl, "{") {
			routes[tmpl] = []string{tmpl}
			continue
		}
		ops, _ := item.(map[string]any)
		for _, m := range spanPathMethods {
			if _, ok := ops[m]; !ok {
				continue
			}
			p, err := contracttest.ExamplePath(m, tmpl)
			if err != nil {
				t.Fatalf("ExamplePath(%s, %s): %v", m, tmpl, err)
			}
			routes[tmpl] = append(routes[tmpl], p)
		}
	}
	return routes
}

func TestSpanPathReturnsTheTemplateOfEveryContractRouteIgnoringCase(t *testing.T) {
	for tmpl, examples := range specRoutes(t) {
		for _, p := range examples {
			for name, in := range map[string]string{"as sent": p, "upper case": strings.ToUpper(p), "lower case": strings.ToLower(p)} {
				if got := SpanPath(in); got != tmpl {
					t.Errorf("SpanPath(%q) (%s) = %q, want the template %q", in, name, got, tmpl)
				}
			}
		}
	}
}

func TestSpanPathAnArticleIdWithAnEncodedSlashIsOneSegment(t *testing.T) {
	if got := SpanPath("/api/v1/articles/platform%2Fdeploy"); got != "/api/v1/articles/{id}" {
		t.Errorf("SpanPath = %q, want /api/v1/articles/{id}", got)
	}
}

// Every other input gets one placeholder: not empty, not a template, and none
// of the input in it.
func TestSpanPathGivesOneFixedPlaceholderToAnythingElse(t *testing.T) {
	templates := specRoutes(t)
	placeholder := SpanPath("/foo/bar")
	if placeholder == "" {
		t.Fatal("the placeholder is empty")
	}
	if _, isRoute := templates[placeholder]; isRoute {
		t.Fatalf("the placeholder %q is a route template", placeholder)
	}
	if strings.Contains(placeholder, "foo") || strings.Contains(placeholder, "bar") {
		t.Errorf("the placeholder %q carries the input", placeholder)
	}
	for _, in := range []string{
		"", "/", "foo", "/foo/bar/baz", "/api", "/api/", "/api/v1", "/api/v1/", "/api/v1/unknown/42",
		"/api/v1/articles", "/api/v1/articles/", "/api/v1/articles/a/b", "/api/v1/articles/a/b/c",
		"/api/v1/articles/x/", "/api/v1/articles//x",
		"/api/v1/search/extra", "/mcp/x", "/../etc/passwd", "/api/v1/articles/../search",
		"/api/v1/articles/x?q=hunter2", "/foo/bar?token=s3cret", "/foo#frag",
		"/foo/\x00\x1b[31m\r\nx: y\x7f/‮", "/foo/%00%1b%0d%0a", "/" + strings.Repeat("A", 100000),
	} {
		got := SpanPath(in)
		if got == placeholder {
			continue
		}
		if _, isRoute := templates[got]; !isRoute {
			t.Errorf("SpanPath(%.60q) = %.80q: neither the placeholder nor a template", in, got)
		}
		for _, leak := range []string{"hunter2", "s3cret", "foo", "frag"} {
			if strings.Contains(got, leak) {
				t.Errorf("SpanPath(%.60q) = %q carries %q", in, got, leak)
			}
		}
	}
	// No input, however long or hostile, grows the result.
	if got := SpanPath("/" + strings.Repeat("A", 100000)); got != placeholder {
		t.Errorf("a 100000-byte path gave %.80q, want the placeholder", got)
	}
}

// The same placeholder and templates, a thousand different ids: the set of
// values a span can carry stays bounded by the contract.
func TestSpanPathValuesAreBoundedByTheContract(t *testing.T) {
	templates := specRoutes(t)
	seen := map[string]bool{}
	for i := range 1000 {
		id := strings.Repeat("x", i%7+1) + string(rune('a'+i%26))
		for _, in := range []string{"/api/v1/articles/" + id, "/API/V1/ARTICLES/" + id, "/u/" + id, "/api/v1/" + id} {
			seen[SpanPath(in)] = true
		}
	}
	if len(seen) > len(templates)+1 {
		t.Errorf("%d distinct values for ids over %d templates plus a placeholder: %v", len(seen), len(templates), seen)
	}
}

// otlpCollector records the bodies of what an OTLP collector received, by path.
type otlpCollector struct {
	mu     sync.Mutex
	bodies map[string][]byte
}

func newOTLPCollector(t *testing.T) (*httptest.Server, *otlpCollector) {
	t.Helper()
	c := &otlpCollector{bodies: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body = zr
		}
		b, _ := io.ReadAll(body)
		c.mu.Lock()
		c.bodies[r.URL.Path] = append(c.bodies[r.URL.Path], b...)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func (c *otlpCollector) traces() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.bodies["/v1/traces"])
}

// Through a real local server and the OTLP exporter: the span of an article
// lookup carries the contract's template, and neither the id nor the case of
// the request, nor an unknown path, nor a query string, reaches the collector.
func TestLocalServerExportsContractTemplatesNotPaths(t *testing.T) {
	collector, got := newOTLPCollector(t)
	tel, err := platform.SetupTelemetry(context.Background(), func(k string) (string, bool) {
		v, ok := map[string]string{
			"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL,
			"OTEL_LOGS_EXPORTER":          "none",
			"OTEL_METRICS_EXPORTER":       "none",
		}[k]
		return v, ok
	})
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := loadedConfig(t, map[string]string{platform.EnvContentDir: testContent, platform.EnvDrainSeconds: "0"})
	addr, token, wait, err := Local{Config: cfg, Telemetry: tel}.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/api/v1/articles/id-marker-7731",
		"/API/V1/articles/id-marker-7731",
		"/api/v1/articles/id-marker-7731?q=query-marker-5512",
		"/zz-unknown-marker-9904/segment-marker-1187",
		"/api/v1/zz-unknown-marker-9904",
		"/zz-unknown-marker-9904/%00%1b%0d%0a",
		"/api/v1/articles/" + strings.Repeat("L", 5000),
	} {
		rawDo(t, http.MethodGet, addr+p, bearer(token))
	}
	cancel()
	done := make(chan error, 1)
	go func() { done <- wait() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the local server did not stop")
	}
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	body := got.traces()
	if !bytes.Contains(body, []byte("url.path")) {
		t.Fatalf("no span with url.path reached the collector (%d bytes)", len(body))
	}
	if !bytes.Contains(body, []byte("/api/v1/articles/{id}")) {
		t.Errorf("the exported spans do not carry the template /api/v1/articles/{id}")
	}
	for _, bad := range []string{
		"id-marker-7731", "query-marker-5512", "zz-unknown-marker-9904", "segment-marker-1187",
		"/API/V1", "/api/v1/articles/id", "LLLLLLLL", "\x1b",
	} {
		if bytes.Contains(body, []byte(bad)) {
			t.Errorf("the exported spans carry %q", bad)
		}
	}
}
