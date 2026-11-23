package platform

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// Acceptance tests for #281: client-side tracing for the CLI and goldie mcp
// over stdio, linked to (not parented by) the server span. The seam they pin,
// which keeps every OpenTelemetry type inside internal/platform:
//
//	func (t *Telemetry) WrapTransport(rt http.RoundTripper) http.RoundTripper
//	func SetupClientTelemetry(ctx context.Context, lookup Lookup, mode M) (*Telemetry, error)
//
// where M is string or a type whose underlying type is string, called with
// "cli" or "mcp". They rely on the helpers newSpanSink, logSink, mapLookup,
// defaultTraceSettings (telemetry_test.go), newCollector
// (telemetry_settings_test.go), syncBuffer and committedReference
// (content_dir_text_test.go).

// Markers planted in the requests a client span describes. None may reach a
// span or an export.
const (
	plantedToken     = "planted-token-9e2f41c0"
	plantedArticle   = "planted-article-7c1d"
	plantedNested    = "planted-nested-article-55aa"
	plantedProject   = "planted-project-q-31b8"
	plantedSearch    = "planted-search-term-0d4e"
	plantedUser      = "planted-user-6f10"
	plantedPassword  = "planted-pass-a2c9"
	articleTemplate  = "/api/v1/articles/{id}"
	baggageMarker    = "planted-baggage-marker"
	clientLogMarker  = "client-log-marker-81d2"
	clientMetricName = "client_metric_marker_81d2"
)

var plantedMarkers = []string{plantedToken, "Bearer", plantedArticle, plantedNested, plantedProject,
	plantedSearch, plantedUser, plantedPassword}

// recordedRequest is what the API stub received, to prove the wrap changes
// only what the span sees, never the request itself.
type recordedRequest struct {
	escapedPath, rawQuery, auth string
	header                      http.Header
}

// apiStub answers 200 to everything and records each request.
type apiStub struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []recordedRequest
}

func newAPIStub(t *testing.T, wrap func(http.Handler) http.Handler) *apiStub {
	t.Helper()
	a := &apiStub{}
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.reqs = append(a.reqs, recordedRequest{
			escapedPath: r.URL.EscapedPath(), rawQuery: r.URL.RawQuery,
			auth: r.Header.Get("Authorization"), header: r.Header.Clone(),
		})
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	if wrap != nil {
		h = wrap(h)
	}
	a.srv = httptest.NewServer(h)
	t.Cleanup(a.srv.Close)
	return a
}

func (a *apiStub) requests() []recordedRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]recordedRequest(nil), a.reqs...)
}

// plantedRequests are the API calls the clients make, each carrying the
// token, and the article ones an id, the others a query.
func plantedRequests(t *testing.T, base string) []*http.Request {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	withUser := *u
	withUser.User = url.UserPassword(plantedUser, plantedPassword)
	raw := []string{
		base + "/api/v1/articles/" + plantedArticle + "?project=" + plantedProject,
		base + "/api/v1/articles/team%2F" + plantedNested,
		base + "/api/v1/articles/team/" + plantedNested + "?project=" + plantedProject,
		base + "/api/v1/search?q=" + plantedSearch + "&project=" + plantedProject,
		withUser.String() + "/api/v1/search?q=" + plantedSearch,
	}
	var out []*http.Request
	for _, s := range raw {
		req, err := http.NewRequest(http.MethodGet, s, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+plantedToken)
		out = append(out, req)
	}
	return out
}

// articleRequests is how many of plantedRequests name an article.
const articleRequests = 3

func doAll(t *testing.T, rt http.RoundTripper, reqs []*http.Request) {
	t.Helper()
	hc := &http.Client{Transport: rt}
	for _, req := range reqs {
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatalf("%s through the wrapped transport: %v", req.Method, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

func shutdownWithin(t *testing.T, tel *Telemetry, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_ = tel.Shutdown(ctx)
}

// Criterion: WrapTransport is nil-safe and hands back the transport unchanged
// when traces are off, as WrapHandler does.
func TestWrapTransportIsNilSafeAndUnchangedWhenTracesAreOff(t *testing.T) {
	rt := &http.Transport{}
	var nilTel *Telemetry
	if got := nilTel.WrapTransport(rt); got != http.RoundTripper(rt) {
		t.Error("a nil Telemetry must hand back the transport unchanged")
	}
	if got := (&Telemetry{}).WrapTransport(rt); got != http.RoundTripper(rt) {
		t.Error("a disabled Telemetry must hand back the transport unchanged")
	}
	logsOnly := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), nil, &logSink{})
	t.Cleanup(func() { _ = logsOnly.Shutdown(context.Background()) })
	if got := logsOnly.WrapTransport(rt); got != http.RoundTripper(rt) {
		t.Error("a Telemetry without traces must hand back the transport unchanged")
	}
	tel, err := SetupClientTelemetry(context.Background(), mapLookup(nil), "cli")
	if err != nil {
		t.Fatalf("SetupClientTelemetry with no OTEL_* variables: %v", err)
	}
	if got := tel.WrapTransport(rt); got != http.RoundTripper(rt) {
		t.Error("client telemetry with no OTEL_* variables must hand back the transport unchanged")
	}
}

// Criterion: around one httptest server wrapped with WrapHandler, the server
// span has a different trace ID and a span link to the client span, and the
// request carries the client span as traceparent. Propagation is TraceContext
// only: baggage in the caller's context is never sent, even when the global
// propagator would send it.
func TestWrapTransportServerSpanIsANewTraceLinkedToTheClientSpan(t *testing.T) {
	clientSpans, serverSpans := newSpanSink(), newSpanSink()
	client := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), clientSpans, nil)
	server := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), serverSpans, nil)
	api := newAPIStub(t, server.WrapHandler)

	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
	member, err := baggage.NewMember("planted", baggageMarker)
	if err != nil {
		t.Fatal(err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(baggage.ContextWithBaggage(context.Background(), bag),
		http.MethodGet, api.srv.URL+"/api/v1/search?q=agents", nil)
	if err != nil {
		t.Fatal(err)
	}
	doAll(t, client.WrapTransport(http.DefaultTransport), []*http.Request{req})
	shutdownWithin(t, client, 5*time.Second)
	shutdownWithin(t, server, 5*time.Second)

	cs, ss := clientSpans.spans(), serverSpans.spans()
	if len(cs) != 1 || len(ss) != 1 {
		t.Fatalf("got %d client and %d server spans, want exactly 1 each", len(cs), len(ss))
	}
	c, s := cs[0], ss[0]
	if c.SpanKind.String() != "client" {
		t.Errorf("client span kind = %v, want client", c.SpanKind)
	}
	if s.SpanKind.String() != "server" {
		t.Errorf("server span kind = %v, want server", s.SpanKind)
	}

	got := api.requests()
	if len(got) != 1 {
		t.Fatalf("the API saw %d requests, want 1", len(got))
	}
	tp := got[0].header.Get("Traceparent")
	want := "00-" + c.SpanContext.TraceID().String() + "-" + c.SpanContext.SpanID().String() + "-"
	if !strings.HasPrefix(tp, want) {
		t.Errorf("traceparent = %q, want the client span (%s..)", tp, want)
	}
	if v, ok := got[0].header["Baggage"]; ok {
		t.Errorf("baggage header sent: %q; propagation must be TraceContext only", v)
	}
	for name, vs := range got[0].header {
		for _, v := range vs {
			if strings.Contains(v, baggageMarker) {
				t.Errorf("header %s carries the caller's baggage", name)
			}
		}
	}

	if s.SpanContext.TraceID() == c.SpanContext.TraceID() {
		t.Error("the server span is in the client's trace; it must start its own and link to the client span")
	}
	if s.Parent.IsValid() {
		t.Errorf("the server span has a parent (%v); the client span must be a link, not a parent", s.Parent.SpanID())
	}
	var linked bool
	for _, l := range s.Links {
		if l.SpanContext.TraceID() == c.SpanContext.TraceID() && l.SpanContext.SpanID() == c.SpanContext.SpanID() {
			linked = true
		}
	}
	if !linked {
		t.Errorf("the server span has no link to the client span (links: %+v)", s.Links)
	}
}

// spanText is every piece of text a span carries: name, status, attribute
// keys and values, and those of its events and links.
func spanText(s tracetest.SpanStub) []string {
	out := []string{s.Name, s.Status.Description}
	for _, kv := range s.Attributes {
		out = append(out, string(kv.Key)+"="+kv.Value.String())
	}
	for _, e := range s.Events {
		out = append(out, e.Name)
		for _, kv := range e.Attributes {
			out = append(out, string(kv.Key)+"="+kv.Value.String())
		}
	}
	for _, l := range s.Links {
		for _, kv := range l.Attributes {
			out = append(out, string(kv.Key)+"="+kv.Value.String())
		}
	}
	return out
}

// Criterion: the client span has no query string, no article id (the route
// template /api/v1/articles/{id} only) and no Authorization. The request
// itself still goes out whole: the wrap changes only what the span sees.
func TestClientSpanCarriesNoQueryArticleIDOrAuthorization(t *testing.T) {
	spans := newSpanSink()
	client := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), spans, nil)
	api := newAPIStub(t, nil)
	reqs := plantedRequests(t, api.srv.URL)
	doAll(t, client.WrapTransport(http.DefaultTransport), reqs)
	shutdownWithin(t, client, 5*time.Second)

	got := spans.spans()
	if len(got) != len(reqs) {
		t.Fatalf("got %d client spans, want %d (one per request)", len(got), len(reqs))
	}
	templated := 0
	for i, s := range got {
		var hasTemplate bool
		for _, text := range spanText(s) {
			for _, bad := range plantedMarkers {
				if strings.Contains(text, bad) {
					t.Errorf("span %d leaks %q in %q", i, bad, text)
				}
			}
			// The template may be percent-encoded in a URL attribute.
			if u, err := url.PathUnescape(text); strings.Contains(text, articleTemplate) || (err == nil && strings.Contains(u, articleTemplate)) {
				hasTemplate = true
			}
		}
		for _, kv := range s.Attributes {
			if k := string(kv.Key); k == "url.query" || strings.HasPrefix(k, "http.request.header.") {
				t.Errorf("span %d records %s", i, k)
			}
		}
		if hasTemplate {
			templated++
		}
	}
	if templated != articleRequests {
		t.Errorf("%d spans carry the route template %s, want %d (one per article request)", templated, articleTemplate, articleRequests)
	}

	// The API still received the id, the query and the token.
	seen := api.requests()
	if len(seen) != len(reqs) {
		t.Fatalf("the API saw %d requests, want %d", len(seen), len(reqs))
	}
	first := seen[0]
	if !strings.Contains(first.escapedPath, plantedArticle) || !strings.Contains(first.rawQuery, plantedProject) ||
		first.auth != "Bearer "+plantedToken {
		t.Errorf("the request was changed on its way: path %q, query %q, Authorization %q", first.escapedPath, first.rawQuery, first.auth)
	}
	if !strings.Contains(seen[1].escapedPath, "team%2F"+plantedNested) {
		t.Errorf("an escaped id was changed on its way: path %q", seen[1].escapedPath)
	}
}

// decodeTraces decodes what a collector received on /v1/traces.
func decodeTraces(t *testing.T, body []byte) *coltracepb.ExportTraceServiceRequest {
	t.Helper()
	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode the trace export: %v", err)
	}
	return &req
}

// Criterion: SetupClientTelemetry sets up traces only, with service.name
// goldie and goldie.role cli or mcp (never goldie.mode). Logs and metrics are never exported,
// even when their exporters are selected, and nothing planted in a request
// reaches the collector.
func TestSetupClientTelemetryExportsTracesOnlyWithTheClientMode(t *testing.T) {
	for _, mode := range []string{"cli", "mcp"} {
		t.Run(mode, func(t *testing.T) {
			collector, seen := newCollector(t)
			tel, err := SetupClientTelemetry(context.Background(), mapLookup(map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL,
				"OTEL_LOGS_EXPORTER":          "otlp",
				"OTEL_METRICS_EXPORTER":       "otlp",
			}), mode)
			if err != nil {
				t.Fatalf("SetupClientTelemetry: %v", err)
			}
			if !tel.Enabled() {
				t.Fatal("an OTLP endpoint must turn client tracing on")
			}
			api := newAPIStub(t, nil)
			doAll(t, tel.WrapTransport(http.DefaultTransport), plantedRequests(t, api.srv.URL)[:1])
			tel.WrapLogger(NewLogger(&syncBuffer{}, 0, mode), mode).Error(clientLogMarker)
			if c, err := tel.Meter("t").Int64Counter(clientMetricName); err == nil {
				c.Add(context.Background(), 1)
			}
			shutdownWithin(t, tel, 5*time.Second)

			seen.mu.Lock()
			defer seen.mu.Unlock()
			for path := range seen.bodies {
				if path != "/v1/traces" {
					t.Errorf("client telemetry exported to %s; client roles export traces only", path)
				}
			}
			body := seen.bodies["/v1/traces"]
			if len(body) == 0 {
				t.Fatalf("no spans reached the collector (paths: %v)", seen.auth)
			}
			for _, bad := range append(plantedMarkers, clientLogMarker, clientMetricName) {
				if strings.Contains(string(body), bad) {
					t.Errorf("the trace export carries %q", bad)
				}
			}
			req := decodeTraces(t, body)
			var spans int
			for _, rs := range req.ResourceSpans {
				res := map[string]string{}
				for _, kv := range rs.GetResource().GetAttributes() {
					res[kv.Key] = kv.Value.GetStringValue()
				}
				want := map[string]string{"service.name": "goldie", "goldie.role": mode, "service.version": CurrentVersion().Version}
				for k, v := range want {
					if res[k] != v {
						t.Errorf("resource %s = %q, want %q", k, res[k], v)
					}
				}
				if v, ok := res["goldie.mode"]; ok {
					t.Errorf("client resource carries goldie.mode=%q; that stays a server-mode attribute", v)
				}
				for _, ss := range rs.ScopeSpans {
					for _, sp := range ss.Spans {
						spans++
						if sp.Kind != tracepb.Span_SPAN_KIND_CLIENT {
							t.Errorf("exported span kind = %v, want client", sp.Kind)
						}
					}
				}
			}
			if spans != 1 {
				t.Errorf("exported %d spans, want 1 (one request)", spans)
			}
		})
	}
}

// Criterion: client telemetry is off unless an endpoint or exporter selects
// traces, and OTEL_SDK_DISABLED=true turns it off whatever else is set.
// Logs or metrics alone do not turn a client role on.
func TestSetupClientTelemetryIsOffUnlessTracesAreSelected(t *testing.T) {
	collector, seen := newCollector(t)
	cases := map[string]map[string]string{
		"nothing set": {},
		"sdk disabled": {
			"OTEL_SDK_DISABLED": "true", "OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL, "OTEL_TRACES_EXPORTER": "otlp",
		},
		"sdk disabled, upper case": {"OTEL_SDK_DISABLED": "TRUE", "OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL},
		"traces none, logs and metrics on": {
			"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL, "OTEL_TRACES_EXPORTER": "none",
			"OTEL_LOGS_EXPORTER": "otlp", "OTEL_METRICS_EXPORTER": "otlp",
		},
		"only a logs endpoint": {"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": collector.URL + "/v1/logs"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			for _, mode := range []string{"cli", "mcp"} {
				tel, err := SetupClientTelemetry(context.Background(), mapLookup(env), mode)
				if err != nil {
					t.Fatalf("%s: SetupClientTelemetry: %v", mode, err)
				}
				if tel.Enabled() {
					t.Errorf("%s: client telemetry is on", mode)
				}
				rt := &http.Transport{}
				if tel.WrapTransport(rt) != http.RoundTripper(rt) {
					t.Errorf("%s: the transport was wrapped", mode)
				}
				if err := tel.Shutdown(context.Background()); err != nil {
					t.Errorf("%s: Shutdown: %v", mode, err)
				}
			}
		})
	}
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if len(seen.bodies) != 0 {
		t.Errorf("the collector received %v with client telemetry off", seen.auth)
	}
}

// Criterion (PC-04): the injected lookup is the only configuration source.
// OTEL_* values in the process environment neither turn client tracing on nor
// change where it exports or which headers it sends.
func TestSetupClientTelemetryReadsOnlyTheInjectedLookup(t *testing.T) {
	processCollector, processSeen := newCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", processCollector.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", processCollector.URL+"/v1/traces")
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=from-process")
	t.Setenv("OTEL_SERVICE_NAME", "svc-from-process")
	api := newAPIStub(t, nil)

	tel, err := SetupClientTelemetry(context.Background(), mapLookup(nil), "cli")
	if err != nil {
		t.Fatal(err)
	}
	if tel.Enabled() {
		t.Error("the process environment turned client tracing on; only lookup may")
	}
	doAll(t, tel.WrapTransport(http.DefaultTransport), plantedRequests(t, api.srv.URL)[:1])
	shutdownWithin(t, tel, 5*time.Second)
	if got := api.requests(); len(got) != 1 || got[0].header.Get("Traceparent") != "" {
		t.Errorf("traceparent sent with client tracing off: %v", got)
	}

	collector, seen := newCollector(t)
	tel, err = SetupClientTelemetry(context.Background(), mapLookup(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL,
	}), "cli")
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	doAll(t, tel.WrapTransport(http.DefaultTransport), plantedRequests(t, api.srv.URL)[:1])
	shutdownWithin(t, tel, 5*time.Second)

	processSeen.mu.Lock()
	if len(processSeen.bodies) != 0 {
		t.Errorf("exported to the process-environment collector: %v", processSeen.auth)
	}
	processSeen.mu.Unlock()
	seen.mu.Lock()
	defer seen.mu.Unlock()
	body := seen.bodies["/v1/traces"]
	if len(body) == 0 {
		t.Fatalf("no spans reached the lookup collector (paths: %v)", seen.auth)
	}
	if a := seen.auth["/v1/traces"]; a != "" {
		t.Errorf("Authorization = %q, want none: the process environment's headers were sent", a)
	}
	if strings.Contains(string(body), "svc-from-process") {
		t.Error("service.name came from the process environment")
	}
}

// PC-04, PC-05: an invalid value fails setup with an error that names the
// variable and never echoes the value, which can be a secret.
func TestSetupClientTelemetryInvalidValueNamesTheVariableOnly(t *testing.T) {
	const secret = "hunter2-client"
	for _, c := range [][2]string{
		{"OTEL_SDK_DISABLED", secret},
		{"OTEL_EXPORTER_OTLP_ENDPOINT", "ftp://" + secret},
		{"OTEL_EXPORTER_OTLP_HEADERS", "authorization=" + secret + "%zz"},
		{"OTEL_EXPORTER_OTLP_TRACES_HEADERS", secret},
		{"OTEL_TRACES_SAMPLER", secret},
	} {
		env := map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", c[0]: c[1]}
		tel, err := SetupClientTelemetry(context.Background(), mapLookup(env), "cli")
		if err == nil {
			_ = tel.Shutdown(context.Background())
			t.Errorf("%s: invalid value accepted", c[0])
			continue
		}
		if !strings.Contains(err.Error(), c[0]) || strings.Contains(err.Error(), secret) {
			t.Errorf("%s: err = %q, want it to name the variable and not echo the value", c[0], err)
		}
	}
}
