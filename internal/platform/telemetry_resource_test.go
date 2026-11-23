package platform

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// The resource of all three providers comes from the injected lookup only
// (#222, PC-04): OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES in the
// process environment, which the SDK's resource.Environment() reads, have no
// effect when the lookup does not carry them.

// resourceCollector is an OTLP collector that keeps the resource attributes
// of the last trace, log and metric export it received, by signal.
type resourceCollector struct {
	srv *httptest.Server
	mu  sync.Mutex
	res map[string]map[string]string // signal -> attribute -> string value
}

func newResourceCollector(t *testing.T) *resourceCollector {
	t.Helper()
	c := &resourceCollector{res: map[string]map[string]string{}}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var signal string
		var rs []*resourcepb.Resource
		switch r.URL.Path {
		case "/v1/traces":
			var req coltrace.ExportTraceServiceRequest
			if proto.Unmarshal(b, &req) == nil {
				signal = "traces"
				for _, x := range req.ResourceSpans {
					rs = append(rs, x.GetResource())
				}
			}
		case "/v1/logs":
			var req collogs.ExportLogsServiceRequest
			if proto.Unmarshal(b, &req) == nil {
				signal = "logs"
				for _, x := range req.ResourceLogs {
					rs = append(rs, x.GetResource())
				}
			}
		case "/v1/metrics":
			var req collmetrics.ExportMetricsServiceRequest
			if proto.Unmarshal(b, &req) == nil {
				signal = "metrics"
				for _, x := range req.ResourceMetrics {
					rs = append(rs, x.GetResource())
				}
			}
		}
		c.mu.Lock()
		for _, r := range rs {
			m := map[string]string{}
			for _, kv := range r.GetAttributes() {
				m[kv.Key] = kv.Value.GetStringValue()
			}
			c.res[signal] = m
		}
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

// exportOnAllSignals sets telemetry up against the collector with lookup
// values, makes one request (a span and log records) and one counter
// increment, drains, and returns the resource each signal arrived with.
func (c *resourceCollector) exportOnAllSignals(t *testing.T, env map[string]string) map[string]map[string]string {
	t.Helper()
	env["OTEL_EXPORTER_OTLP_ENDPOINT"] = c.srv.URL
	tel, err := SetupTelemetry(context.Background(), mapLookup(env))
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	app, _, stop := startTraced(t, tel, &syncBuffer{})
	get(t, "http://"+app+"/x")
	countThree(t, tel)
	stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]map[string]string{}
	for _, s := range []string{"traces", "logs", "metrics"} {
		if c.res[s] == nil {
			t.Fatalf("no %s export reached the collector", s)
		}
		out[s] = c.res[s]
	}
	return out
}

func TestTelemetryServiceNameAndResourceAttributesComeFromTheLookupOnAllSignals(t *testing.T) {
	// The process environment says something else for the same names.
	t.Setenv("OTEL_SERVICE_NAME", "from-process")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=process,env.only=1")
	got := newResourceCollector(t).exportOnAllSignals(t, map[string]string{
		"OTEL_SERVICE_NAME":        "from-lookup",
		"OTEL_RESOURCE_ATTRIBUTES": "deployment.environment=lookup,team=platform",
	})
	for signal, res := range got {
		if res["service.name"] != "from-lookup" {
			t.Errorf("%s: service.name = %q, want from-lookup", signal, res["service.name"])
		}
		if res["deployment.environment"] != "lookup" || res["team"] != "platform" {
			t.Errorf("%s: resource = %v, want deployment.environment=lookup and team=platform", signal, res)
		}
		if _, ok := res["env.only"]; ok {
			t.Errorf("%s: resource %v carries an attribute only the process environment set", signal, res)
		}
	}
}

func TestTelemetryProcessEnvironmentAloneHasNoEffectOnTheResource(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "from-process")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=also-process,env.only=1,deployment.environment=process")
	got := newResourceCollector(t).exportOnAllSignals(t, map[string]string{})
	for signal, res := range got {
		if res["service.name"] != defaultServiceName {
			t.Errorf("%s: service.name = %q, want the default %q", signal, res["service.name"], defaultServiceName)
		}
		for _, k := range []string{"env.only", "deployment.environment"} {
			if v, ok := res[k]; ok {
				t.Errorf("%s: resource attribute %s=%q came from the process environment, want none (PC-04)", signal, k, v)
			}
		}
	}
}

// The same holds when the providers are built directly around a resource: it
// is the only source of attributes, whatever the process environment holds.
func TestTelemetryProvidersIgnoreTheProcessEnvironmentForTheResource(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "env.only=1")
	spans, logs := newSpanSink(), &logSink{}
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), spans, logs)
	_, sp := tel.tp.Tracer("t").Start(context.Background(), "s")
	sp.End()
	shutdownOK(t, tel)
	got := spans.spans()
	if len(got) != 1 {
		t.Fatalf("%d spans, want 1", len(got))
	}
	for _, kv := range got[0].Resource.Attributes() {
		if string(kv.Key) == "env.only" {
			t.Errorf("span resource carries env.only from the process environment: %v", got[0].Resource.Attributes())
		}
	}
}

// The OTEL_EXPORTER_OTLP_ENDPOINT setting says a generic endpoint also turns
// metrics on and how to opt out; the committed reference carries the text.
func TestOTLPEndpointSettingSaysItTurnsMetricsOnAndHowToOptOut(t *testing.T) {
	var meaning string
	for _, s := range TelemetrySettings {
		if s.Name == "OTEL_EXPORTER_OTLP_ENDPOINT" {
			meaning = s.Meaning
		}
	}
	if meaning == "" {
		t.Fatal("OTEL_EXPORTER_OTLP_ENDPOINT is not in TelemetrySettings")
	}
	// The sentence must be about the generic endpoint and metrics being on
	// by default, and name the opt-out; matching on the pieces keeps the
	// wording free.
	wants := []string{"OTEL_METRICS_EXPORTER=none", "opt"}
	for _, want := range wants {
		if !strings.Contains(meaning, want) {
			t.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT description %q does not state %q", meaning, want)
		}
	}
	if lower := strings.ToLower(meaning); !strings.Contains(lower, "generic") || !strings.Contains(lower, "also") {
		t.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT description %q must say a generic endpoint also turns metrics on", meaning)
	}
	committed, err := os.ReadFile("../../docs/reference/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range wants {
		if !strings.Contains(string(committed), want) {
			t.Errorf("docs/reference/configuration.md does not state %q (run: ./run regen)", want)
		}
	}
}
