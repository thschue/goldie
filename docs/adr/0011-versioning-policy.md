# 11. Versioning policy: additive-only contract

Status: accepted

## Context

Integrators and agents depend on the API and MCP shapes, while the CLI flags, configuration and Helm values are still being discovered.

## Decision

One version covers the binary, the image and the chart, managed by release-please. From the first release the API and MCP contract (`/api/v1`, MCP tool and prompt shapes) is additive-only: fields, endpoints and tools can be added, never removed or changed in meaning; a breaking change needs a new API version. CLI flags, config variables and Helm values may change in `0.x` minor releases until `1.0`, when they freeze too.

## Consequences

Integrations do not break, while the operator-facing surface can still be corrected early. The cost is that a mistake in the contract is permanent, so API and MCP shapes get more scrutiny than anything else, and `0.x` users must read release notes.
