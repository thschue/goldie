package platform

import (
	"context"
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
	tel := newTelemetry(telemetryResource(), spans, &logSink{})
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
	tel := newTelemetry(telemetryResource(), newSpanSink(), logs)
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
