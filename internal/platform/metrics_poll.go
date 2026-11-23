package platform

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// PollCounter counts the finished polls of one Source by outcome, following
// ResultCounter: a goldie-named instrument whose attributes are fixed, so no
// value derived from a Source's configuration (URL, path) can become a label
// (PC-12). source is the Source's name, a constant such as "git", and result is
// ok or error. No OpenTelemetry type leaves this package.
type PollCounter struct {
	c       metric.Int64Counter // nil counts nothing
	ok, bad metric.MeasurementOption
}

// PollCounter returns the counter called name (it starts with "goldie.") with
// the given description, for the Source called source. Without metrics it
// counts nothing, and it is safe on a nil Telemetry. An instrument the SDK
// refuses counts nothing too: a metric never breaks a poll.
func (t *Telemetry) PollCounter(name, description, source string) *PollCounter {
	c, err := t.Meter("goldie").Int64Counter(name, metric.WithDescription(description), metric.WithUnit("{poll}"))
	if err != nil {
		c = nil
	}
	src := attribute.String("source", source)
	return &PollCounter{
		c:   c,
		ok:  metric.WithAttributeSet(attribute.NewSet(src, attribute.String("result", "ok"))),
		bad: metric.WithAttributeSet(attribute.NewSet(src, attribute.String("result", "error"))),
	}
}

// Add counts one finished poll: an error when failed is true, else ok. It is
// safe on a nil counter.
func (pc *PollCounter) Add(ctx context.Context, failed bool) {
	if pc == nil || pc.c == nil {
		return
	}
	if failed {
		pc.c.Add(ctx, 1, pc.bad)
		return
	}
	pc.c.Add(ctx, 1, pc.ok)
}
