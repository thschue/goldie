package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
)

// OTEL_* variables the SDK parses itself (timeouts, TLS paths, compression)
// may be rejected by it, and it prints the value. The SDK's logger goes
// through goldie's: the value reaches neither stderr nor the log, and a fixed
// warning names the problem instead (PC-05). Every case must end in one of two
// outcomes: SetupTelemetry fails naming the variable, or the fixed warning is
// logged. Silence is a failure.
//
// This test must never run in parallel: it uses t.Setenv, replaces os.Stderr
// and sets the process-wide otel error handler and logger.
func TestTelemetrySDKRejectedValueNeverReachesStderrOrLog(t *testing.T) {
	const secret = "s3cr3t-marker"
	cases := map[string]string{
		"OTEL_EXPORTER_OTLP_TIMEOUT":          "soon" + secret,
		"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT":   "soon" + secret,
		"OTEL_EXPORTER_OTLP_LOGS_COMPRESSION": "zip" + secret,
		"OTEL_EXPORTER_OTLP_CERTIFICATE":      "/no/such/" + secret + ".pem",
		"OTEL_EXPORTER_OTLP_CLIENT_KEY":       "/no/such/" + secret + ".key",
	}
	// The SDK reads a client key only together with a client certificate.
	companions := map[string]map[string]string{
		"OTEL_EXPORTER_OTLP_CLIENT_KEY": {"OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE": "/no/such/cert.pem"},
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
			t.Setenv(name, v)
			for k, cv := range companions[name] {
				t.Setenv(k, cv)
			}
			var logged string
			failed := false
			stderr := captureStderr(t, func() {
				tel, err := SetupTelemetry(context.Background(), os.LookupEnv)
				if err != nil {
					failed = true
					// goldie refusing the value, naming the variable only, is fine.
					if strings.Contains(err.Error(), secret) {
						t.Errorf("error echoes the value: %v", err)
					}
					if !strings.Contains(err.Error(), name) {
						t.Errorf("error does not name %s: %v", name, err)
					}
					return
				}
				out := &syncBuffer{}
				tel.WrapLogger(NewLogger(out, 0, "server"), "server")
				_ = tel.Shutdown(context.Background())
				logged = out.String()
			})
			if strings.Contains(stderr, secret) {
				t.Errorf("value reached stderr: %s", stderr)
			}
			if !failed && !strings.Contains(logged, "OpenTelemetry SDK reported") {
				t.Errorf("the SDK rejection was not reported: %q", logged)
			}
			if strings.Contains(logged, secret) {
				t.Errorf("log echoes the value: %s", logged)
			}
		})
	}
}

// captureStderr runs f with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	func() {
		defer func() { os.Stderr = prev; _ = w.Close() }()
		f()
	}()
	return <-done
}

// A report the SDK makes before a logger exists is delivered once one is
// attached, without its text.
func TestTelemetrySDKReportBeforeLoggerIsDeliveredOnWrap(t *testing.T) {
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), newSpanSink(), nil)
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	tel.notice.install()
	otel.Handle(errors.New("secret-marker"))
	out := &syncBuffer{}
	tel.WrapLogger(NewLogger(out, 0, "server"), "server")
	if got := out.String(); !strings.Contains(got, "OpenTelemetry SDK reported") || strings.Contains(got, "secret-marker") {
		t.Errorf("log = %s", got)
	}
}

// The startup report lists the signals, the protocol and each collector as
// scheme://host: never headers, userinfo, path or query (PC-05, PC-06).
func TestTelemetryReportRedactsEndpoints(t *testing.T) {
	cases := map[string]struct {
		env     map[string]string
		signals string
		want    string
	}{
		"generic endpoint with credentials": {
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "https://user:pw-marker@collector.example:4318/tenant-marker?token=q-marker",
				"OTEL_EXPORTER_OTLP_HEADERS":  "authorization=Bearer%20hdr-marker",
				"OTEL_METRICS_EXPORTER":       "none",
			},
			signals: "traces,logs",
			want:    "https://collector.example:4318",
		},
		"logs only": {
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://u:pw-marker@logs.example/x?k=q-marker",
				"OTEL_TRACES_EXPORTER":             "none",
			},
			signals: "logs",
			want:    "http://logs.example",
		},
		"default endpoint": {
			env:     map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_LOGS_EXPORTER": "none"},
			signals: "traces",
			want:    "https://localhost:4318",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tel, err := SetupTelemetry(context.Background(), mapLookup(c.env))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
			out := &syncBuffer{}
			tel.Report(NewLogger(out, 0, "server"))
			var rec map[string]any
			if err := json.Unmarshal([]byte(out.String()), &rec); err != nil {
				t.Fatalf("log is not one JSON line: %v: %s", err, out.String())
			}
			if rec["msg"] != "telemetry enabled" || rec["signals"] != c.signals || rec["protocol"] != "http/protobuf" {
				t.Errorf("record = %v", rec)
			}
			if !strings.Contains(out.String(), c.want+`"`) {
				t.Errorf("log lacks endpoint %s: %s", c.want, out.String())
			}
			for _, bad := range []string{"pw-marker", "tenant-marker", "q-marker", "hdr-marker", "user:", "/x", "token"} {
				if strings.Contains(out.String(), bad) {
					t.Errorf("log leaks %q: %s", bad, out.String())
				}
			}
		})
	}
}

func TestTelemetryReportIsSilentWhenDisabled(t *testing.T) {
	tel, err := SetupTelemetry(context.Background(), mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	tel.Report(NewLogger(out, 0, "server"))
	(*Telemetry)(nil).Report(NewLogger(out, 0, "server"))
	if out.String() != "" {
		t.Errorf("log = %s", out.String())
	}
}

// The log bridge carries component and version once, from the same source as
// the stdout handler.
func TestIdentityAttrsMatchNewLogger(t *testing.T) {
	out := &syncBuffer{}
	NewLogger(out, 0, "server").Info("x")
	for _, a := range identityAttrs("server") {
		if !strings.Contains(out.String(), `"`+a.Key+`":"`+a.Value.String()+`"`) {
			t.Errorf("stdout lacks %s: %s", a.Key, out.String())
		}
	}
}
