package platform

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ResultCounter counts answered requests by outcome. It is the shape
// goldie counters follow (#13): a goldie-named instrument whose attributes are
// fixed or checked, so no value derived from a request (query text, article id,
// Project id) can become a label (PC-12). This one has one attribute, result,
// with two fixed values; SkillGetCounter is the one that labels a skill name. No
// OpenTelemetry type leaves this package.
type ResultCounter struct {
	c         metric.Int64Counter // nil counts nothing
	hit, miss metric.MeasurementOption
}

// ResultCounter returns the counter called name (it starts with "goldie.")
// with the given description. Without metrics it counts nothing, and it is
// safe on a nil Telemetry. An instrument the SDK refuses counts nothing too:
// a metric never breaks a request.
func (t *Telemetry) ResultCounter(name, description string) *ResultCounter {
	c, err := t.Meter("goldie").Int64Counter(name, metric.WithDescription(description), metric.WithUnit("{request}"))
	if err != nil {
		c = nil
	}
	return &ResultCounter{
		c:    c,
		hit:  metric.WithAttributeSet(attribute.NewSet(attribute.String("result", "hit"))),
		miss: metric.WithAttributeSet(attribute.NewSet(attribute.String("result", "miss"))),
	}
}

// Add counts one answered request: a miss when miss is true, else a hit. It is
// safe on a nil counter.
func (rc *ResultCounter) Add(ctx context.Context, miss bool) {
	if rc == nil || rc.c == nil {
		return
	}
	if miss {
		rc.c.Add(ctx, 1, rc.miss)
		return
	}
	rc.c.Add(ctx, 1, rc.hit)
}
