# 1. A single binary with modules and roles

Status: accepted

## Context

Goldie has to run as one small Deployment for a team and as separately scaled components for a platform. Separate binaries or repositories would multiply releases, versions and images.

## Decision

Ship one binary, `goldie`, and one image. Roles are subcommands (`server` with `GOLDIE_MODE` = `all | api | ui | mcp`, `mcp`, and the client commands). Inside the binary, modules (engine, api, ui, mcp, cli, contract, platform) have one-way dependencies enforced by `depguard` import rules in `.golangci.yml`, proven by a deliberate-violation fixture. A composition root, `internal/app`, wires modules into roles.

## Consequences

One version and one artifact to operate, and boundaries that are checked by the linter rather than by convention. In exchange every module compiles into every role, and a dependency rule that is too loose erodes the design silently, so the rules and their fixture are part of the contract and are protected paths.
