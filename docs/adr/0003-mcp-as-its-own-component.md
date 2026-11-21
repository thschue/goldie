# 3. MCP as its own component

Status: accepted

## Context

Agents reach goldie through MCP. Embedding it in the API would couple its release, scaling and auth surface to the REST handlers.

## Decision

MCP is a separate module and a separate server mode. It talks to the API only through the contract client: in mode `all` over an in-process transport that calls the API handler directly, in separated modes over HTTP to the upstream. The caller's credential is passed through, so activity is attributed to the real caller.

## Consequences

MCP can be scaled, exposed and secured independently, and the API stays the single place where authorisation and Resolution happen. The cost is a second client of the API to keep in step with the contract, which is why the contract is additive-only (ADR 0011).
