package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// logSink is an in-memory log exporter.
type logSink struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (l *logSink) Export(_ context.Context, rs []sdklog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range rs {
		l.records = append(l.records, r.Clone())
	}
	return nil
}
func (l *logSink) Shutdown(context.Context) error   { return nil }
func (l *logSink) ForceFlush(context.Context) error { return nil }

func (l *logSink) bodies() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, r := range l.records {
		out = append(out, r.Body().AsString())
	}
	return out
}

// spanSink keeps spans after Shutdown, which tracetest's exporter resets.
type spanSink struct {
	*tracetest.InMemoryExporter
	mu   sync.Mutex
	kept tracetest.SpanStubs
}

func newSpanSink() *spanSink { return &spanSink{InMemoryExporter: tracetest.NewInMemoryExporter()} }

func (s *spanSink) ExportSpans(ctx context.Context, sp []sdktrace.ReadOnlySpan) error {
	s.mu.Lock()
	s.kept = append(s.kept, tracetest.SpanStubsFromReadOnlySpans(sp)...)
	s.mu.Unlock()
	return nil
}

func (s *spanSink) spans() tracetest.SpanStubs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(tracetest.SpanStubs(nil), s.kept...)
}

func mapLookup(m map[string]string) Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// defaultTraceSettings is what an empty lookup yields.
func defaultTraceSettings() traceSettings {
	ts, _ := parseTraceSettings(func(string) string { return "" })
	return ts
}

// startTraced runs a Server with telemetry on ephemeral ports. The returned
// stop cancels the server (the drain flushes telemetry) and waits for Serve.
func startTraced(t *testing.T, tel *Telemetry, logs *syncBuffer) (app, admin string, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	addrs := make(chan [2]string, 2)
	logger := tel.WrapLogger(NewLogger(logs, 0, "server"), "server")
	srv := &Server{
		Config:    Config{Addr: "127.0.0.1:0", AdminAddr: "127.0.0.1:0"},
		Logger:    logger,
		Telemetry: tel,
		App: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
		OnListen: func(l, a string) { addrs <- [2]string{l, a} },
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, make(chan struct{})) }()
	for range 2 {
		select {
		case a := <-addrs:
			if a[0] == "app" {
				app = a[1]
			} else {
				admin = a[1]
			}
		case <-time.After(5 * time.Second):
			t.Fatal("server did not start listening")
		}
	}
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Serve did not return")
		}
	}
	t.Cleanup(stop)
	return app, admin, stop
}

func TestTelemetryOneServerSpanOnPublicListener(t *testing.T) {
	spans := newSpanSink()
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), spans, &logSink{})
	app, admin, stop := startTraced(t, tel, &syncBuffer{})

	req, _ := http.NewRequest("GET", "http://"+app+"/api/v1/items?token=s3cret&q=hunter2", nil)
	req.Header.Set("Authorization", "Bearer topsecret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	get(t, "http://"+admin+"/livez")
	get(t, "http://"+admin+"/readyz")
	get(t, "http://"+admin+"/version")
	stop() // the drain flushes the batch processors

	got := spans.spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want exactly 1 (admin listener must not be traced): %+v", len(got), got)
	}
	s := got[0]
	if s.SpanKind.String() != "server" {
		t.Errorf("span kind = %v, want server", s.SpanKind)
	}
	if s.Name != "GET" {
		t.Errorf("span name = %q, want the method only (no raw path: unbounded cardinality)", s.Name)
	}
	res := map[string]string{}
	for _, kv := range s.Resource.Attributes() {
		res[string(kv.Key)] = kv.Value.String()
	}
	want := map[string]string{
		"service.name":    "goldie",
		"service.version": CurrentVersion().Version,
		"goldie.mode":     "all",
	}
	for k, v := range want {
		if res[k] != v {
			t.Errorf("resource %s = %q, want %q", k, res[k], v)
		}
	}
	// Nothing secret may reach a span: no query text, no headers.
	for _, kv := range s.Attributes {
		v := kv.Value.String()
		for _, bad := range []string{"s3cret", "hunter2", "topsecret", "Bearer"} {
			if strings.Contains(v, bad) {
				t.Errorf("span attribute %s=%q leaks request data", kv.Key, v)
			}
		}
		if k := string(kv.Key); k == "url.query" || strings.HasPrefix(k, "http.request.header.") {
			t.Errorf("span records %s by default", k)
		}
	}
}

func TestTelemetryLogsFlowThroughBridgeAndKeepPC07Shape(t *testing.T) {
	logs := &logSink{}
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), newSpanSink(), logs)
	out := &syncBuffer{}
	_, _, stop := startTraced(t, tel, out)
	stop()

	if !strings.Contains(out.String(), `"component":"server"`) || !strings.Contains(out.String(), `"msg":"listening"`) {
		t.Errorf("stdout lost the PC-07 JSON shape: %s", out.String())
	}
	var found bool
	for _, b := range logs.bodies() {
		if b == "listening" {
			found = true
		}
	}
	if !found {
		t.Errorf("no log record reached the exporter: %v", logs.bodies())
	}
}

func TestTelemetryUnsetIsANoOp(t *testing.T) {
	tel, err := SetupTelemetry(context.Background(), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if tel.Enabled() {
		t.Fatal("telemetry enabled with no OTEL_* variables")
	}
	h := http.NewServeMux()
	if got := tel.WrapHandler(h); got != http.Handler(h) {
		t.Error("disabled telemetry must not wrap the handler")
	}
	l := slog.New(slog.DiscardHandler)
	if tel.WrapLogger(l, "server") != l {
		t.Error("disabled telemetry must not wrap the logger")
	}
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown on disabled telemetry: %v", err)
	}
	// SDK disabled wins over a configured endpoint.
	tel, err = SetupTelemetry(context.Background(), mapLookup(map[string]string{
		"OTEL_SDK_DISABLED":           "true",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
	}))
	if err != nil || tel.Enabled() {
		t.Fatalf("OTEL_SDK_DISABLED=true: enabled=%v err=%v", tel.Enabled(), err)
	}
}

func TestTelemetryConfiguredIsEnabled(t *testing.T) {
	tel, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !tel.Enabled() {
		t.Fatal("an OTLP endpoint must enable telemetry")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tel.Shutdown(ctx)
}

func TestTelemetryInvalidValueFailsFastNamingVariable(t *testing.T) {
	cases := []struct {
		name, val string
	}{
		{"OTEL_SDK_DISABLED", "maybe"},
		{"OTEL_EXPORTER_OTLP_ENDPOINT", "not a url"},
		{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "ftp://collector"},
		{"OTEL_EXPORTER_OTLP_PROTOCOL", "grpc"},
		{"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "carrier-pigeon"},
		{"OTEL_TRACES_EXPORTER", "zipkin"},
		{"OTEL_LOGS_EXPORTER", "console"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", c.name: c.val}
			_, err := SetupTelemetry(context.Background(), mapLookup(env))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), c.name) {
				t.Errorf("error %q does not name %s", err, c.name)
			}
			if strings.Contains(err.Error(), c.val) {
				t.Errorf("error %q echoes the value (PC-05)", err)
			}
		})
	}
}

func TestTelemetryNoneExporterDisablesSignal(t *testing.T) {
	tel, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{
		"OTEL_TRACES_EXPORTER":        "none",
		"OTEL_LOGS_EXPORTER":          "none",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
	}))
	if err != nil || tel.Enabled() {
		t.Fatalf("both signals none: enabled=%v err=%v", tel.Enabled(), err)
	}
}

func TestTelemetryExportsOverOTLPHTTPWithHeadersFromFile(t *testing.T) {
	var mu sync.Mutex
	paths := map[string]string{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		paths[r.URL.Path] = r.Header.Get("X-Api-Token")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	secret := filepath.Join(t.TempDir(), "headers")
	if err := os.WriteFile(secret, []byte("x-api-token=sekrit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS_FILE", secret)
	tel, err := SetupTelemetry(context.Background(), os.LookupEnv)
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	app, _, stop := startTraced(t, tel, &syncBuffer{})
	get(t, "http://"+app+"/x")
	stop()
	mu.Lock()
	defer mu.Unlock()
	for _, p := range []string{"/v1/traces", "/v1/logs"} {
		if paths[p] != "sekrit" {
			t.Errorf("collector got token %q on %s, want the one from the _FILE (paths: %v)", paths[p], p, paths)
		}
	}
}

func TestTelemetryHeaderErrorsNeverEchoValues(t *testing.T) {
	_, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{
		"OTEL_EXPORTER_OTLP_HEADERS": "tokenvalue-without-equals",
	}))
	if err == nil || !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_HEADERS") || strings.Contains(err.Error(), "tokenvalue") {
		t.Fatalf("err = %v", err)
	}
}

// OTLP header values are percent-encoded (OpenTelemetry spec); a collector
// token such as "Basic%20..." must arrive decoded.
func TestTelemetryHeaderValuesArePercentDecoded(t *testing.T) {
	var mu sync.Mutex
	var got string
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		got = r.Header.Get("Authorization")
		mu.Unlock()
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Basic%20c2Vrcml0")
	tel, err := SetupTelemetry(context.Background(), os.LookupEnv)
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	app, _, stop := startTraced(t, tel, &syncBuffer{})
	get(t, "http://"+app+"/x")
	stop()
	mu.Lock()
	defer mu.Unlock()
	if got != "Basic c2Vrcml0" {
		t.Errorf("collector got Authorization %q, want the percent-decoded value", got)
	}
}

// Span attributes copy request data an unauthenticated caller controls (user
// agent, Host, path, X-Forwarded-For). Unbounded, every queued span could hold
// up to the 1 MiB header limit until the collector takes it.
func TestTelemetrySpanAttributeValuesAreBounded(t *testing.T) {
	spans := newSpanSink()
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), spans, nil)
	app, _, stop := startTraced(t, tel, &syncBuffer{})
	req, _ := http.NewRequest("GET", "http://"+app+"/"+strings.Repeat("p", 4000), nil)
	req.Header.Set("User-Agent", strings.Repeat("A", 100_000))
	req.Header.Set("X-Forwarded-For", strings.Repeat("1", 100_000))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	stop()
	got := spans.spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	for _, kv := range got[0].Attributes {
		if n := len(kv.Value.String()); n > defaultSpanAttributeValueLimit {
			t.Errorf("span attribute %s is %d bytes, want at most %d", kv.Key, n, defaultSpanAttributeValueLimit)
		}
	}
}

// SDK export errors carry the collector URL, which can hold credentials
// (PC-05): only a fixed warning is logged, at most once a minute.
func TestTelemetryReportErrorsToRedactsAndRateLimits(t *testing.T) {
	prev := otel.GetErrorHandler()
	t.Cleanup(func() { otel.SetErrorHandler(prev) })
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), newSpanSink(), nil)
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	out := &syncBuffer{}
	tel.WrapLogger(NewLogger(out, 0, "server"), "server")

	otel.Handle(errors.New(`Post "http://user:pw@collector:4318/v1/traces": dial failed`))
	otel.Handle(errors.New(`Post "http://user:pw@collector:4318/v1/traces": dial failed`))

	got := out.String()
	if n := strings.Count(got, "OpenTelemetry SDK reported"); n != 1 {
		t.Errorf("got %d warnings, want exactly 1 (rate limit): %s", n, got)
	}
	for _, bad := range []string{"user:pw", "collector:4318", "dial failed"} {
		if strings.Contains(got, bad) {
			t.Errorf("log leaks the SDK error text %q: %s", bad, got)
		}
	}
}

// A malformed escape fails startup without echoing the value. Accepting it
// would let the SDK's own env parser print the raw header to stderr.
func TestTelemetryHeaderBadEscapeFailsWithoutEcho(t *testing.T) {
	_, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
		"OTEL_EXPORTER_OTLP_HEADERS":  "authorization=hunter2%zz",
	}))
	if err == nil || !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_HEADERS") || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("err = %v", err)
	}
}

// The bridge must not export records the logger drops: GOLDIE_LOG_LEVEL gates
// both outputs.
func TestTelemetryLogBridgeHonoursLogLevel(t *testing.T) {
	logs := &logSink{}
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), nil, logs)
	l := tel.WrapLogger(NewLogger(&syncBuffer{}, slog.LevelWarn, "server"), "server")
	l.Info("quiet-info")
	l.With("k", "v").Warn("loud-warn")
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := logs.bodies()
	if len(got) != 1 || got[0] != "loud-warn" {
		t.Errorf("exported %v, want only loud-warn", got)
	}
}

// *_FILE works only where goldie reads the value itself. For the other names
// the SDK would ignore it and fall back to its defaults, so it is an error.
func TestTelemetryFileFormRejectedForNonSecretNames(t *testing.T) {
	_, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT_FILE": "/run/secrets/endpoint",
	}))
	if err == nil || !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT_FILE") || strings.Contains(err.Error(), "/run/secrets") {
		t.Errorf("err = %v", err)
	}
}

// A malformed OTEL_RESOURCE_ATTRIBUTES fails startup naming the variable and
// never echoing it; the SDK alone would drop it silently.
func TestTelemetryResourceAttributesMalformedFailsFast(t *testing.T) {
	for _, val := range []string{"hunter2", "k=v%zz-hunter2", "=hunter2", "a=b,hunter2"} {
		_, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
			"OTEL_RESOURCE_ATTRIBUTES":    val,
		}))
		if err == nil || !strings.Contains(err.Error(), "OTEL_RESOURCE_ATTRIBUTES") || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("value %q: err = %v", val, err)
		}
	}
}

// OTEL_RESOURCE_ATTRIBUTES adds attributes and may rename the service, but
// cannot override what goldie owns; OTEL_SERVICE_NAME wins over it.
func TestTelemetryResourceReservedAttributesWin(t *testing.T) {
	attrs, err := parseResourceAttributes("deployment.environment=prod%20eu,service.version=9.9.9,goldie.mode=server,service.name=a")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, kv := range telemetryResource("", attrs).Attributes() {
		got[string(kv.Key)] = kv.Value.String()
	}
	want := map[string]string{
		"deployment.environment": "prod eu", "service.name": "a",
		"service.version": CurrentVersion().Version, "goldie.mode": "all",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, kv := range telemetryResource("b", attrs).Attributes() {
		if kv.Key == "service.name" && kv.Value.String() != "b" {
			t.Errorf("OTEL_SERVICE_NAME did not win: %v", kv.Value)
		}
	}
}

// captureSink records everything the otel global logger is asked to print.
type captureSink struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (c *captureSink) Init(logr.RuntimeInfo)  {}
func (c *captureSink) Enabled(level int) bool { return level <= 0 }
func (c *captureSink) Info(_ int, msg string, kv ...any) {
	c.write(msg, nil, kv)
}
func (c *captureSink) Error(err error, msg string, kv ...any) {
	c.write(msg, err, kv)
}
func (c *captureSink) WithValues(...any) logr.LogSink { return c }
func (c *captureSink) WithName(string) logr.LogSink   { return c }
func (c *captureSink) write(msg string, err error, kv []any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.WriteString(msg)
	if err != nil {
		c.buf.WriteString(err.Error())
	}
	for _, v := range kv {
		fmt.Fprint(&c.buf, v)
	}
}
func (c *captureSink) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func captureOTelLogger(t *testing.T) *captureSink {
	t.Helper()
	sink := &captureSink{}
	otel.SetLogger(logr.New(sink))
	t.Cleanup(func() { otel.SetLogger(logr.Discard()) })
	return sink
}

// Whatever goldie accepts in an OTLP headers variable must not make the
// exporter SDK print header text to the otel global logger (PC-05, PC-07,
// PC-12). Every case is refused at startup, so the SDK never parses it.
func TestTelemetryHeaderTextNeverReachesOTelLogger(t *testing.T) {
	const secret = "s3cr3t-marker"
	bad := "a b" + secret + "=v"
	cases := map[string]map[string]string{
		"key with a space":          {"OTEL_EXPORTER_OTLP_HEADERS": bad},
		"key with a non-ASCII rune": {"OTEL_EXPORTER_OTLP_HEADERS": "k\u00e9" + secret + "=v"},
		"key with a slash":          {"OTEL_EXPORTER_OTLP_HEADERS": "a/" + secret + "=v"},
		"bad escape":                {"OTEL_EXPORTER_OTLP_HEADERS": "authorization=" + secret + "%zz"},
		"no equals":                 {"OTEL_EXPORTER_OTLP_HEADERS": secret},
		"bad generic, good traces":  {"OTEL_EXPORTER_OTLP_HEADERS": bad, "OTEL_EXPORTER_OTLP_TRACES_HEADERS": "x-ok=1"},
		"bad generic, good logs":    {"OTEL_EXPORTER_OTLP_HEADERS": bad, "OTEL_EXPORTER_OTLP_LOGS_HEADERS": "x-ok=1"},
		"bad traces, good generic":  {"OTEL_EXPORTER_OTLP_HEADERS": "x-ok=1", "OTEL_EXPORTER_OTLP_TRACES_HEADERS": bad},
		"bad logs, good generic":    {"OTEL_EXPORTER_OTLP_HEADERS": "x-ok=1", "OTEL_EXPORTER_OTLP_LOGS_HEADERS": bad},
		"bad logs, good traces":     {"OTEL_EXPORTER_OTLP_TRACES_HEADERS": "x-ok=1", "OTEL_EXPORTER_OTLP_LOGS_HEADERS": bad},
		"bad escape in logs":        {"OTEL_EXPORTER_OTLP_LOGS_HEADERS": "authorization=" + secret + "%zz"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			sink := captureOTelLogger(t)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
			for k, v := range env {
				t.Setenv(k, v)
			}
			tel, err := SetupTelemetry(context.Background(), os.LookupEnv)
			if strings.Contains(sink.String(), secret) {
				t.Error("header text reached the otel logger")
			}
			if err == nil {
				_ = tel.Shutdown(context.Background())
				t.Fatal("a header goldie must refuse was accepted")
			}
			if strings.Contains(err.Error(), secret) {
				t.Error("error echoes header text")
			}
		})
	}
}

// The exporters get the endpoints goldie validated, with the OTLP path rules:
// the generic URL is a base for /v1/<signal>, a signal-specific URL is used
// as-is and wins.
func TestTelemetryExportersUseTheValidatedEndpoints(t *testing.T) {
	cases := map[string]struct {
		env  func(base string) map[string]string
		want []string
	}{
		"generic base URL with a path": {
			env:  func(b string) map[string]string { return map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": b + "/otlp"} },
			want: []string{"/otlp/v1/traces", "/otlp/v1/logs"},
		},
		"signal-specific URLs are used as-is": {
			env: func(b string) map[string]string {
				return map[string]string{
					"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": b + "/t",
					"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":   b,
				}
			},
			want: []string{"/t", "/"},
		},
		"signal-specific wins over generic": {
			env: func(b string) map[string]string {
				return map[string]string{
					"OTEL_EXPORTER_OTLP_ENDPOINT":      b + "/base",
					"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": b + "/logs",
				}
			},
			want: []string{"/base/v1/traces", "/logs"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			paths := map[string]bool{}
			collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				mu.Lock()
				paths[r.URL.Path] = true
				mu.Unlock()
			}))
			defer collector.Close()
			for k, v := range c.env(collector.URL) {
				t.Setenv(k, v)
			}
			tel, err := SetupTelemetry(context.Background(), os.LookupEnv)
			if err != nil || !tel.Enabled() {
				t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
			}
			app, _, stop := startTraced(t, tel, &syncBuffer{})
			get(t, "http://"+app+"/x")
			stop()
			mu.Lock()
			defer mu.Unlock()
			for _, p := range c.want {
				if !paths[p] {
					t.Errorf("collector got nothing on %s (got %v)", p, paths)
				}
			}
		})
	}
}

// The SDK parses the endpoint variables itself, untrimmed, and prints a value
// it rejects to stderr before falling back to its default endpoint. goldie
// refuses such a value at startup instead, naming the variable only (PC-04).
func TestTelemetryRefusesEndpointsTheSDKWouldPrint(t *testing.T) {
	const marker = "collector-marker.invalid"
	for _, name := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"} {
		for _, v := range []string{" http://" + marker + ":4318", "http://" + marker + ":4318\n", "http://" + marker + ":4318/\t"} {
			_, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{name: v}))
			if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), marker) {
				t.Errorf("%s=%q: err = %v, want an error naming only the variable", name, v, err)
			}
		}
	}
}

// Well-formed headers are accepted and the SDK has nothing to complain about.
func TestTelemetryValidHeadersLeaveSDKReportSilent(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Basic%20abc,X-Api_Token.1=v")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "x-logs=1")
	tel, err := SetupTelemetry(context.Background(), os.LookupEnv)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	out := &syncBuffer{}
	tel.WrapLogger(NewLogger(out, 0, "server"), "server")
	_ = tel.Shutdown(context.Background())
	if got := out.String(); got != "" {
		t.Errorf("logger was not silent: %q", got)
	}
}
