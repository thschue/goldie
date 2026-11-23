# 25. Role rules are marked once in the spec, with `x-goldie-role`

Status: proposed

## Context

Some operations (`getSync`, `getActivity`) and one parameter value (`drafts=true`) are for operator
tokens only (ADR 0008). Each rule was written in the spec's prose and again in code, so the two could
drift: a new operation could ship open while the spec said "operators only".

OpenAPI 3.1 can carry role names in an operation's `security` requirement for an http bearer scheme,
but it cannot express a rule on one parameter value, and goldie's generated server (oapi-codegen)
does not read `security` scopes either. `x-goldie-role` is the spec's first extension that carries
meaning (`x-lint-ignore` is tooling metadata). Once a release ships, ADR 0011 makes it part of the
contract, so it is recorded before that.

## Decision

A role rule is marked once, in `internal/contract/openapi.yaml`, with `x-goldie-role: operator`.

- On an operation, the whole operation needs the role. `internal/contract` reads these marks from the
  embedded spec at startup (`OperatorPaths`) and the app guard enforces them.
- On a `components/parameters` entry, a true value of the parameter needs the role. `drafts` is one
  such entry, `components/parameters/Drafts`, referenced by search and lookup. The mark is enforced
  where the parameter is bound, in `internal/api` (`readOptions`, a reader token asking for
  `drafts=true` gets 403 `forbidden`), because a generic guard in `app` would parse query strings the
  generated code already binds. A spec-walking test in `internal/api` finds every marked parameter
  and requires that a reader token sending the restricted value is refused, so an operation that takes
  `drafts` but forgets `readOptions` fails the build instead of leaking drafts.
- `security` scopes are not used for roles. A second mechanism would drift from the first.

The check is fail-closed for the whole document. A mark is accepted only directly on an operation or
directly on a `components/parameters` entry, and its value must be `operator`. A mark anywhere else, an
unknown role value or a misspelt one fails startup with the location, the extension and the key it
sits under. That includes the root, `info`, `webhooks`, security schemes, and a component an operation
reaches through `$ref` or an alias, such as `components/responses/X` or `components/requestBodies/X`:
the mark does not apply through the reference, so accepting it would leave the rule open while the
author believes it closed.

### Known limitations

These are deliberate fail-closed defaults today. Each one fails startup rather than silently leaving
an operation open.

- A path-item `$ref` (legal in OpenAPI 3.1) is rejected. A shared `components/pathItems` entry would
  need the check to resolve the `$ref` first.
- YAML merge keys (`<<: *x`) inside an operation are rejected when they carry a mark: the mark is not
  directly on the operation, so the check fails startup.
- OpenAPI 3.2's `query` operation and `additionalOperations` are not in `httpMethods` in
  `internal/contract/roles.go`, so a mark on them is rejected until they are added explicitly.
- A mark in `components/responses`, or in another component reached through `$ref`, is an error.

A `$ref` inside an operation's responses, request body or parameters stays legal.

## Consequences

One mechanism covers both kinds of rule. The guard cannot drift from the spec, and the generated
server is unchanged: the extension is additive (ADR 0011), and clients ignore it. The mark on a
parameter does not change its wire shape.

Tools that read only standard OpenAPI (Swagger UI's lock icons, outside generators) do not see the
role, so the `bearerAuth` description names the extension. Under ADR 0011 the mark is part of the
contract from the first release: removing or weakening one is a breaking change.

A future operation that needs a role takes one line in the spec and no code, and a future parameter
rule takes one mark plus its binding code, which the spec-walking test checks.
