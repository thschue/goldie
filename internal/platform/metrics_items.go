package platform

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ItemCount is the number of Items of one Kind in one Scope kind. Scope is
// global, project or local, and Kind is article, agent, instruction or skill.
// It mirrors engine.ItemCount, so this package imports nothing from engine.
type ItemCount struct {
	Scope string
	Kind  string
	Count int64
}

// ItemGauge registers the observable gauge goldie.items, which counts the
// Items goldie serves by scope and kind and nothing else, so no Project id,
// Source name, URL or path can become a label (PC-12). read returns the
// current counts and runs at each collection; nil or empty means there is no
// Snapshot yet and nothing is observed. It is safe on a nil Telemetry, with
// metrics off and with a nil read. An instrument the SDK refuses observes
// nothing: a metric never breaks serving.
func (t *Telemetry) ItemGauge(read func() []ItemCount) {
	if read == nil {
		return
	}
	_, _ = t.Meter("goldie").Int64ObservableGauge("goldie.items",
		metric.WithDescription("Items served from the current Snapshot, by scope and kind."),
		metric.WithUnit("{item}"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			for _, c := range read() {
				o.Observe(c.Count, metric.WithAttributeSet(attribute.NewSet(
					attribute.String("scope", c.Scope), attribute.String("kind", c.Kind))))
			}
			return nil
		}))
}
