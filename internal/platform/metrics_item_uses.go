package platform

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ItemUseCounter counts every answered lookup or get of an Item, following
// SkillGetCounter's shape. Its attributes are kind, name (an Item that
// exists, since only hits are counted), scope (global or project, never a
// Project id), via (cli, mcp or other), project (a registered Project's
// spelling or "unregistered") and principal (a token name from the
// configuration). No OpenTelemetry type leaves this package.
type ItemUseCounter struct {
	c metric.Int64Counter // nil counts nothing
}

// ItemUseCounter returns the counter called name (it starts with "goldie.")
// with the given description. Without metrics it counts nothing, and it is
// safe on a nil Telemetry. An instrument the SDK refuses counts nothing too.
func (t *Telemetry) ItemUseCounter(name, description string) *ItemUseCounter {
	c, err := t.Meter("goldie").Int64Counter(name, metric.WithDescription(description), metric.WithUnit("{request}"))
	if err != nil {
		c = nil
	}
	return &ItemUseCounter{c: c}
}

// ItemUse is one use of an Item. Every field must come from the server's own
// data (a found Item, the registered Project, the token name), never from a
// request value.
type ItemUse struct {
	Kind      string // article, skill, agent or loop
	Name      string // canonical name or id of the Item found
	Global    bool   // the Item's Scope is global (any other is "project")
	Via       string // cli, mcp or anything else, which counts as other
	Project   string // registered Project spelling, "" when none is registered
	Principal string // token name, "" when the request had none
}

// Add counts one use. It is safe on a nil counter.
func (ic *ItemUseCounter) Add(ctx context.Context, u ItemUse) {
	if ic == nil || ic.c == nil {
		return
	}
	project := u.Project
	if project == "" {
		project = "unregistered"
	}
	principal := u.Principal
	if principal == "" {
		principal = "anonymous"
	}
	ic.c.Add(ctx, 1, metric.WithAttributeSet(attribute.NewSet(
		attribute.String("kind", u.Kind), attribute.String("name", u.Name), attribute.String("scope", scopeLabel(u.Global)),
		attribute.String("via", viaLabel(u.Via)), attribute.String("project", project), attribute.String("principal", principal))))
}
