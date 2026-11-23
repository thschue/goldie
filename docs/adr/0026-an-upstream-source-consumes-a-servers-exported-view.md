# 26. An upstream Source consumes a server's exported view; its transport lives in the composition root

Status: accepted

## Context

An embedded server (#10) needs to combine a repository's `.goldie/` with a central server. The engine
owns Sources but cannot import `contract`, which holds the only hardened client. Without the central
server's enforced Items the embedded server cannot refuse local content that shadows them. Two security
questions were put to the maintainer: where the user's token may go when `.goldie/config.yaml` names an
`addr`, and what happens when the upstream is unreachable at startup.

## Decision

- Upstream sends the Project's resolved view without drafts, with `scope` and `enforced` on each Item
  (`/api/v1/projects/{id}/items`). The engine owns the kind (`NewViewSource`): it validates a View and
  builds Snapshots, with no I/O. `app` supplies the fetch through the contract client.
- **Token:** a `config.yaml` `addr` is used as upstream only when it has the same origin as
  `GOLDIE_ADDR`, and the token is only ever sent to `GOLDIE_ADDR`. A different origin is refused with a
  hint to set `GOLDIE_ADDR`, and is never contacted. `http` is refused unless the host is loopback.
- **Upstream unreachable at startup:** the server starts, `/readyz` fails `source:upstream` until the
  first successful load (PC-02), and the API answers 503 `not_ready` until then, as before the first load
  of any Source (#95). Nothing unchecked against upstream enforcement is served. A later failure keeps
  the last good Snapshot and readiness stays `ok`, as for a stale git Source. A local Item that shadows
  an enforced upstream Item is not an upstream failure: it stops startup.

## Consequences

One client, and a pure engine. A reader gets no Items beyond their own view. Resolution runs in two
engine instances but is implemented once (ADR 0002 holds). The export shape is additive-only contract.
With an upstream configured, local cannot suppress an Item that upstream's project copy already
shadowed. A View holds winners only, so every reference and lookup in it resolves in the exported
Project's view, not in the global view of the central server.

## Rejected

A raw export of all Scopes (it leaks other Projects' Items), an HTTP client inside the engine (a second
hardened client), and a new `internal/upstream` module. A separate `GOLDIE_UPSTREAM_TOKEN`, and failing
closed when the upstream is unreachable at startup, were the maintainer's alternatives and were not chosen.
