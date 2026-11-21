# 2. Clients are pure HTTP clients; local mode is an embedded server

Status: accepted

## Context

The CLI could contain its own copy of Resolution to work offline or against a directory, which would make local and remote results drift apart.

## Decision

The CLI and MCP talk to the API only through the contract client. Local mode (`--dir`, or `.goldie/` content) starts an embedded server on loopback, using a `dir` Source, or an `upstream` Source when a central server is configured, with `.goldie/` as the local Scope. The CLI never contains Resolution logic. Only the composition root may reach the server from the client side.

## Consequences

Local and remote give the same results for the same content, and there is exactly one implementation of Resolution. The cost is that even a purely local run starts an HTTP server, so startup and port handling must be cheap and safe (loopback only).
