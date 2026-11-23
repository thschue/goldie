# 24. API wire names are snake_case

Status: accepted

## Context

The first multi-word properties in the OpenAPI contract, `client_version` and `trace_id` on
`ActivityEvent` (#243), raised the question of casing. `./run lint-spec` runs vacuum with an empty
config, so vacuum's default ruleset applies, and it includes `camel-case-properties`. No project
decision chose that rule. Today four properties carry `x-lint-ignore: camel-case-properties` in
`internal/contract/openapi.yaml`: `replaced_by` (on two schemas), `client_version` and `trace_id`.

The rest of goldie's vocabulary is already snake_case:

- Item frontmatter and the API shapes built from it: `replaced_by`. `requires_sync` and
  `shadowed_by` are named in `docs/design/ui-handoff.md` and are not in the spec yet.
- Query parameter names: `q`, `project`, `drafts`. vacuum's default rules have no casing rule for
  parameters, so a camelCase one would pass the lint and then freeze at release.
- SSE event types: `live` and `dropped` (`internal/api/activity_stream.go`). ADR 0022's
  `replica_status` is an event too, not yet in the spec.
- Error codes: `not_ready`, `too_many_streams` (`internal/api`) and `upgrade_required`
  (`internal/api/clientversion.go`). Enum values (`deprecated`, `draft`, `search`, `lookup`) agree.
- The MCP tool names, `search_knowledge` and `list_projects`, and the hand-written argument structs
  in `internal/mcp/mcp.go`, whose fields carry explicit `json:` tags (`query`, `project`, `id`).
- Frontmatter keys read by the engine, `yaml:"replaced_by"` (`internal/engine/governance.go`).

From the first release the `/api/v1` and MCP shapes are additive-only, so a wire name cannot change
once released (ADR 0011). That holds for every name a client can depend on, not only properties.
MCP tool arguments and results are hand-written Go structs that no spec lint or codegen checks, and
Go's JSON encoding emits the Go field name when a field has no tag.

## Decision

Wire names in `/api/v1`, in MCP tool and prompt shapes and in SSE payloads are **snake_case**:
property names, query parameter names, SSE event types, enum values and error codes. Path parameter
names follow for consistency. HTTP header names (`X-Goldie-Client`, `Last-Event-ID`) and
OpenTelemetry attribute names keep their own conventions.

When a property exposes a frontmatter key, it is spelled exactly like the key (`replaced_by`). New
multi-word frontmatter and `.goldie/` file keys are snake_case too, so the key cannot force a
camelCase API field.

Spec-generated Go types get the wire name from the spec. Hand-written wire structs (MCP tool
arguments and results) carry it in an explicit `json:` tag.

Until the spec lint uses a snake-case rule, each multi-word property carries
`x-lint-ignore: camel-case-properties`.

## Consequences

- One spelling for a field from the article file to the API, the UI and the MCP. Rejected:
  camelCase, which would give `replaced_by` in an article's frontmatter and `replacedBy` in the API.
- Go fields keep Go-style names as oapi-codegen generates them (`ClientVersion`, `TraceId`); the JSON
  tags carry the snake_case wire name.
- Until the lint changes, every multi-word property needs a per-property ignore. Replacing them with
  one snake-case vacuum rule is a later issue. Disabling a stock rule makes a check less strict, so
  under ADR 0013 the reviewer holds that change for the maintainer if it is significant.
- Changing the casing after release would be a breaking change and needs a new API version
  (ADR 0011).
