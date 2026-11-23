package platform

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Acceptance tests for #595 at the instrument: Telemetry.ItemGauge is the
// observable gauge goldie.items (Int64, unit {item}) with the attributes scope
// and kind only. It reads its values through a callback at each collection and
// imports nothing from engine.
//
// The API the tests assume, following PollCounter (the issue names the call
// but not its types):
//
//	type ItemCount struct {
//		Scope string
//		Kind  string
//		Count int64
//	}
//	func (t *Telemetry) ItemGauge(read func() []ItemCount)
//
// read returns the current counts; nil or empty means there is no Snapshot yet
// and nothing is observed.

// itemPoints collects once and returns the goldie.items metric, or false when
// the export has none.
func itemPoints(t *testing.T, reader *sdkmetric.ManualReader) (metricdata.Metrics, bool) {
	t.Helper()
	rm := collectOne(t, reader)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "goldie.items" {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

func itemsTelemetry(t *testing.T) (*Telemetry, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), nil, nil, withMetricReader(reader))
	t.Cleanup(func() { shutdownQuietly(tel) })
	return tel, reader
}

func gaugePoints(t *testing.T, m metricdata.Metrics) map[string]int64 {
	t.Helper()
	g, ok := m.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("goldie.items data = %T, want an Int64 gauge", m.Data)
	}
	out := map[string]int64{}
	for _, dp := range g.DataPoints {
		keys := []string{}
		for _, kv := range dp.Attributes.ToSlice() {
			keys = append(keys, string(kv.Key))
		}
		slices.Sort(keys)
		if !slices.Equal(keys, []string{"kind", "scope"}) {
			t.Errorf("attribute keys = %v, want only kind and scope", keys)
		}
		scope, _ := dp.Attributes.Value("scope")
		kind, _ := dp.Attributes.Value("kind")
		out[scope.AsString()+"/"+kind.AsString()] = dp.Value
	}
	return out
}

// Criterion: the metric is goldie.items, an Int64 observable gauge, unit
// {item}, with the attributes scope and kind.
func TestItemGaugeIsAnInt64GaugeInItemsWithScopeAndKind(t *testing.T) {
	tel, reader := itemsTelemetry(t)
	tel.ItemGauge(func() []ItemCount {
		return []ItemCount{{"global", "article", 3}, {"project", "skill", 0}}
	})
	m, ok := itemPoints(t, reader)
	if !ok {
		t.Fatal("goldie.items is not exported")
	}
	if m.Unit != "{item}" {
		t.Errorf("unit = %q, want {item}", m.Unit)
	}
	got := gaugePoints(t, m)
	if len(got) != 2 || got["global/article"] != 3 {
		t.Errorf("points = %v, want global/article 3 and project/skill 0", got)
	}
	if v, ok := got["project/skill"]; !ok || v != 0 {
		t.Errorf("project/skill = %d, present %v: an empty series is exported with 0", v, ok)
	}
}

// Criterion: values are read at each collection, so the next collection after
// a change shows the new counts.
func TestItemGaugeReadsAtEachCollection(t *testing.T) {
	tel, reader := itemsTelemetry(t)
	var n atomic.Int64
	n.Store(1)
	tel.ItemGauge(func() []ItemCount { return []ItemCount{{"global", "agent", n.Load()}} })
	for _, want := range []int64{1, 7, 0} {
		n.Store(want)
		m, ok := itemPoints(t, reader)
		if !ok {
			t.Fatal("goldie.items is not exported")
		}
		if got := gaugePoints(t, m)["global/agent"]; got != want {
			t.Errorf("global/agent = %d, want %d", got, want)
		}
	}
}

// Criterion: before the first Snapshot there are no data points.
func TestItemGaugeHasNoPointsWithoutCounts(t *testing.T) {
	tel, reader := itemsTelemetry(t)
	var have atomic.Bool
	tel.ItemGauge(func() []ItemCount {
		if !have.Load() {
			return nil
		}
		return []ItemCount{{"global", "article", 1}}
	})
	if m, ok := itemPoints(t, reader); ok {
		if g, isGauge := m.Data.(metricdata.Gauge[int64]); !isGauge || len(g.DataPoints) != 0 {
			t.Errorf("goldie.items before the first Snapshot = %#v, want no data points", m.Data)
		}
	}
	have.Store(true)
	m, ok := itemPoints(t, reader)
	if !ok || gaugePoints(t, m)["global/article"] != 1 {
		t.Errorf("after the first Snapshot: exported %v, want global/article 1", ok)
	}
}

// Criterion: with a nil Telemetry, no metrics, or a nil callback, nothing is
// observed and nothing panics.
func TestItemGaugeIsSafeWithoutTelemetryOrCallback(t *testing.T) {
	var called atomic.Bool
	read := func() []ItemCount { called.Store(true); return []ItemCount{{"global", "article", 1}} }

	var nilTel *Telemetry
	nilTel.ItemGauge(read)

	off, err := SetupTelemetry(context.Background(), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	off.ItemGauge(read)
	if called.Load() {
		t.Error("the callback ran without a meter provider")
	}

	tel, reader := itemsTelemetry(t)
	tel.ItemGauge(nil)
	if m, ok := itemPoints(t, reader); ok {
		if g, isGauge := m.Data.(metricdata.Gauge[int64]); isGauge && len(g.DataPoints) != 0 {
			t.Errorf("a nil callback observed %v", g.DataPoints)
		}
	}
}
