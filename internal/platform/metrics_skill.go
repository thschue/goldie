package platform

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// SkillGetCounter counts the skill gets that hit, following ResultCounter. Its
// attributes are name (a skill that exists, since only hits are counted, so a
// name a caller makes up never becomes a label), scope (global or project,
// never a Project id) and via (cli, mcp or other). No OpenTelemetry type
// leaves this package.
type SkillGetCounter struct {
	c metric.Int64Counter // nil counts nothing
}

// SkillGetCounter returns the counter called name (it starts with "goldie.")
// with the given description. Without metrics it counts nothing, and it is
// safe on a nil Telemetry. An instrument the SDK refuses counts nothing too.
func (t *Telemetry) SkillGetCounter(name, description string) *SkillGetCounter {
	c, err := t.Meter("goldie").Int64Counter(name, metric.WithDescription(description), metric.WithUnit("{request}"))
	if err != nil {
		c = nil
	}
	return &SkillGetCounter{c: c}
}

// SkillScopeLabel is the scope attribute: global, or project.
func SkillScopeLabel(global bool) string {
	if global {
		return "global"
	}
	return "project"
}

// Add counts one skill get that hit. The caller passes the name of a skill
// that exists and via as one of a fixed few values. It is safe on a nil
// counter.
func (sc *SkillGetCounter) Add(ctx context.Context, name, scope, via string) {
	if sc == nil || sc.c == nil {
		return
	}
	sc.c.Add(ctx, 1, metric.WithAttributeSet(attribute.NewSet(
		attribute.String("name", name), attribute.String("scope", scope), attribute.String("via", via))))
}
