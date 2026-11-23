# 24. API property names are snake_case

Status: proposed (the maintainer decides; #365)

## Context

The first multi-word properties in the OpenAPI contract, `client_version` and `trace_id` on
`ActivityEvent` (#243), raised the question of casing. `./run lint-spec` runs vacuum with an empty
config, so vacuum's default ruleset applies, and it includes `camel-case-properties`. No project
decision chose that rule. Today four properties carry `x-lint-ignore: camel-case-properties` in
`internal/contract/openapi.yaml`: `replaced_by` (on two schemas), `client_version` and `trace_id`.

The rest of goldie's vocabulary is already snake_case:

- Item frontmatter and the API shapes built from it: `replaced_by`. `requires_sync` and
  `shadowed_by` are named in `docs/design/ui-handoff.md` and are not in the spec yet.
- Federated activity (ADR 0022): `replica_status`, not in the spec yet either.
- The MCP tool names: `search_knowledge` and `list_projects` (`internal/mcp/mcp.go`).

From the first release the `/api/v1` and MCP shapes are additive-only, so a wire name cannot change
once released (ADR 0011).

## Decision

Property names in `/api/v1`, in MCP tool and prompt shapes, and in SSE payloads are **snake_case**.
When a property exposes a frontmatter key, it is spelled exactly like the key (`replaced_by`).

Until the spec lint uses a snake-case rule, each multi-word property carries
`x-lint-ignore: camel-case-properties`.

## Consequences

- One spelling for a field from the article file to the API, the UI and the MCP. Rejected:
  camelCase, which would give `replaced_by` in an article's frontmatter and `replacedBy` in the API.
- Generated Go fields stay idiomatic (`ClientVersion`, `TraceId`); the JSON tags carry the
  snake_case wire name.
- Until the lint changes, every multi-word property needs a per-property ignore. Replacing them with
  one snake-case vacuum rule is a later issue. Disabling a stock rule makes a check less strict, so
  under ADR 0013 the reviewer holds that change for the maintainer if it is significant.
- Changing the casing after release would be a breaking change and needs a new API version
  (ADR 0011).
