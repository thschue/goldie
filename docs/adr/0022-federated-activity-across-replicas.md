# 22. Federated activity across replicas

Status: accepted

## Context

#1 keeps activity in memory per process, with no database and no shared history (Q9). Operators want one live view per installation (Q48).

#1 lists "a database or shared activity history across replicas" as out of scope. This is not that: there is no shared store, nothing is persisted, and each replica owns its own ring; the serving replica only merges live streams it reads from its peers.

## Decision

Each replica keeps its own bounded ring buffer. `GET /api/v1/activity?replicas=all` makes the serving replica subscribe to each peer's local stream. Peers are found by resolving `GOLDIE_ACTIVITY_PEERS` (headless-Service DNS); without it, only the local stream is served. The caller's own credential is forwarded, so each peer re-checks the operator role, and there is no internal peer token (Q49). That credential is the bearer token or, for a browser signed in with a session cookie, the session cookie alone; a bearer token wins. Peer requests are always local-only. History is merged by timestamp. Live events pass a reorder window (default 2s, configurable), are deduped by `replica` plus `id`, and those later than the window are marked `late: true` (Q50). An unreachable or slow peer gives a `replica_status` event and is dropped on slow, never waited for. Nothing is persisted.

## Consequences

The view is best-effort and partial while peers are down, and ordering holds only within the window, so clock skew shows up as late events. Operator credentials travel between replicas, so TLS or a NetworkPolicy is the operator's job, and the docs say so. A session cookie is accepted by a peer only if every replica shares `GOLDIE_SESSION_KEY`, so `replicas=all` from the UI needs that shared key; a replica with another key shows as down. The cost is N x M connections, so subscribers are capped. This amends Q9: still in memory, but one view across replicas. `replicas`, `replica`, `id`, `late` and `replica_status` become additive-only contract.
