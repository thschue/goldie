package platform

import (
	"context"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Acceptance tests for #599: ToolCallCounter counts MCP tool calls as
// goldie.mcp.tool.calls, an Int64 counter with unit {call}, by tool (the eight
// registered names, else "other") and result (ok or error), and is safe on nil.

func TestToolCallCounterRecordsByToolAndResult(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), nil, nil, withMetricReader(reader))
	t.Cleanup(func() { shutdownQuietly(tel) })
	c := tel.ToolCallCounter("goldie.mcp.tool.calls", "MCP tool calls, by tool and result.")
	ctx := context.Background()
	for _, tool := range []string{"search_knowledge", "get_article", "list_projects", "current_context",
		"find_skills", "get_skill", "list_skills", "list_agents"} {
		c.Add(ctx, tool, false)
	}
	c.Add(ctx, "search_knowledge", true)
	c.Add(ctx, "planted-tool-marker-5994", false)
	c.Add(ctx, "Search_Knowledge", true)
	c.Add(ctx, "", false)

	got := map[string]int64{}
	var seen bool
	for _, sm := range collectOne(t, reader).ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "goldie.mcp.tool.calls" {
				continue
			}
			seen = true
			if m.Unit != "{call}" {
				t.Errorf("unit = %q, want {call}", m.Unit)
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || !sum.IsMonotonic {
				t.Fatalf("data = %#v, want a monotonic Int64 sum", m.Data)
			}
			for _, dp := range sum.DataPoints {
				if dp.Attributes.Len() != 2 {
					t.Errorf("attributes = %v, want tool and result only", dp.Attributes)
				}
				tool, _ := dp.Attributes.Value("tool")
				result, _ := dp.Attributes.Value("result")
				got[tool.AsString()+"|"+result.AsString()] += dp.Value
			}
		}
	}
	if !seen {
		t.Fatal("goldie.mcp.tool.calls not recorded")
	}
	want := map[string]int64{
		"search_knowledge|ok": 1, "search_knowledge|error": 1,
		"get_article|ok": 1, "list_projects|ok": 1, "current_context|ok": 1,
		"find_skills|ok": 1, "get_skill|ok": 1, "list_skills|ok": 1, "list_agents|ok": 1,
		"other|ok": 2, "other|error": 1,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
	for k := range got {
		if strings.Contains(k, "marker") {
			t.Errorf("a caller's name became a label: %s", k)
		}
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected point %s", k)
		}
	}
}

func TestToolCallCounterIsSafeOnNil(t *testing.T) {
	var nilCounter *ToolCallCounter
	nilCounter.Add(context.Background(), "search_knowledge", false)
	var tel *Telemetry
	tel.ToolCallCounter("goldie.mcp.tool.calls", "x").Add(context.Background(), "get_skill", true)
}
