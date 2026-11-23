package platform

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// PollCounter counts the finished polls of one Source by outcome and records
// how long each took (goldie.sync.duration, seconds), following
// ResultCounter: a goldie-named instrument whose attributes are fixed, so no
// value derived from a Source's configuration (URL, path) can become a label
// (PC-12). source is the Source's name, a constant such as "git", and result is
// ok or error. No OpenTelemetry type leaves this package.
type PollCounter struct {
	c       metric.Int64Counter     // nil counts nothing
	d       metric.Float64Histogram // nil records nothing
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
	d, err := t.Meter("goldie").Float64Histogram("goldie.sync.duration",
		metric.WithDescription("Time a Source poll took, by source and result."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(pollBuckets...))
	if err != nil {
		d = nil
	}
	src := attribute.String("source", source)
	return &PollCounter{
		c:   c,
		d:   d,
		ok:  metric.WithAttributeSet(attribute.NewSet(src, attribute.String("result", "ok"))),
		bad: metric.WithAttributeSet(attribute.NewSet(src, attribute.String("result", "error"))),
	}
}

// pollBuckets are the duration bucket bounds in seconds, 0.05 s to 60 s: a
// local read is fast and a git fetch over a slow link takes many seconds.
var pollBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

// Add counts one finished poll that took took: an error when failed is true,
// else ok. It is safe on a nil counter.
func (pc *PollCounter) Add(ctx context.Context, failed bool, took time.Duration) {
	if pc == nil {
		return
	}
	opt := pc.ok
	if failed {
		opt = pc.bad
	}
	if pc.c != nil {
		pc.c.Add(ctx, 1, opt)
	}
	if pc.d != nil {
		pc.d.Record(ctx, took.Seconds(), opt)
	}
}
