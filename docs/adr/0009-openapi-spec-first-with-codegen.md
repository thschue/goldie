# 9. OpenAPI 3.1, spec first, with code generation

Status: accepted

## Context

The API has several consumers (UI, CLI, MCP, integrators). If the code is the source of truth, the documentation and clients drift from it.

## Decision

The OpenAPI 3.1 document is the contract and is written first. The Go types and server interface and the TypeScript types are generated from it with `./run regen`, and CI fails on drift. Every response in tests is validated against the spec, and a route-coverage test checks that the spec and the router agree in both directions. The spec is served at `/openapi.yaml|json`, with Swagger UI at `/swagger-ui`.

## Consequences

Clients, server and docs cannot disagree, and modules can talk over the contract. The cost is a generation step in the workflow and generated code in the repository, which must never be edited by hand.
