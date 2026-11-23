package platform

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// toolNames are the MCP tools the server registers. A test in internal/app
// fails when internal/mcp's tool list drifts from it.
var toolNames = map[string]bool{
	"search_knowledge": true, "get_article": true, "list_projects": true, "current_context": true,
	"find_skills": true, "get_skill": true, "list_skills": true, "list_agents": true,
	"list_loops": true, "get_loop": true,
}

// ToolCallCounter counts MCP tool calls by tool and result, following
// PollCounter. Its attributes are tool (one of the registered names, else
// "other", so a name a caller sends never becomes a label) and result (ok or
// error). No tool argument is ever a label (PC-12), and no OpenTelemetry type
// leaves this package.
type ToolCallCounter struct {
	c metric.Int64Counter // nil counts nothing
}

// ToolCallCounter returns the counter called name (it starts with "goldie.")
// with the given description. Without metrics it counts nothing, and it is
// safe on a nil Telemetry. An instrument the SDK refuses counts nothing too:
// a metric never breaks a tool call.
func (t *Telemetry) ToolCallCounter(name, description string) *ToolCallCounter {
	c, err := t.Meter("goldie").Int64Counter(name, metric.WithDescription(description), metric.WithUnit("{call}"))
	if err != nil {
		c = nil
	}
	return &ToolCallCounter{c: c}
}

// Add counts one tool call: an error when failed is true, else ok. A tool
// that is not registered counts as "other". It is safe on a nil counter.
func (c *ToolCallCounter) Add(ctx context.Context, tool string, failed bool) {
	if c == nil || c.c == nil {
		return
	}
	if !toolNames[tool] {
		tool = "other"
	}
	result := "ok"
	if failed {
		result = "error"
	}
	c.c.Add(ctx, 1, metric.WithAttributeSet(attribute.NewSet(
		attribute.String("tool", tool), attribute.String("result", result))))
}
