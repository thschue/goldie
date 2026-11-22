package platform

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/resource"
)

// The metrics variables beyond the shared OTLP ones.
const (
	envMetricInterval    = "OTEL_METRIC_EXPORT_INTERVAL"
	envMetricTemporality = "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE"
)

const (
	// defaultMetricInterval is the OpenTelemetry default export interval.
	defaultMetricInterval = 60 * time.Second

	// metricCardinalityLimit caps the data points one instrument holds per
	// collection. Instruments will carry attributes derived from requests,
	// so the cap is explicit rather than the SDK's default or its
	// OTEL_GO_X_CARDINALITY_LIMIT variable. Overflow is aggregated by the
	// SDK into one data point, not dropped.
	metricCardinalityLimit = 2000
)

// telemetryOption changes how newTelemetry wires the providers.
type telemetryOption func(*telemetryConfig)

type telemetryConfig struct {
	metricReader sdkmetric.Reader
}

// withMetricReader builds the MeterProvider around r instead of an OTLP
// exporter, so tests collect the recorded data directly.
func withMetricReader(r sdkmetric.Reader) telemetryOption {
	return func(c *telemetryConfig) { c.metricReader = r }
}

// newMeterProvider is the one MeterProvider of the process. It is held by
// Telemetry and never installed as the global provider. The exemplar filter
// is passed explicitly (the SDK's default) so OTEL_METRICS_EXEMPLAR_FILTER in
// the process environment does not apply.
func newMeterProvider(res *resource.Resource, r sdkmetric.Reader) *sdkmetric.MeterProvider {
	return sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(r),
		sdkmetric.WithCardinalityLimit(metricCardinalityLimit),
		sdkmetric.WithExemplarFilter(exemplar.TraceBasedFilter))
}

// Meter returns a named meter of the metrics provider, or a no-op meter when
// metrics are off. It is safe on a nil Telemetry.
func (t *Telemetry) Meter(name string) metric.Meter {
	if t == nil || t.mp == nil {
		return metricnoop.NewMeterProvider().Meter(name)
	}
	return t.mp.Meter(name)
}

// metricSettings are the export interval and temporality, parsed from the
// injected lookup and handed to the SDK explicitly, so it reads neither from
// the process environment.
type metricSettings struct {
	interval    time.Duration
	temporality sdkmetric.TemporalitySelector
}

// parseMetricSettings reads OTEL_METRIC_EXPORT_INTERVAL (milliseconds) and
// OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE through get. Errors name
// the variable, never the value.
func parseMetricSettings(get func(string) string) (metricSettings, error) {
	var errs []error
	ms := metricSettings{interval: defaultMetricInterval, temporality: sdkmetric.CumulativeTemporalitySelector}
	if v := get(envMetricInterval); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 || n > math.MaxInt64/int64(time.Millisecond) {
			errs = append(errs, fmt.Errorf("%s: must be a positive number of milliseconds", envMetricInterval))
		} else {
			ms.interval = time.Duration(n) * time.Millisecond
		}
	}
	switch strings.ToLower(get(envMetricTemporality)) {
	case "", "cumulative":
	case "delta":
		ms.temporality = sdkmetric.DeltaTemporalitySelector
	case "lowmemory":
		ms.temporality = sdkmetric.LowMemoryTemporalitySelector
	default:
		errs = append(errs, fmt.Errorf("%s: must be cumulative, delta or lowmemory", envMetricTemporality))
	}
	return ms, errors.Join(errs...)
}

// newMetricReader is a periodic reader around an OTLP http/protobuf exporter.
// Endpoint and headers are always passed, as for the other signals.
func newMetricReader(ctx context.Context, endpoint string, headers map[string]string, ms metricSettings) (sdkmetric.Reader, error) {
	exp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint),
		otlpmetrichttp.WithHeaders(headers), otlpmetrichttp.WithTemporalitySelector(ms.temporality))
	if err != nil {
		return nil, errors.New("OTEL_EXPORTER_OTLP_METRICS_*: cannot configure the metrics exporter")
	}
	return sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(ms.interval)), nil
}
