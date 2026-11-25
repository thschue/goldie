package platform

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// SkillGetCounter counts the skill gets that hit, following ResultCounter's shape. Its
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

// Add counts one skill get that hit. name is the canonical name of a skill
// that exists, global says whether it came from the global Scope (any other
// Scope is "project"), and via is cli, mcp or anything else, which counts as
// other. It is safe on a nil counter.
func (sc *SkillGetCounter) Add(ctx context.Context, name string, global bool, via string) {
	if sc == nil || sc.c == nil {
		return
	}
	sc.c.Add(ctx, 1, metric.WithAttributeSet(attribute.NewSet(
		attribute.String("name", name), attribute.String("scope", scopeLabel(global)), attribute.String("via", viaLabel(via)))))
}

// scopeLabel is the scope attribute of an Item metric: global, or project for
// any other Scope, never a Project id.
func scopeLabel(global bool) string {
	if global {
		return "global"
	}
	return "project"
}

// viaLabel is the via attribute of an Item metric: cli or mcp, anything else
// is other, so no request value becomes a label.
func viaLabel(via string) string {
	if via == "cli" || via == "mcp" {
		return via
	}
	return "other"
}
