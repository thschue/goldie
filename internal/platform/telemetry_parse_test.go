package platform

import (
	"strings"
	"testing"
)

// TestParseTelemetrySettings covers the parse seam: no exporter is built.
func TestParseTelemetrySettings(t *testing.T) {
	type sig struct {
		on       bool
		endpoint string
		headers  string // "k=v" of the first header, "" for none
	}
	tests := []struct {
		name     string
		role     string
		env      map[string]string
		wantErrs []string // each must appear in the error; none for a clean parse
		disabled bool
		client   bool
		service  string
		signals  map[string]sig
	}{
		{name: "empty selects nothing", role: RoleServer,
			signals: map[string]sig{"traces": {}, "logs": {}, "metrics": {}}},
		{name: "generic endpoint gets the signal path", role: RoleServer,
			env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318/base"},
			signals: map[string]sig{
				"traces":  {on: true, endpoint: "http://c:4318/base/v1/traces"},
				"logs":    {on: true, endpoint: "http://c:4318/base/v1/logs"},
				"metrics": {on: true, endpoint: "http://c:4318/base/v1/metrics"},
			}},
		{name: "signal endpoint wins and is used as-is", role: RoleServer,
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT":        "http://c:4318",
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://t:1/x",
				"OTEL_LOGS_EXPORTER":                 "none",
			},
			signals: map[string]sig{
				"traces":  {on: true, endpoint: "http://t:1/x"},
				"logs":    {endpoint: "http://c:4318/v1/logs"},
				"metrics": {on: true, endpoint: "http://c:4318/v1/metrics"},
			}},
		{name: "exporter otlp without an endpoint uses the default", role: RoleServer,
			env:     map[string]string{"OTEL_TRACES_EXPORTER": "otlp"},
			signals: map[string]sig{"traces": {on: true}, "logs": {}, "metrics": {}}},
		{name: "signal headers win over generic", role: RoleServer,
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_HEADERS":      "a=1",
				"OTEL_EXPORTER_OTLP_LOGS_HEADERS": "b=2",
			},
			signals: map[string]sig{"traces": {headers: "a=1"}, "logs": {headers: "b=2"}, "metrics": {headers: "a=1"}}},
		{name: "service name and disabled false", role: RoleServer,
			env:     map[string]string{"OTEL_SERVICE_NAME": " svc ", "OTEL_SDK_DISABLED": "FALSE"},
			service: "svc",
			signals: map[string]sig{"traces": {}, "logs": {}, "metrics": {}}},
		{name: "disabled stops the parse", role: RoleServer,
			env:      map[string]string{"OTEL_SDK_DISABLED": "true", "OTEL_TRACES_EXPORTER": "bogus"},
			disabled: true},
		{name: "a client role reads traces only", role: RoleCLI,
			env:     map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318", "OTEL_METRICS_EXPORTER": "bogus"},
			signals: map[string]sig{"traces": {on: true, endpoint: "http://c:4318/v1/traces"}}},
		{name: "every error is joined", role: RoleServer,
			env: map[string]string{
				"OTEL_SDK_DISABLED":            "maybe",
				"OTEL_RESOURCE_ATTRIBUTES":     "nokey",
				"OTEL_TRACES_SAMPLER":          "nope",
				"OTEL_METRIC_EXPORT_INTERVAL":  "-1",
				"OTEL_TRACES_EXPORTER":         "jaeger",
				"OTEL_EXPORTER_OTLP_PROTOCOL":  "grpc",
				"OTEL_EXPORTER_OTLP_ENDPOINT":  "ftp://c",
				"OTEL_EXPORTER_OTLP_HEADERS":   "bad header=1",
				"OTEL_SERVICE_NAME_FILE":       "/x",
				"OTEL_LOGS_EXPORTER_FILE":      "/y",
				"OTEL_EXPORTER_OTLP_LOGS_FILE": "/z",
			},
			wantErrs: []string{
				"OTEL_SDK_DISABLED: must be true or false",
				"OTEL_RESOURCE_ATTRIBUTES",
				"OTEL_TRACES_SAMPLER: must be",
				"OTEL_METRIC_EXPORT_INTERVAL",
				"OTEL_TRACES_EXPORTER: must be otlp or none",
				"OTEL_EXPORTER_OTLP_PROTOCOL: only http/protobuf",
				"OTEL_EXPORTER_OTLP_ENDPOINT: must be an http or https URL",
				"OTEL_EXPORTER_OTLP_HEADERS",
				"OTEL_SERVICE_NAME_FILE is not supported",
				"OTEL_LOGS_EXPORTER_FILE is not supported",
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := parseTelemetrySettings(mapLookup(tc.env), tc.role)
			if len(tc.wantErrs) > 0 {
				if err == nil {
					t.Fatal("want an error, got none")
				}
				for _, w := range tc.wantErrs {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("error lacks %q: %v", w, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s.disabled != tc.disabled {
				t.Errorf("disabled = %v, want %v", s.disabled, tc.disabled)
			}
			if s.serviceName != tc.service {
				t.Errorf("serviceName = %q, want %q", s.serviceName, tc.service)
			}
			if len(s.signals) != len(tc.signals) {
				t.Fatalf("signals = %d, want %d", len(s.signals), len(tc.signals))
			}
			for name, want := range tc.signals {
				got := s.signal(name)
				if got.on != want.on || got.endpoint != want.endpoint {
					t.Errorf("%s = on %v endpoint %q, want on %v endpoint %q", name, got.on, got.endpoint, want.on, want.endpoint)
				}
				var h string
				for k, v := range got.headers {
					h = k + "=" + v
				}
				if h != want.headers {
					t.Errorf("%s headers = %q, want %q", name, h, want.headers)
				}
			}
		})
	}
}
