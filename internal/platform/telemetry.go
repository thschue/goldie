package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Telemetry is the OpenTelemetry setup: server traces on the public listener
// and a bridge that copies log records to OTLP. It is configured by the
// standard OTEL_* variables and is a no-op when none selects an exporter. All
// methods are safe on a nil or disabled Telemetry.
//
// Only OTLP over http/protobuf is built in: it is the SDK default, needs no
// separate gRPC exporter modules, and works through plain HTTP proxies.
type Telemetry struct {
	tp *sdktrace.TracerProvider
	lp *sdklog.LoggerProvider

	once sync.Once
	err  error
}

// telemetryResource returns the the resource attributes every signal carries.
// goldie.mode is "all" until server roles are split (#24).
func telemetryResource() *resource.Resource {
	base := resource.NewSchemaless(
		attribute.String("service.name", "goldie"),
		attribute.String("service.version", CurrentVersion().Version),
		attribute.String("goldie.mode", "all"),
	)
	// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES may override the defaults.
	if merged, err := resource.Merge(base, resource.Environment()); err == nil {
		return merged
	}
	return base
}

// defaultSpanAttributeValueLimit caps each span attribute value when
// OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT is unset (the SDK default is
// unlimited). Server spans copy caller-controlled request data, and queued
// spans stay in memory until the collector accepts them.
const defaultSpanAttributeValueLimit = 1024

// newTelemetry wires the providers around the given exporters. Batch
// processors keep export off the request path; Shutdown flushes them.
func newTelemetry(res *resource.Resource, spans sdktrace.SpanExporter, logs sdklog.Exporter) *Telemetry {
	t := &Telemetry{}
	if spans != nil {
		limits := sdktrace.NewSpanLimits() // honours the OTEL_SPAN_* limit variables
		if limits.AttributeValueLengthLimit < 0 {
			limits.AttributeValueLengthLimit = defaultSpanAttributeValueLimit
		}
		t.tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(spans),
			sdktrace.WithRawSpanLimits(limits))
	}
	if logs != nil {
		t.lp = sdklog.NewLoggerProvider(sdklog.WithResource(res),
			sdklog.WithProcessor(sdklog.NewBatchProcessor(logs)))
	}
	return t
}

// Signal selection and validation names (standard OpenTelemetry variables).
const (
	envOTelDisabled = "OTEL_SDK_DISABLED"
	envOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPProtocol = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envOTLPHeaders  = "OTEL_EXPORTER_OTLP_HEADERS"
)

// SetupTelemetry reads the OTEL_* variables and builds the exporters. It
// returns a disabled Telemetry when nothing selects one, and an error naming
// the variable (never its value, PC-04, PC-05) when a value is invalid.
func SetupTelemetry(ctx context.Context, lookup Lookup) (*Telemetry, error) {
	var errs []error
	get := func(name string) string {
		v, err := Setting(lookup, os.ReadFile, name)
		if err != nil {
			errs = append(errs, err)
		}
		return strings.TrimSpace(v)
	}
	if v := strings.ToLower(get(envOTelDisabled)); v != "" && v != "true" && v != "false" {
		errs = append(errs, fmt.Errorf("%s: must be true or false", envOTelDisabled))
	} else if v == "true" {
		return &Telemetry{}, errors.Join(errs...)
	}
	type signal struct{ name, exporter, prefix string }
	signals := []signal{
		{"traces", "OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_"},
		{"logs", "OTEL_LOGS_EXPORTER", "OTEL_EXPORTER_OTLP_LOGS_"},
	}
	on := map[string]bool{}
	headers := map[string]map[string]string{}
	for _, s := range signals {
		exp := strings.ToLower(get(s.exporter))
		if exp != "" && exp != "otlp" && exp != "none" {
			errs = append(errs, fmt.Errorf("%s: must be otlp or none", s.exporter))
		}
		for _, name := range []string{envOTLPProtocol, s.prefix + "PROTOCOL"} {
			if p := get(name); p != "" && p != "http/protobuf" {
				errs = append(errs, fmt.Errorf("%s: only http/protobuf is supported", name))
			}
		}
		endpoint := false
		for _, name := range []string{envOTLPEndpoint, s.prefix + "ENDPOINT"} {
			if v := get(name); v != "" {
				endpoint = true
				if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					errs = append(errs, fmt.Errorf("%s: must be an http or https URL", name))
				}
			}
		}
		on[s.name] = exp == "otlp" || (exp == "" && endpoint)
		for _, name := range []string{s.prefix + "HEADERS", envOTLPHeaders} { // signal-specific wins
			if v := get(name); v != "" {
				h, err := parseOTLPHeaders(v)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", name, err))
				}
				headers[s.name] = h
				break
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if !on["traces"] && !on["logs"] {
		return &Telemetry{}, nil
	}

	var (
		spans sdktrace.SpanExporter
		logs  sdklog.Exporter
		err   error
	)
	if on["traces"] {
		opts := []otlptracehttp.Option{}
		if h := headers["traces"]; h != nil {
			opts = append(opts, otlptracehttp.WithHeaders(h))
		}
		if spans, err = otlptracehttp.New(ctx, opts...); err != nil {
			return nil, errors.New("OTEL_EXPORTER_OTLP_TRACES_*: cannot configure the trace exporter")
		}
	}
	if on["logs"] {
		opts := []otlploghttp.Option{}
		if h := headers["logs"]; h != nil {
			opts = append(opts, otlploghttp.WithHeaders(h))
		}
		if logs, err = otlploghttp.New(ctx, opts...); err != nil {
			return nil, errors.New("OTEL_EXPORTER_OTLP_LOGS_*: cannot configure the log exporter")
		}
	}
	return newTelemetry(telemetryResource(), spans, logs), nil
}

// parseOTLPHeaders parses "k=v,k2=v2" with percent-encoded values, as the
// OpenTelemetry spec and the SDK define it. The error never contains the text.
// A malformed escape is an error here: left to the SDK's own env parser, it
// would print the raw value to stderr.
func parseOTLPHeaders(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, errors.New("must be comma-separated key=value pairs")
		}
		dv, err := url.PathUnescape(v)
		if err != nil {
			return nil, errors.New("values must be percent-encoded")
		}
		out[k] = strings.TrimSpace(dv)
	}
	return out, nil
}

// Enabled reports whether any signal is exported.
func (t *Telemetry) Enabled() bool { return t != nil && (t.tp != nil || t.lp != nil) }

// ReportErrorsTo routes the SDK's export errors to the logger. The errors
// themselves are not logged: they carry the collector URL, which can hold
// credentials (PC-05). Reports are limited to one a minute.
func (t *Telemetry) ReportErrorsTo(l *slog.Logger) {
	if !t.Enabled() {
		return
	}
	var mu sync.Mutex
	var last time.Time
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) < time.Minute {
			return
		}
		last = time.Now()
		l.Warn("telemetry export failed; check the OTEL_EXPORTER_OTLP_* settings and the collector")
	}))
}

// spanName is the HTTP method only: the path is unbounded, and routes belong
// to the handlers behind this wrap. Unknown methods collapse to HTTP.
func spanName(_ string, r *http.Request) string {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return r.Method
	}
	return "HTTP"
}

// WrapHandler traces h as a server span per request. otelhttp records no
// headers and no query string by default, and the public endpoint option
// links, rather than parents, to a caller's trace context.
func (t *Telemetry) WrapHandler(h http.Handler) http.Handler {
	if t == nil || t.tp == nil {
		return h
	}
	return otelhttp.NewHandler(h, "goldie",
		otelhttp.WithTracerProvider(t.tp),
		otelhttp.WithPropagators(propagation.TraceContext{}),
		otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return true }),
		otelhttp.WithSpanNameFormatter(spanName),
	)
}

// WrapLogger adds the OTel log bridge next to l's own handler, so stdout
// keeps the PC-07 shape and records also flow to OTLP.
func (t *Telemetry) WrapLogger(l *slog.Logger, component string) *slog.Logger {
	if t == nil || t.lp == nil {
		return l
	}
	bridge := otelslog.NewHandler("goldie", otelslog.WithLoggerProvider(t.lp)).
		WithAttrs([]slog.Attr{slog.String("component", component), slog.String("version", Version)})
	return slog.New(slog.NewMultiHandler(l.Handler(), bridge))
}

// Shutdown flushes and stops the exporters. It is safe to call twice; later
// calls return the first result.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	t.once.Do(func() {
		var errs []error
		if t.tp != nil {
			errs = append(errs, t.tp.Shutdown(ctx))
		}
		if t.lp != nil {
			errs = append(errs, t.lp.Shutdown(ctx))
		}
		t.err = errors.Join(errs...)
	})
	return t.err
}
