package platform

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ItemUseCounter counts every answered lookup or get of an Item, following
// SkillGetCounter's shape. Its attributes are kind, name (an Item that
// exists, since only hits are counted), scope (global or project, never a
// Project id), via (cli, mcp or other), project (a registered Project's
// spelling or "unregistered") and principal (a token name from the
// configuration). No OpenTelemetry type leaves this package.
//
// Beside the metric it keeps an in-process tally of the same events, by kind,
// name, Scope and via, so a replica can report an Item's uses with metrics off
// (see Uses). The tally restarts with the process.
type ItemUseCounter struct {
	c metric.Int64Counter // nil counts nothing to the metric; the tally still counts

	mu    sync.Mutex
	tally map[itemKey]*ViaCounts
	since time.Time // when the tally began: the server's start
}

// itemKey names an Item in the tally. project is "" for a global Item, so a
// global and a project Item of one name are two entries.
type itemKey struct{ kind, name, project string }

// ViaCounts is the uses of one Item by client.
type ViaCounts struct{ CLI, MCP, Other int }

// processStart is when this package was loaded, the start of a nil counter.
var processStart = time.Now()

// Since is when the tally began, which is when the server was built: the
// process start, for "since" in a usage answer. A nil counter has no tally of
// its own and says the process start.
func (ic *ItemUseCounter) Since() time.Time {
	if ic == nil {
		return processStart
	}
	return ic.since
}

// ItemUseCounter returns the counter called name (it starts with "goldie.")
// with the given description. Without metrics it counts nothing to the
// metric, and it is safe on a nil Telemetry. An instrument the SDK refuses
// counts nothing to the metric too. The tally counts either way.
func (t *Telemetry) ItemUseCounter(name, description string) *ItemUseCounter {
	c, err := t.Meter("goldie").Int64Counter(name, metric.WithDescription(description), metric.WithUnit("{request}"))
	if err != nil {
		c = nil
	}
	return &ItemUseCounter{c: c, tally: map[itemKey]*ViaCounts{}, since: time.Now()}
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

// Add counts one use, in the tally and in the metric. It is safe on a nil
// counter.
func (ic *ItemUseCounter) Add(ctx context.Context, u ItemUse) {
	if ic == nil {
		return
	}
	ic.count(u)
	if ic.c == nil {
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

// count adds one use to the tally. Only an Item that was found reaches it, so
// the tally is bounded by the content, not by what a caller sends.
func (ic *ItemUseCounter) count(u ItemUse) {
	k := itemKey{kind: u.Kind, name: u.Name}
	if !u.Global {
		k.project = u.Project
	}
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if ic.tally == nil {
		ic.tally = map[itemKey]*ViaCounts{}
	}
	v := ic.tally[k]
	if v == nil {
		v = &ViaCounts{}
		ic.tally[k] = v
	}
	switch viaLabel(u.Via) {
	case "cli":
		v.CLI++
	case "mcp":
		v.MCP++
	default:
		v.Other++
	}
}

// Uses is the tally of one Item: project is the registered Project's id for a
// project Item and ignored for a global one. It is safe on a nil counter and
// returns zeros for an Item never used.
func (ic *ItemUseCounter) Uses(kind, name, project string, global bool) ViaCounts {
	if ic == nil {
		return ViaCounts{}
	}
	k := itemKey{kind: kind, name: name}
	if !global {
		k.project = project
	}
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if v := ic.tally[k]; v != nil {
		return *v
	}
	return ViaCounts{}
}
