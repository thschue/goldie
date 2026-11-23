package platform

import (
	"context"
	"encoding/json"
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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	collmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// The metrics provider (#151). The seam is SetupTelemetry and the Telemetry
// methods, as for traces and logs. The tests rely on:
//   - (*Telemetry).Meter(name string) metric.Meter, safe on nil and disabled;
//   - newTelemetry(res, ts, spans, logs, opts ...telemetryOption) and
//     withMetricReader(sdkmetric.Reader) telemetryOption, which builds the
//     MeterProvider around the given reader instead of an OTLP exporter;
//   - the variables OTEL_METRICS_EXPORTER, OTEL_EXPORTER_OTLP_METRICS_{ENDPOINT,
//     PROTOCOL,HEADERS,TEMPORALITY_PREFERENCE} and OTEL_METRIC_EXPORT_INTERVAL.

const genericEndpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT"

// metricsOnly turns traces and logs off so only metrics are exported.
func metricsOnly(env map[string]string) map[string]string {
	env["OTEL_TRACES_EXPORTER"] = "none"
	env["OTEL_LOGS_EXPORTER"] = "none"
	return env
}

// metricCollector is an OTLP collector that decodes the metrics it receives.
type metricCollector struct {
	srv    *httptest.Server
	mu     sync.Mutex
	paths  map[string]int    // requests per URL path
	token  map[string]string // last X-Api-Token per URL path
	reqs   []*collmetrics.ExportMetricsServiceRequest
	status int
}

func newMetricCollector(t *testing.T, status int) *metricCollector {
	t.Helper()
	c := &metricCollector{paths: map[string]int{}, token: map[string]string{}, status: status}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.paths[r.URL.Path]++
		c.token[r.URL.Path] = r.Header.Get("X-Api-Token")
		var req collmetrics.ExportMetricsServiceRequest
		if strings.HasSuffix(r.URL.Path, "/v1/metrics") && proto.Unmarshal(b, &req) == nil {
			c.reqs = append(c.reqs, &req)
		}
		c.mu.Unlock()
		w.WriteHeader(c.status)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *metricCollector) hits(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paths[path]
}

func (c *metricCollector) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.paths {
		n += v
	}
	return n
}

func (c *metricCollector) tokenAt(path string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token[path]
}

// metrics returns every metric received so far with its resource attributes.
func (c *metricCollector) metrics() (out []*metricspb.Metric, res map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	res = map[string]string{}
	for _, r := range c.reqs {
		for _, rm := range r.ResourceMetrics {
			for _, kv := range rm.GetResource().GetAttributes() {
				res[kv.Key] = kv.Value.GetStringValue()
			}
			for _, sm := range rm.ScopeMetrics {
				out = append(out, sm.Metrics...)
			}
		}
	}
	return out, res
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func countThree(t *testing.T, tel *Telemetry) {
	t.Helper()
	c, err := tel.Meter("goldie.test").Int64Counter("goldie.test.count")
	if err != nil {
		t.Fatal(err)
	}
	c.Add(context.Background(), 3)
}

func shutdownOK(t *testing.T, tel *Telemetry) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tel.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func shutdownQuietly(tel *Telemetry) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tel.Shutdown(ctx)
}

// --- selection -----------------------------------------------------------

func TestMetricsUnsetIsANoOpMeterAndBuildsNoExporter(t *testing.T) {
	col := newMetricCollector(t, http.StatusOK)
	// A process environment that would turn metrics on if the SDK read it.
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", col.srv.URL)
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	var nilTel *Telemetry
	if nilTel.Meter("x") == nil {
		t.Error("Meter on a nil Telemetry returned nil")
	}
	tel, err := SetupTelemetry(context.Background(), mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if tel.Enabled() {
		t.Fatal("telemetry enabled with nothing set")
	}
	m := tel.Meter("goldie.test")
	if m == nil {
		t.Fatal("Meter returned nil when disabled")
	}
	c, err := m.Int64Counter("x") // must not panic or error
	if err != nil {
		t.Fatal(err)
	}
	c.Add(context.Background(), 1)
	shutdownOK(t, tel)
	time.Sleep(100 * time.Millisecond)
	if col.total() != 0 {
		t.Errorf("an exporter was built: collector got %v", col.paths)
	}
}

func TestMetricsAreTurnedOnByExporterOrEndpoint(t *testing.T) {
	cases := map[string]map[string]string{
		"exporter otlp":                  {"OTEL_METRICS_EXPORTER": "otlp"},
		"exporter otlp case-insensitive": {"OTEL_METRICS_EXPORTER": "OTLP"},
		"generic endpoint":               {genericEndpointEnv: "{URL}"},
		"metrics endpoint":               {"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "{URL}/m"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			col := newMetricCollector(t, http.StatusOK)
			resolved := map[string]string{}
			for k, v := range env {
				resolved[k] = strings.ReplaceAll(v, "{URL}", col.srv.URL)
			}
			tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(resolved)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { shutdownQuietly(tel) })
			if !tel.Enabled() {
				t.Fatal("metrics did not turn telemetry on")
			}
			out := &syncBuffer{}
			tel.Report(NewLogger(out, 0, "server"))
			var rec map[string]any
			if err := json.Unmarshal([]byte(out.String()), &rec); err != nil {
				t.Fatalf("report is not one JSON line: %v: %s", err, out.String())
			}
			if rec["signals"] != "metrics" {
				t.Errorf("signals = %v, want metrics", rec["signals"])
			}
		})
	}
}

func TestMetricsOffByNoneAndBySDKDisabled(t *testing.T) {
	cases := map[string]map[string]string{
		"exporter none wins over an endpoint": {"OTEL_METRICS_EXPORTER": "none", "OTEL_TRACES_EXPORTER": "none", "OTEL_LOGS_EXPORTER": "none", genericEndpointEnv: "http://127.0.0.1:1"},
		"sdk disabled":                        {"OTEL_SDK_DISABLED": "true", "OTEL_METRICS_EXPORTER": "otlp", genericEndpointEnv: "http://127.0.0.1:1"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			tel, err := SetupTelemetry(context.Background(), mapLookup(env))
			if err != nil || tel.Enabled() {
				t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
			}
			c, err := tel.Meter("x").Int64Counter("y")
			if err != nil {
				t.Fatal(err)
			}
			c.Add(context.Background(), 1)
		})
	}
}

// Metrics on with traces and logs off, and the reverse.
func TestMetricsIndependentOfTracesAndLogs(t *testing.T) {
	t.Run("metrics only", func(t *testing.T) {
		col := newMetricCollector(t, http.StatusOK)
		tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(map[string]string{genericEndpointEnv: col.srv.URL})))
		if err != nil || !tel.Enabled() {
			t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
		}
		h := http.NewServeMux()
		if tel.WrapHandler(h) != http.Handler(h) {
			t.Error("traces are off: the handler must not be wrapped")
		}
		l := slog.New(slog.DiscardHandler)
		if tel.WrapLogger(l, "server") != l {
			t.Error("logs are off: the logger must not get a bridge")
		}
		countThree(t, tel)
		shutdownOK(t, tel)
		if col.hits("/v1/metrics") == 0 {
			t.Errorf("no metrics exported: %v", col.paths)
		}
		if col.hits("/v1/traces")+col.hits("/v1/logs") != 0 {
			t.Errorf("traces or logs exported while off: %v", col.paths)
		}
	})
	t.Run("traces and logs only", func(t *testing.T) {
		col := newMetricCollector(t, http.StatusOK)
		tel, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{
			genericEndpointEnv:      col.srv.URL,
			"OTEL_METRICS_EXPORTER": "none",
		}))
		if err != nil || !tel.Enabled() {
			t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
		}
		countThree(t, tel) // a no-op meter
		out := &syncBuffer{}
		tel.Report(NewLogger(out, 0, "server"))
		if strings.Contains(out.String(), "metrics") {
			t.Errorf("report names metrics while they are off: %s", out.String())
		}
		shutdownOK(t, tel)
		if col.hits("/v1/metrics") != 0 {
			t.Errorf("metrics exported while off: %v", col.paths)
		}
	})
	t.Run("all three", func(t *testing.T) {
		tel, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{genericEndpointEnv: "http://127.0.0.1:1"}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { shutdownQuietly(tel) })
		out := &syncBuffer{}
		tel.Report(NewLogger(out, 0, "server"))
		var rec map[string]any
		_ = json.Unmarshal([]byte(out.String()), &rec)
		if rec["signals"] != "traces,logs,metrics" {
			t.Errorf("signals = %v, want traces,logs,metrics: %s", rec["signals"], out.String())
		}
		if rec["metrics_endpoint"] != "http://127.0.0.1:1" {
			t.Errorf("report lacks the metrics endpoint: %s", out.String())
		}
	})
}

func TestMetricsReportRedactsEndpoint(t *testing.T) {
	tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(map[string]string{
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "https://user:pw-marker@collector.example:4318/tenant-marker?token=q-marker",
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS":  "authorization=Bearer%20hdr-marker",
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdownQuietly(tel) })
	out := &syncBuffer{}
	tel.Report(NewLogger(out, 0, "server"))
	if !strings.Contains(out.String(), `"https://collector.example:4318"`) {
		t.Errorf("report lacks the collector: %s", out.String())
	}
	for _, bad := range []string{"pw-marker", "tenant-marker", "q-marker", "hdr-marker", "user:"} {
		if strings.Contains(out.String(), bad) {
			t.Errorf("report leaks %q: %s", bad, out.String())
		}
	}
}

func TestMetricsDefaultEndpointUsesTLS(t *testing.T) {
	tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(map[string]string{"OTEL_METRICS_EXPORTER": "otlp"})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdownQuietly(tel) })
	out := &syncBuffer{}
	tel.Report(NewLogger(out, 0, "server"))
	if !strings.Contains(out.String(), `"metrics_endpoint":"https://localhost:4318"`) {
		t.Errorf("default endpoint is not https://localhost:4318: %s", out.String())
	}
}

// --- validation ----------------------------------------------------------

func TestMetricsInvalidValueFailsFastNamingVariable(t *testing.T) {
	cases := []struct{ name, val string }{
		{"OTEL_METRICS_EXPORTER", "prometheus"},
		{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "ftp://collector"},
		{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "not a url"},
		{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", " http://127.0.0.1:4318"},
		{"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "grpc"},
		{"OTEL_EXPORTER_OTLP_METRICS_HEADERS", "tokenvalue-without-equals"},
		{"OTEL_EXPORTER_OTLP_METRICS_HEADERS", "bad key=v"},
		{"OTEL_EXPORTER_OTLP_METRICS_HEADERS", "k=%zz-secretmarker"},
		{"OTEL_METRIC_EXPORT_INTERVAL", "0"},
		{"OTEL_METRIC_EXPORT_INTERVAL", "-5"},
		{"OTEL_METRIC_EXPORT_INTERVAL", "1.5"},
		{"OTEL_METRIC_EXPORT_INTERVAL", "9x9marker"},
		{"OTEL_METRIC_EXPORT_INTERVAL", "10s"},
		{"OTEL_METRIC_EXPORT_INTERVAL", "99999999999999999999"},
		{"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "sometimes"},
	}
	for _, c := range cases {
		t.Run(c.name+"="+c.val, func(t *testing.T) {
			env := map[string]string{genericEndpointEnv: "http://127.0.0.1:4318", c.name: c.val}
			tel, err := SetupTelemetry(context.Background(), mapLookup(env))
			if err == nil {
				t.Fatal("want an error")
			}
			if tel != nil {
				t.Error("a failed setup must not return telemetry")
			}
			if !strings.Contains(err.Error(), c.name) {
				t.Errorf("error %q does not name %s", err, c.name)
			}
			if v := strings.TrimSpace(c.val); len(v) > 2 && strings.Contains(err.Error(), v) {
				t.Errorf("error %q echoes the value (PC-05)", err)
			}
		})
	}
}

func TestMetricsValidValuesAreAccepted(t *testing.T) {
	for _, env := range []map[string]string{
		{"OTEL_METRIC_EXPORT_INTERVAL": "1"},
		{"OTEL_METRIC_EXPORT_INTERVAL": "60000"},
		{"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE": "cumulative"},
		{"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE": "delta"},
		{"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE": "lowmemory"},
		{"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE": "DELTA"},
		{"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "http/protobuf"},
		{"OTEL_EXPORTER_OTLP_METRICS_HEADERS": "x-api-token=a%20b"},
	} {
		env[genericEndpointEnv] = "http://127.0.0.1:1"
		tel, err := SetupTelemetry(context.Background(), mapLookup(env))
		if err != nil {
			t.Errorf("%v: %v", env, err)
			continue
		}
		shutdownQuietly(tel)
	}
}

func TestMetricsFileFormOnlyForHeaders(t *testing.T) {
	for _, name := range []string{
		"OTEL_METRICS_EXPORTER",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL",
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE",
		"OTEL_METRIC_EXPORT_INTERVAL",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{
				genericEndpointEnv: "http://127.0.0.1:4318",
				name + "_FILE":     "/no/such/file-marker",
			}))
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("err = %v, want one naming %s", err, name)
			}
			if strings.Contains(err.Error(), "file-marker") {
				t.Errorf("error echoes the path: %v", err)
			}
		})
	}
}

func TestMetricsHeadersAcceptFileFormAndWinOverGeneric(t *testing.T) {
	col := newMetricCollector(t, http.StatusOK)
	file := filepath.Join(t.TempDir(), "headers")
	if err := os.WriteFile(file, []byte("x-api-token=metrics-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(map[string]string{
		genericEndpointEnv:                        col.srv.URL,
		"OTEL_EXPORTER_OTLP_HEADERS":              "x-api-token=generic-token",
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS_FILE": file,
	})))
	if err != nil {
		t.Fatal(err)
	}
	countThree(t, tel)
	shutdownOK(t, tel)
	if got := col.tokenAt("/v1/metrics"); got != "metrics-token" {
		t.Errorf("collector got token %q, want the one from the _FILE (paths: %v)", got, col.paths)
	}
}

func TestMetricsHeadersFileErrorsNeverEchoContent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "headers")
	if err := os.WriteFile(file, []byte("no-equals-sign-secretmarker"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS_FILE": file,
	}))
	if err == nil || !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_METRICS_HEADERS") || strings.Contains(err.Error(), "secretmarker") {
		t.Fatalf("err = %v", err)
	}
}

// --- endpoints -----------------------------------------------------------

func TestMetricsEndpointsGenericGetsPathSpecificIsUsedAsIs(t *testing.T) {
	cases := map[string]struct {
		env  func(base string) map[string]string
		want string
	}{
		"generic base URL gets the signal path": {
			env:  func(b string) map[string]string { return map[string]string{genericEndpointEnv: b + "/otlp"} },
			want: "/otlp/v1/metrics",
		},
		"specific is used as-is": {
			env: func(b string) map[string]string {
				return map[string]string{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": b + "/m"}
			},
			want: "/m",
		},
		"specific wins over generic": {
			env: func(b string) map[string]string {
				return map[string]string{genericEndpointEnv: b + "/base", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": b + "/mine"}
			},
			want: "/mine",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			col := newMetricCollector(t, http.StatusOK)
			// A conflicting process environment must not matter.
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://127.0.0.1:1/process-env")
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "x-api-token=process-env")
			tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(c.env(col.srv.URL))))
			if err != nil {
				t.Fatal(err)
			}
			countThree(t, tel)
			shutdownOK(t, tel)
			if col.hits(c.want) == 0 {
				t.Errorf("collector got nothing on %s (got %v)", c.want, col.paths)
			}
			if got := col.tokenAt(c.want); got == "process-env" {
				t.Error("the SDK read the process environment headers")
			}
		})
	}
}

// --- interval and temporality --------------------------------------------

func TestMetricsExportIntervalComesFromLookupNotTheProcessEnvironment(t *testing.T) {
	t.Run("lookup interval is honoured over a slow process environment", func(t *testing.T) {
		t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "3600000")
		col := newMetricCollector(t, http.StatusOK)
		tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(map[string]string{
			genericEndpointEnv:            col.srv.URL,
			"OTEL_METRIC_EXPORT_INTERVAL": "20",
		})))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { shutdownQuietly(tel) })
		countThree(t, tel)
		waitFor(t, "a periodic metrics export", 5*time.Second, func() bool { return col.hits("/v1/metrics") > 0 })
	})
	t.Run("the process environment is ignored when lookup sets none", func(t *testing.T) {
		t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "20")
		col := newMetricCollector(t, http.StatusOK)
		tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(map[string]string{genericEndpointEnv: col.srv.URL})))
		if err != nil {
			t.Fatal(err)
		}
		countThree(t, tel)
		time.Sleep(500 * time.Millisecond)
		if n := col.hits("/v1/metrics"); n != 0 {
			t.Errorf("exported %d times before the default interval: the SDK read the process environment", n)
		}
		shutdownQuietly(tel)
	})
	t.Run("an invalid process environment value does not fail startup", func(t *testing.T) {
		t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "garbage")
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "garbage")
		tel, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{genericEndpointEnv: "http://127.0.0.1:1"}))
		if err != nil {
			t.Fatalf("setup read the process environment: %v", err)
		}
		shutdownQuietly(tel)
	})
}

func TestMetricsTemporalityPreferenceIsPassedToTheSDK(t *testing.T) {
	const (
		cumulative = metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
		delta      = metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
	)
	cases := []struct {
		pref, processEnv string
		want             metricspb.AggregationTemporality
	}{
		{"", "delta", cumulative}, // unset: the default, whatever the process says
		{"cumulative", "delta", cumulative},
		{"delta", "cumulative", delta},
		{"lowmemory", "cumulative", delta}, // a counter is delta under lowmemory
	}
	for _, c := range cases {
		t.Run("pref="+c.pref, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", c.processEnv)
			col := newMetricCollector(t, http.StatusOK)
			env := map[string]string{genericEndpointEnv: col.srv.URL}
			if c.pref != "" {
				env["OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE"] = c.pref
			}
			tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(env)))
			if err != nil {
				t.Fatal(err)
			}
			countThree(t, tel)
			shutdownOK(t, tel)
			ms, _ := col.metrics()
			var found bool
			for _, m := range ms {
				if s := m.GetSum(); s != nil {
					found = true
					if s.AggregationTemporality != c.want {
						t.Errorf("temporality = %v, want %v", s.AggregationTemporality, c.want)
					}
				}
			}
			if !found {
				t.Fatal("no sum metric reached the collector")
			}
		})
	}
}

// --- recorded data -------------------------------------------------------

func collectOne(t *testing.T, r *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return rm
}

func TestMetricsCounterCarriesResourceAttributes(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), nil, nil, withMetricReader(reader))
	t.Cleanup(func() { shutdownQuietly(tel) })
	if !tel.Enabled() {
		t.Fatal("a metric reader must make telemetry enabled")
	}
	c, err := tel.Meter("goldie.test").Int64Counter("goldie.test.count")
	if err != nil {
		t.Fatal(err)
	}
	c.Add(context.Background(), 2, metric.WithAttributes(attribute.String("k", "v")))
	c.Add(context.Background(), 3, metric.WithAttributes(attribute.String("k", "v")))

	rm := collectOne(t, reader)
	res := map[string]string{}
	for _, kv := range rm.Resource.Attributes() {
		res[string(kv.Key)] = kv.Value.String()
	}
	if res["service.name"] != "goldie" || res["goldie.mode"] != "all" {
		t.Errorf("resource = %v, want service.name=goldie and goldie.mode=all", res)
	}
	if res["service.version"] != CurrentVersion().Version {
		t.Errorf("service.version = %q", res["service.version"])
	}
	var got int64
	var seen bool
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != "goldie.test" {
			continue
		}
		for _, m := range sm.Metrics {
			if m.Name != "goldie.test.count" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) != 1 {
				t.Fatalf("data = %#v", m.Data)
			}
			seen, got = true, sum.DataPoints[0].Value
		}
	}
	if !seen || got != 5 {
		t.Errorf("counter seen=%v value=%d, want 5", seen, got)
	}
}

func TestMetricsResourceHonoursServiceNameOverride(t *testing.T) {
	col := newMetricCollector(t, http.StatusOK)
	tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(map[string]string{
		genericEndpointEnv:  col.srv.URL,
		"OTEL_SERVICE_NAME": "goldie-staging",
	})))
	if err != nil {
		t.Fatal(err)
	}
	countThree(t, tel)
	shutdownOK(t, tel)
	_, res := col.metrics()
	if res["service.name"] != "goldie-staging" || res["goldie.mode"] != "all" {
		t.Errorf("resource = %v", res)
	}
}

// The provider has an explicit cardinality limit, so a caller-controlled
// attribute cannot grow memory without bound. Overflow is aggregated, not lost.
func TestMetricsCardinalityIsLimited(t *testing.T) {
	// The SDK's own variable says "no limit"; the explicit limit of 2000 must
	// win, so deleting it fails this test. Overflow adds one data point.
	t.Setenv("OTEL_GO_X_CARDINALITY_LIMIT", "0")
	reader := sdkmetric.NewManualReader()
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), nil, nil, withMetricReader(reader))
	t.Cleanup(func() { shutdownQuietly(tel) })
	c, err := tel.Meter("goldie.test").Int64Counter("goldie.test.hostile")
	if err != nil {
		t.Fatal(err)
	}
	const distinct = 10000
	for i := range distinct {
		c.Add(context.Background(), 1, metric.WithAttributes(attribute.String("user_input", fmt.Sprintf("v%d", i))))
	}
	rm := collectOne(t, reader)
	var points int
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok && m.Name == "goldie.test.hostile" {
				points = len(sum.DataPoints)
				for _, dp := range sum.DataPoints {
					total += dp.Value
				}
			}
		}
	}
	if points == 0 || points > 2001 {
		t.Errorf("%d data points for %d distinct attribute sets, want a bounded number (at most 2001)", points, distinct)
	}
	if total != distinct {
		t.Errorf("recorded total = %d, want %d: overflow must be aggregated, not dropped", total, distinct)
	}
}

// --- shutdown and globals ------------------------------------------------

func TestMetricsShutdownFlushesAndSecondCallReturnsFirstResult(t *testing.T) {
	col := newMetricCollector(t, http.StatusOK)
	// The default interval is long: only the Shutdown flush can export.
	tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(map[string]string{genericEndpointEnv: col.srv.URL})))
	if err != nil {
		t.Fatal(err)
	}
	countThree(t, tel)
	if col.hits("/v1/metrics") != 0 {
		t.Fatal("exported before the interval")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first := tel.Shutdown(ctx)
	if first != nil {
		t.Errorf("Shutdown: %v", first)
	}
	ms, _ := col.metrics()
	var total int64
	for _, m := range ms {
		for _, dp := range m.GetSum().GetDataPoints() {
			total += dp.GetAsInt()
		}
	}
	if total != 3 {
		t.Errorf("collector saw a total of %d after Shutdown, want 3 (flush)", total)
	}
	n := col.hits("/v1/metrics")
	if second := tel.Shutdown(ctx); second != first {
		t.Errorf("second Shutdown = %v, want the first result %v", second, first)
	}
	if col.hits("/v1/metrics") != n {
		t.Error("the second Shutdown exported again")
	}
}

func TestMetricsShutdownReturnsTheSameErrorTwice(t *testing.T) {
	col := newMetricCollector(t, http.StatusBadRequest)
	tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(map[string]string{genericEndpointEnv: col.srv.URL})))
	if err != nil {
		t.Fatal(err)
	}
	countThree(t, tel)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first := tel.Shutdown(ctx)
	second := tel.Shutdown(context.Background())
	if (first == nil) != (second == nil) || (first != nil && first.Error() != second.Error()) {
		t.Errorf("first = %v, second = %v", first, second)
	}
}

func TestMetricsInstallNoGlobalMeterProvider(t *testing.T) {
	before := otel.GetMeterProvider()
	col := newMetricCollector(t, http.StatusOK)
	tel, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{genericEndpointEnv: col.srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if otel.GetMeterProvider() != before {
		t.Error("SetupTelemetry installed a global MeterProvider")
	}
	_ = tel.WrapLogger(slog.New(slog.DiscardHandler), "server")
	if otel.GetMeterProvider() != before {
		t.Error("WrapLogger installed a global MeterProvider")
	}
	// A counter made from the global provider must record nowhere.
	g, _ := otel.Meter("global").Int64Counter("goldie.test.global")
	g.Add(context.Background(), 1)
	shutdownOK(t, tel)
	if otel.GetMeterProvider() != before {
		t.Error("Shutdown installed a global MeterProvider")
	}
	ms, _ := col.metrics()
	for _, m := range ms {
		if m.Name == "goldie.test.global" {
			t.Error("global meter data reached the collector")
		}
	}
}

// --- export failures -----------------------------------------------------

// A failing collector is reported at most once a minute, with a fixed text
// that never carries the collector URL or headers (PC-05).
func TestMetricsExportFailureIsReportedOnceWithoutTheCollectorURL(t *testing.T) {
	col := newMetricCollector(t, http.StatusBadRequest)
	tel, err := SetupTelemetry(context.Background(), mapLookup(metricsOnly(map[string]string{
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": col.srv.URL + "/path-marker",
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS":  "x-api-token=header-marker",
		"OTEL_METRIC_EXPORT_INTERVAL":         "20",
	})))
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	tel.WrapLogger(NewLogger(out, 0, "server"), "server")
	c, _ := tel.Meter("goldie.test").Int64Counter("goldie.test.count")
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		c.Add(context.Background(), 1)
		time.Sleep(10 * time.Millisecond)
	}
	waitFor(t, "several failed exports", 5*time.Second, func() bool { return col.hits("/path-marker") >= 3 })
	shutdownQuietly(tel)
	log := out.String()
	if n := strings.Count(log, "OpenTelemetry SDK reported"); n != 1 {
		t.Errorf("%d reports within a minute, want exactly 1: %s", n, log)
	}
	host := strings.TrimPrefix(col.srv.URL, "http://")
	for _, bad := range []string{host, "path-marker", "header-marker", "127.0.0.1"} {
		if strings.Contains(log, bad) {
			t.Errorf("log leaks %q: %s", bad, log)
		}
	}
}
