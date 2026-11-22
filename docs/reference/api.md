<!-- Generated from internal/contract/openapi.yaml by `./run regen`. Do not edit. -->

# Goldie API reference

Version 0.1.0. This page documents the public API; the admin endpoints are in the
[platform contract](../platform-contract.md).

The public HTTP API of the goldie server. This document is the contract:
the Go and TypeScript types are generated from it, and it is served by the
server itself at `/openapi.yaml` and `/openapi.json`.

The admin endpoints (`/livez`, `/readyz`, `/version`) are served on the
separate admin port and are not part of this document. `/version` is also
answered on the public listener; it is documented with the admin endpoints.

## Endpoints

### `GET /openapi.json`

Get the OpenAPI document as JSON

Returns this document in JSON form. It is equivalent to the YAML form.

Operation ID: `getOpenAPIJSON`

| Status | Description | Body |
|---|---|---|
| `200` | The OpenAPI document in JSON form. | `application/json` (`object`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /openapi.yaml`

Get the OpenAPI document as YAML

Returns this document in YAML form.

Operation ID: `getOpenAPIYAML`

| Status | Description | Body |
|---|---|---|
| `200` | The OpenAPI document in YAML form. | `application/yaml` (`string`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

## Schemas

### `Error`

One error. It never contains addresses, paths or stack traces.

| Property | Type | Required | Description |
|---|---|---|---|
| `code` | `string` | yes | A stable, machine-readable error code. |
| `details` | `ErrorDetails` | yes |  |
| `message` | `string` | yes | A short, human-readable explanation. |

### `ErrorDetails`

Next-step hints for the caller, and nothing else. It never echoes the
request and never contains addresses, ports, hostnames, paths or stack
traces. It is required, so a client can always read it, and always
carries a hint.

| Property | Type | Required | Description |
|---|---|---|---|
| `hint` | `string` | yes | What the caller can do next. |

### `ErrorEnvelope`

The body of every error response.

| Property | Type | Required | Description |
|---|---|---|---|
| `error` | `Error` | yes |  |
