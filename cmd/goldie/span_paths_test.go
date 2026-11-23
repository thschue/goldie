package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Acceptance tests for #327 at the process boundary: a real goldie exports the
// contract's route templates as the path of a span, never an id, a wrong-case
// path, a dot-segment id or an unknown path, as goldie server, as goldie
// server -dev, and as the client commands. All three must pass the path
// function internal/app provides to platform. They use the binary, the OTLP
// collector of client_tracing_test.go and the helpers of server_test.go and
// server_dev_test.go.

// spanPathTargets are requested with the API token. The first is an article
// lookup; the others carry an id, a query, a wrong case, a dot segment or an
// unknown path, each with a marker that must not reach the collector.
var spanPathTargets = []string{
	"/api/v1/articles/id-marker-7731",
	"/API/V1/articles/id-marker-7731",
	"/api/v1/articles/id-marker-7731?q=query-marker-5512",
	"/api/v1/articles/..%2Fid-marker-7731",
	"/api/v1/articles/%2e%2e%2fid-marker-7731",
	"/api/v1/articles/%2e%2e/id-marker-7731",
	"/api/v1/articles/a/../id-marker-7731",
	"/api/v1/articles/../id-marker-7731",
	"/zz-unknown-marker-9904/segment-marker-1187",
	"/api/v1/zz-unknown-marker-9904",
	"/zz-unknown-marker-9904/%00%1b%0d%0a",
	"/api/v1/articles/" + strings.Repeat("L", 5000),
}

// spanPathLeaks must appear in no export.
var spanPathLeaks = []string{
	"id-marker-7731", "query-marker-5512", "zz-unknown-marker-9904", "segment-marker-1187",
	"/API/V1", "/api/v1/articles/id", "LLLLLLLL", "/../", "%2e%2e", "%2E%2E", "..%2F", "..%2f",
}

func assertTemplatesOnly(t *testing.T, body []byte) {
	t.Helper()
	if !bytes.Contains(body, []byte("url.path")) {
		t.Fatalf("no span with url.path reached the collector (%d bytes)", len(body))
	}
	if !bytes.Contains(body, []byte("/api/v1/articles/{id}")) {
		t.Errorf("the exported spans do not carry the template /api/v1/articles/{id}")
	}
	for _, bad := range spanPathLeaks {
		if bytes.Contains(body, []byte(bad)) {
			t.Errorf("the exported spans carry %q", bad)
		}
	}
}

func traceBody(c *otlpCollector) []byte {
	_, bodies := c.snapshot()
	return bodies["/v1/traces"]
}

func TestServerDevExportsContractTemplatesNotPaths(t *testing.T) {
	c := newOTLPCollector(t, http.StatusOK)
	p := startDev(t, devEnv("OTEL_EXPORTER_OTLP_ENDPOINT="+c.srv.URL, "OTEL_LOGS_EXPORTER=none", "OTEL_METRICS_EXPORTER=none"), contentDir(t))
	for _, path := range spanPathTargets {
		devStatus(t, p.addr, path, p.token)
	}
	p.stop(t)
	assertTemplatesOnly(t, traceBody(c))
}

func TestServerExportsContractTemplatesNotPaths(t *testing.T) {
	c := newOTLPCollector(t, http.StatusOK)
	p := startServer(t, "OTEL_EXPORTER_OTLP_ENDPOINT="+c.srv.URL, "OTEL_LOGS_EXPORTER=none", "OTEL_METRICS_EXPORTER=none",
		"GOLDIE_DRAIN_SECONDS=0")
	for _, path := range spanPathTargets {
		devStatus(t, "http://"+p.app, path, testToken)
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if err := p.waitExit(t, 10*time.Second); err != nil {
		t.Errorf("goldie server exit: %v", err)
	}
	assertTemplatesOnly(t, traceBody(c))
}

// A request the server rejects (no token) is traced with the same templates:
// the span path does not depend on the outcome.
func TestServerRejectedRequestsExportNoPathEither(t *testing.T) {
	c := newOTLPCollector(t, http.StatusOK)
	p := startServer(t, "OTEL_EXPORTER_OTLP_ENDPOINT="+c.srv.URL, "OTEL_LOGS_EXPORTER=none", "OTEL_METRICS_EXPORTER=none",
		"GOLDIE_DRAIN_SECONDS=0")
	for _, path := range spanPathTargets {
		devStatus(t, "http://"+p.app, path, "")
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	_ = p.waitExit(t, 10*time.Second)
	body := traceBody(c)
	if !bytes.Contains(body, []byte("url.path")) {
		t.Fatalf("no span with url.path reached the collector (%d bytes)", len(body))
	}
	for _, bad := range spanPathLeaks {
		if bytes.Contains(body, []byte(bad)) {
			t.Errorf("the exported spans carry %q", bad)
		}
	}
}

// templateInExport reports whether the export has the article template, which
// url.full carries percent-encoded.
func templateInExport(body []byte) bool {
	return bytes.Contains(body, []byte("/api/v1/articles/{id}")) || bytes.Contains(body, []byte("/api/v1/articles/%7Bid%7D"))
}

// The client commands pass the same function: a lookup's client span carries
// the contract's template and no id.
func TestCLIClientSpanCarriesTheContractTemplate(t *testing.T) {
	api := newHeaderAPI(t)
	c := newOTLPCollector(t, http.StatusOK)
	out, stderr, _, err := runClientBinary(t, []string{"OTEL_EXPORTER_OTLP_ENDPOINT=" + c.srv.URL}, lookupArgs(api.srv.URL)...)
	if err != nil || !strings.Contains(out, "Deploying to Kubernetes") {
		t.Fatalf("lookup: err %v, stdout:\n%s\nstderr:\n%s", err, out, stderr)
	}
	body := traceBody(c)
	if !templateInExport(body) {
		t.Errorf("the client span carries no route template (/api/v1/articles/{id}, as is or encoded)")
	}
	for _, bad := range []string{"platform%2Fdeploy", "platform/deploy", "%2Fdeploy"} {
		if bytes.Contains(body, []byte(bad)) {
			t.Errorf("the client span carries the id as %q", bad)
		}
	}
}

// Dot-segment ids never reach a client span (#342), whether the command sends
// the request or refuses the id: either way the collector sees no id.
func TestCLIClientSpanCarriesNoDotSegmentID(t *testing.T) {
	api := newHeaderAPI(t)
	for _, id := range []string{
		"../id-marker-7731", "..%2Fid-marker-7731", "a/../id-marker-7731", "%2e%2e/id-marker-7731",
		"%2e%2e%2fid-marker-7731", "./id-marker-7731", "..", "%2e%2e",
	} {
		t.Run(id, func(t *testing.T) {
			c := newOTLPCollector(t, http.StatusOK)
			args := []string{"lookup", id, "--addr", api.srv.URL, "--token", cliToken}
			_, _, _, _ = runClientBinary(t, []string{"OTEL_EXPORTER_OTLP_ENDPOINT=" + c.srv.URL}, args...)
			body := traceBody(c)
			for _, bad := range []string{"id-marker-7731", "/../", "%2e%2e", "%2E%2E", "..%2F", "..%2f"} {
				if bytes.Contains(body, []byte(bad)) {
					t.Errorf("the client span carries %q", bad)
				}
			}
		})
	}
}

// The path function is applied after the base URL's prefix: behind a
// reverse proxy that serves goldie under /goldie-prefix-marker, the client
// span still carries the route template, never the id, and the prefix is not
// mistaken for part of an unknown route.
func TestCLIClientSpanTemplatesTheRouteBehindABaseURLPrefix(t *testing.T) {
	api := newHeaderAPI(t)
	const prefix = "/goldie-prefix-marker"
	proxy := httptest.NewServer(http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := url.Parse(api.srv.URL)
		r.URL.Scheme, r.URL.Host, r.RequestURI = u.Scheme, u.Host, ""
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		_, _ = w.Write(buf.Bytes())
	})))
	t.Cleanup(proxy.Close)
	c := newOTLPCollector(t, http.StatusOK)
	out, stderr, _, err := runClientBinary(t, []string{"OTEL_EXPORTER_OTLP_ENDPOINT=" + c.srv.URL}, lookupArgs(proxy.URL+prefix)...)
	if err != nil || !strings.Contains(out, "Deploying to Kubernetes") {
		t.Fatalf("lookup behind a prefix: err %v, stdout:\n%s\nstderr:\n%s", err, out, stderr)
	}
	body := traceBody(c)
	if !templateInExport(body) {
		t.Errorf("behind a base URL prefix the client span carries no route template")
	}
	for _, bad := range []string{"platform%2Fdeploy", "platform/deploy", "%2Fdeploy", cliToken} {
		if bytes.Contains(body, []byte(bad)) {
			t.Errorf("the client span carries %q", bad)
		}
	}
}
