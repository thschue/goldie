# 16. Nested article ids travel as one percent-encoded path segment

Status: proposed

## Context

ADR 0015 makes an article's identity its path under `<scope>/articles/` without `.md`, so ids may
contain `/` (`platform/deploy`). The API looks articles up at `GET /api/v1/articles/{id}` (#70). An
OpenAPI path parameter is exactly one segment, and ADR 0011 freezes the `/api/v1` shape at the first
release, so how a nested id is carried is permanent.

Generated clients (oapi-codegen's `url.PathEscape`, TypeScript's `encodeURIComponent`) send a `/`
inside a path parameter as `%2F`. Go's `net/http` ServeMux matches the escaped path and decodes the
segment for `PathValue`. Reverse proxies treat `%2F` inconsistently: Apache rejects it by default
(`AllowEncodedSlashes Off`), nginx decodes it when `proxy_pass` carries a URI, and some gateways
normalise it.

The alternatives:

- a trailing wildcard `/api/v1/articles/{id...}`, which reads naturally but cannot be expressed in
  OpenAPI path templating, so it breaks route coverage, the conformance checks and generated
  clients;
- a query parameter, `GET /api/v1/article?id=platform/deploy`;
- an id alphabet without `/` (for example `platform.deploy`), which would change ADR 0015's identity.

## Decision

In the API, an article id is one path segment. A `/` inside the id is percent-encoded as `%2F`
(`/api/v1/articles/platform%2Fdeploy`), and the server decodes the segment before lookup. An
unencoded nested path (`/api/v1/articles/platform/deploy`) is not an article lookup and answers the
not-found envelope. The spec's `id` parameter says so. Ids in response bodies are always the decoded
form. Deployment docs (ingress, Helm) must configure proxies to pass `%2F` through unchanged. A test
at the HTTP seam covers a nested id end to end, the bare-slash miss included.

## Consequences

The spec, generated clients, route coverage and the conformance helper stay exact and agree, and one
id maps to one URL.

The cost: a hand-typed or naively joined URL with a bare `/` misses, and its 404 hint is the generic
one rather than the search hint. Operators behind proxies that reject or decode `%2F` must change
their proxy settings, which the Helm chart and docs have to state. Moving to a wildcard or query form
after the first release needs a new API version (ADR 0011).

Rejected: `{id...}` (not expressible in OpenAPI 3.1 paths), a query parameter (loses the
resource-style URL that the UI and MCP links share), and a slash-free id alphabet (would supersede
ADR 0015's path identity).
