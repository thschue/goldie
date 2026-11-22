package platform

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// collectorSeen records what an OTLP collector received.
type collectorSeen struct {
	mu     sync.Mutex
	auth   map[string]string // path -> Authorization
	bodies map[string][]byte // path -> request bodies, concatenated
}

func newCollector(t *testing.T) (*httptest.Server, *collectorSeen) {
	t.Helper()
	seen := &collectorSeen{auth: map[string]string{}, bodies: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen.mu.Lock()
		seen.auth[r.URL.Path] = r.Header.Get("Authorization")
		seen.bodies[r.URL.Path] = append(seen.bodies[r.URL.Path], b...)
		seen.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// The SDK reads nothing from the process environment: endpoint, headers,
// sampler and resource all come from lookup. The process environment holds
// conflicting values that would break each assertion if the SDK read them.
func TestTelemetrySettingsComeFromLookupNotTheProcessEnvironment(t *testing.T) {
	collector, seen := newCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=process")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_off")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=from-process,service.version=9.9.9")
	t.Setenv("OTEL_SERVICE_NAME", "svc-from-process")
	t.Setenv("OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT", "1")

	tel, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL,
		"OTEL_EXPORTER_OTLP_HEADERS":  "authorization=from-lookup",
		"OTEL_TRACES_SAMPLER":         "always_on",
		"OTEL_RESOURCE_ATTRIBUTES":    "deployment.environment=env-from-lookup,service.version=8.8.8,goldie.mode=server",
		"OTEL_SERVICE_NAME":           "svc-from-lookup",
	}))
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	app, _, stop := startTraced(t, tel, &syncBuffer{})
	get(t, "http://"+app+"/x")
	stop()
	seen.mu.Lock()
	defer seen.mu.Unlock()
	body := string(seen.bodies["/v1/traces"])
	if body == "" {
		t.Fatalf("no spans reached the lookup endpoint (paths: %v)", seen.auth)
	}
	if seen.auth["/v1/traces"] != "from-lookup" {
		t.Errorf("Authorization = %q, want from-lookup", seen.auth["/v1/traces"])
	}
	for _, want := range []string{"env-from-lookup", "svc-from-lookup", CurrentVersion().Version} {
		if !strings.Contains(body, want) {
			t.Errorf("resource lacks %q", want)
		}
	}
	// OTEL_RESOURCE_ATTRIBUTES cannot override service.version or goldie.mode.
	for _, bad := range []string{"from-process", "9.9.9", "8.8.8"} {
		if strings.Contains(body, bad) {
			t.Errorf("resource carries %q", bad)
		}
	}
	if i := strings.Index(body, "goldie.mode"); i < 0 || !strings.HasPrefix(strings.TrimLeft(body[i+len("goldie.mode"):], "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12"), "all") {
		t.Error("goldie.mode is not the fixed value \"all\"")
	}
}

// A sampler in lookup decides, even when the process environment says the
// opposite.
func TestTelemetrySamplerComesFromLookup(t *testing.T) {
	collector, seen := newCollector(t)
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	tel, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL,
		"OTEL_LOGS_EXPORTER":          "none",
		"OTEL_TRACES_SAMPLER":         "always_off",
	}))
	if err != nil {
		t.Fatal(err)
	}
	app, _, stop := startTraced(t, tel, &syncBuffer{})
	get(t, "http://"+app+"/x")
	stop()
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if n := len(seen.bodies["/v1/traces"]); n != 0 {
		t.Errorf("always_off in lookup still exported %d bytes of spans", n)
	}
}

func TestParseTraceSettings(t *testing.T) {
	lk := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	ts, err := parseTraceSettings(lk(nil))
	if err != nil || ts.sampler.Description() != sdktrace.ParentBased(sdktrace.AlwaysSample()).Description() ||
		ts.limits.AttributeValueLengthLimit != defaultSpanAttributeValueLimit || ts.limits.AttributeCountLimit != 128 {
		t.Fatalf("defaults: %+v err=%v", ts, err)
	}
	ts, err = parseTraceSettings(lk(map[string]string{
		"OTEL_TRACES_SAMPLER": "parentbased_traceidratio", "OTEL_TRACES_SAMPLER_ARG": "0.25",
		"OTEL_SPAN_EVENT_COUNT_LIMIT": "3", "OTEL_ATTRIBUTE_COUNT_LIMIT": "7",
	}))
	if err != nil || !strings.Contains(ts.sampler.Description(), "0.25") ||
		ts.limits.EventCountLimit != 3 || ts.limits.AttributeCountLimit != 7 {
		t.Errorf("parsed: %v %+v err=%v", ts.sampler.Description(), ts.limits, err)
	}
}

// Invalid sampler and limit values fail startup naming the variable only.
func TestTelemetryRejectsBadSamplerAndLimits(t *testing.T) {
	const secret = "hunter2"
	for _, c := range [][2]string{
		{"OTEL_TRACES_SAMPLER", secret},
		{"OTEL_TRACES_SAMPLER_ARG", secret},
		{"OTEL_TRACES_SAMPLER_ARG", "1.5"},
		{"OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT", secret},
		{"OTEL_SPAN_LINK_COUNT_LIMIT", "-1"},
	} {
		env := map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", c[0]: c[1]}
		if c[0] == "OTEL_TRACES_SAMPLER_ARG" {
			env["OTEL_TRACES_SAMPLER"] = "traceidratio"
		}
		_, err := SetupTelemetry(context.Background(), mapLookup(env))
		if err == nil || !strings.Contains(err.Error(), c[0]) || strings.Contains(err.Error(), secret) {
			t.Errorf("%s=%q: err = %v", c[0], c[1], err)
		}
	}
}
