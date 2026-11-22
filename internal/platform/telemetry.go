package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

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

// telemetryResource returns the resource attributes every signal carries.
// extra holds the parsed OTEL_RESOURCE_ATTRIBUTES and serviceName the
// OTEL_SERVICE_NAME value (empty when unset). goldie owns service.version and
// goldie.mode, so extra cannot override them; service.name may be overridden.
// goldie.mode is "all" until server roles are split (#24).
func telemetryResource(serviceName string, extra map[string]string) *resource.Resource {
	name := "goldie"
	if v := extra["service.name"]; v != "" {
		name = v
	}
	if serviceName != "" {
		name = serviceName
	}
	attrs := make([]attribute.KeyValue, 0, len(extra)+3)
	for k, v := range extra {
		switch k {
		case "service.name", "service.version", "goldie.mode":
		default:
			attrs = append(attrs, attribute.String(k, v))
		}
	}
	attrs = append(attrs,
		attribute.String("service.name", name),
		attribute.String("service.version", CurrentVersion().Version),
		attribute.String("goldie.mode", "all"),
	)
	return resource.NewSchemaless(attrs...)
}

// defaultSpanAttributeValueLimit caps each span attribute value when
// OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT is unset (the SDK default is
// unlimited). Server spans copy caller-controlled request data, and queued
// spans stay in memory until the collector accepts them.
const defaultSpanAttributeValueLimit = 1024

// traceSettings are the trace sampler and span limits, parsed from the
// injected lookup and handed to the SDK explicitly, so it reads neither from
// the process environment.
type traceSettings struct {
	sampler sdktrace.Sampler
	limits  sdktrace.SpanLimits
}

// The span limit variables, each with its SDK fallback where one exists.
const (
	envSpanAttrValueLen = "OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT"
	envAttrValueLen     = "OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT"
	envSpanAttrCount    = "OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT"
	envAttrCount        = "OTEL_ATTRIBUTE_COUNT_LIMIT"
	envSpanEventCount   = "OTEL_SPAN_EVENT_COUNT_LIMIT"
	envSpanLinkCount    = "OTEL_SPAN_LINK_COUNT_LIMIT"
	envEventAttrCount   = "OTEL_EVENT_ATTRIBUTE_COUNT_LIMIT"
	envLinkAttrCount    = "OTEL_LINK_ATTRIBUTE_COUNT_LIMIT"
)

// parseTraceSettings reads OTEL_TRACES_SAMPLER{,_ARG} and the span limit
// variables through get. Errors name the variable, never the value.
func parseTraceSettings(get func(string) string) (traceSettings, error) {
	var errs []error
	// The SDK's defaults, not NewSpanLimits, which reads the process
	// environment: only lookup decides. The value length keeps goldie's cap.
	ts := traceSettings{limits: sdktrace.SpanLimits{
		AttributeValueLengthLimit:   defaultSpanAttributeValueLimit,
		AttributeValueDepthLimit:    sdktrace.DefaultAttributeValueDepthLimit,
		AttributeCountLimit:         sdktrace.DefaultAttributeCountLimit,
		EventCountLimit:             sdktrace.DefaultEventCountLimit,
		LinkCountLimit:              sdktrace.DefaultLinkCountLimit,
		AttributePerEventCountLimit: sdktrace.DefaultAttributePerEventCountLimit,
		AttributePerLinkCountLimit:  sdktrace.DefaultAttributePerLinkCountLimit,
	}}
	limit := func(dst *int, names ...string) {
		for _, name := range names {
			v := get(name)
			if v == "" {
				continue
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				errs = append(errs, fmt.Errorf("%s: must be a non-negative integer", name))
			} else {
				*dst = n
			}
			return
		}
	}
	limit(&ts.limits.AttributeValueLengthLimit, envSpanAttrValueLen, envAttrValueLen)
	limit(&ts.limits.AttributeCountLimit, envSpanAttrCount, envAttrCount)
	limit(&ts.limits.EventCountLimit, envSpanEventCount)
	limit(&ts.limits.LinkCountLimit, envSpanLinkCount)
	limit(&ts.limits.AttributePerEventCountLimit, envEventAttrCount)
	limit(&ts.limits.AttributePerLinkCountLimit, envLinkAttrCount)

	arg := get(envOTelSamplerArg)
	ratio := func() (float64, bool) {
		if arg == "" {
			return 1, true
		}
		f, err := strconv.ParseFloat(arg, 64)
		// NaN compares false against both bounds, so it is checked itself.
		if err != nil || math.IsNaN(f) || f < 0 || f > 1 {
			errs = append(errs, fmt.Errorf("%s: must be a number from 0 to 1", envOTelSamplerArg))
			return 0, false
		}
		return f, true
	}
	switch strings.ToLower(get(envOTelSampler)) {
	case "", "parentbased_always_on":
		ts.sampler = sdktrace.ParentBased(sdktrace.AlwaysSample())
	case "always_on":
		ts.sampler = sdktrace.AlwaysSample()
	case "always_off":
		ts.sampler = sdktrace.NeverSample()
	case "parentbased_always_off":
		ts.sampler = sdktrace.ParentBased(sdktrace.NeverSample())
	case "traceidratio":
		if f, ok := ratio(); ok {
			ts.sampler = sdktrace.TraceIDRatioBased(f)
		}
	case "parentbased_traceidratio":
		if f, ok := ratio(); ok {
			ts.sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(f))
		}
	default:
		errs = append(errs, fmt.Errorf("%s: must be always_on, always_off, traceidratio or a parentbased_ form of them", envOTelSampler))
	}
	return ts, errors.Join(errs...)
}

// newTelemetry wires the providers around the given exporters. Batch
// processors keep export off the request path; Shutdown flushes them.
func newTelemetry(res *resource.Resource, ts traceSettings, spans sdktrace.SpanExporter, logs sdklog.Exporter) *Telemetry {
	t := &Telemetry{}
	if spans != nil {
		t.tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(spans),
			sdktrace.WithSampler(ts.sampler), sdktrace.WithRawSpanLimits(ts.limits))
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
	envOTelService  = "OTEL_SERVICE_NAME"
	envOTelResAttrs = "OTEL_RESOURCE_ATTRIBUTES"

	// defaultOTLPEndpoint is the OTLP/HTTP default when no endpoint is set.
	defaultOTLPEndpoint = "http://localhost:4318"

	envOTelSampler    = "OTEL_TRACES_SAMPLER"
	envOTelSamplerArg = "OTEL_TRACES_SAMPLER_ARG"
)

// secretOTelNames are the variables with a _FILE form (PC-05).
var secretOTelNames = map[string]bool{
	envOTLPHeaders:                      true,
	"OTEL_EXPORTER_OTLP_TRACES_HEADERS": true,
	"OTEL_EXPORTER_OTLP_LOGS_HEADERS":   true,
}

// SetupTelemetry reads the OTEL_* variables and builds the exporters. It
// returns a disabled Telemetry when nothing selects one, and an error naming
// the variable (never its value, PC-04, PC-05) when a value is invalid.
func SetupTelemetry(ctx context.Context, lookup Lookup) (*Telemetry, error) {
	var errs []error
	get := func(name string) string {
		var v string
		var err error
		if secretOTelNames[name] {
			v, err = Setting(lookup, os.ReadFile, name)
		} else {
			// The SDK reads these names from the environment itself and
			// would ignore a _FILE form, so refuse it instead of dropping it.
			if _, ok := lookup(name + "_FILE"); ok {
				err = fmt.Errorf("%s_FILE is not supported: only the OTLP headers variables accept a _FILE form", name)
			}
			v, _ = lookup(name)
		}
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
	resAttrs, err := parseResourceAttributes(get(envOTelResAttrs))
	if err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", envOTelResAttrs, err))
	}
	serviceName := get(envOTelService)
	traceCfg, err := parseTraceSettings(get)
	if err != nil {
		errs = append(errs, err)
	}
	type signal struct{ name, exporter, prefix string }
	signals := []signal{
		{"traces", "OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_"},
		{"logs", "OTEL_LOGS_EXPORTER", "OTEL_EXPORTER_OTLP_LOGS_"},
	}
	on := map[string]bool{}
	headers := map[string]map[string]string{}
	endpoints := map[string]string{}
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
		// The generic endpoint is a base URL that gets the signal path; the
		// signal-specific one is used as-is and wins (OTLP spec). The SDK also
		// parses the raw value and prints one it rejects, so a value with
		// surrounding space is refused, not trimmed.
		for _, name := range []string{envOTLPEndpoint, s.prefix + "ENDPOINT"} {
			if v := get(name); v != "" {
				raw, _ := lookup(name)
				u, err := url.Parse(v)
				if err != nil || raw != v || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					errs = append(errs, fmt.Errorf("%s: must be an http or https URL", name))
					continue
				}
				if name == envOTLPEndpoint {
					u.Path = path.Join(u.Path, "/v1/"+s.name)
				}
				endpoints[s.name] = u.String()
			}
		}
		on[s.name] = exp == "otlp" || (exp == "" && endpoints[s.name] != "")
		// The SDK parses every one of these variables itself when an
		// exporter is built, and prints a rejected key or value to the
		// global OpenTelemetry logger. Validate each one that is set so
		// nothing the SDK would reject gets that far.
		for _, name := range []string{envOTLPHeaders, s.prefix + "HEADERS"} { // signal-specific wins
			if v := get(name); v != "" {
				h, err := parseOTLPHeaders(v)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", name, err))
				}
				headers[s.name] = h
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
	)
	// The endpoint and headers are always passed, defaults included, so the
	// SDK's own reading of the process environment is overridden.
	endpointOf := func(signal string) string {
		if e := endpoints[signal]; e != "" {
			return e
		}
		return defaultOTLPEndpoint + "/v1/" + signal
	}
	headersOf := func(signal string) map[string]string {
		if h := headers[signal]; h != nil {
			return h
		}
		return map[string]string{}
	}
	if on["traces"] {
		if spans, err = otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpointOf("traces")),
			otlptracehttp.WithHeaders(headersOf("traces"))); err != nil {
			return nil, errors.New("OTEL_EXPORTER_OTLP_TRACES_*: cannot configure the trace exporter")
		}
	}
	if on["logs"] {
		if logs, err = otlploghttp.New(ctx, otlploghttp.WithEndpointURL(endpointOf("logs")),
			otlploghttp.WithHeaders(headersOf("logs"))); err != nil {
			return nil, errors.New("OTEL_EXPORTER_OTLP_LOGS_*: cannot configure the log exporter")
		}
	}
	return newTelemetry(telemetryResource(serviceName, resAttrs), traceCfg, spans, logs), nil
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
		if !isHeaderToken(k) {
			return nil, errors.New("keys must be HTTP header names (RFC 7230 tokens)")
		}
		dv, err := url.PathUnescape(v)
		if err != nil {
			return nil, errors.New("values must be percent-encoded")
		}
		out[k] = strings.TrimSpace(dv)
	}
	return out, nil
}

// isHeaderToken reports whether k is a non-empty RFC 7230 token, with the
// same character set as the OTLP exporters' own key check (ASCII letters and
// digits plus !#$%&'*+-.^_`|~). A key outside it makes the SDK print the key
// to its global logger, so goldie must refuse it first.
func isHeaderToken(k string) bool {
	if k == "" {
		return false
	}
	for _, c := range k {
		if c > unicode.MaxASCII {
			return false
		}
		if unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune("!#$%&'*+-.^_`|~", c) {
			continue
		}
		return false
	}
	return true
}

// parseResourceAttributes parses OTEL_RESOURCE_ATTRIBUTES, "k=v,k2=v2" with
// percent-encoded values. The SDK silently drops a malformed value, so goldie
// parses it first and fails startup. The error never contains the text.
func parseResourceAttributes(s string) (map[string]string, error) {
	if s == "" {
		return nil, nil
	}
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
	return slog.New(slog.NewMultiHandler(l.Handler(), levelGate{Handler: bridge, gate: l.Handler()}))
}

// levelGate makes the bridge honour the logger's level (GOLDIE_LOG_LEVEL): the
// batch processor does not filter by severity.
type levelGate struct {
	slog.Handler
	gate slog.Handler
}

func (g levelGate) Enabled(ctx context.Context, lv slog.Level) bool {
	return g.gate.Enabled(ctx, lv) && g.Handler.Enabled(ctx, lv)
}

func (g levelGate) WithAttrs(a []slog.Attr) slog.Handler {
	return levelGate{Handler: g.Handler.WithAttrs(a), gate: g.gate}
}

func (g levelGate) WithGroup(name string) slog.Handler {
	return levelGate{Handler: g.Handler.WithGroup(name), gate: g.gate}
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
