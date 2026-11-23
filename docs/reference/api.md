<!-- Generated from internal/contract/openapi.yaml by `./run regen`. Do not edit. -->

# Goldie API reference

Version 0.1.0. This page documents the public API; the admin endpoints are in the
[platform contract](../platform-contract.md).

The public HTTP API of the goldie server. This document is the contract:
the Go and TypeScript types are generated from it, and it is served by the
server itself at `/openapi.yaml` and `/openapi.json`.

The admin endpoints (`/livez`, `/readyz`, `/version`) are served on the
separate admin port and are not part of this document.

## Endpoints

### `GET /api/v1/activity`

Stream recent searches and lookups

Streams this server process's recent searches and lookups as
server-sent events, for operator tokens only. The stream opens with the
retained events in `id` order, oldest first, then sends each new one as
it is recorded. It is a live view, not an audit log: the buffer is
bounded and shared, so any reader's requests push older events out of
it, and a restart empties it.

Every event is one SSE event whose `data` is one line of JSON:
`id` (a number, increasing), `time` (RFC 3339), `kind` (`search` or
`lookup`), `query` (the search words, or the article id a lookup asked
for), `project` (the registered spelling of the Project, an unregistered
id lower-cased, or `""`), `token` (the name of the token, never its
secret), `client_version` (the `X-Goldie-Client` version, or `""`),
`trace_id` (or `""`) and `miss` (a search with no results, or a lookup
answering 404). All text is escaped as JSON strings, so no query can
end an event or start one.

A subscriber that cannot keep up loses events and then gets an event
of type `dropped` whose data is `{"dropped":<n>}`, the number lost,
before the next event. An idle stream sends an SSE comment every 15
seconds. The stream costs one rate-limit token when it opens, and ends
when the server shuts down. At most 32 streams are open per server
process; one more is a 503 whose hint says to retry later.

Operation ID: `getActivity`

| Status | Description | Body |
|---|---|---|
| `200` | The event stream, not an audit log. Sent with 'Cache-Control: no-store'; the headers are sent at once, before the first event. | `text/event-stream` (`string`) |
| `401` | The bearer token is missing or not a configured token. | `application/json` (`ErrorEnvelope`) |
| `403` | The token is valid but its role is not operator. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server cannot open a stream: it has not loaded its content yet (code 'not_ready'), it already serves the most streams it allows (code 'too_many_streams'), or it is shutting down (code 'shutting_down'). The hint says when to retry. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/articles/{id}`

Look up one article

Returns one article by its id. Ids compare ignoring case. A miss is a
404 whose hint points to search.

Operation ID: `getArticle`

| Status | Description | Body |
|---|---|---|
| `200` | The article. | `application/json` (`Article`) |
| `400` | 'project' is not a 'ProjectId'. | `application/json` (`ErrorEnvelope`) |
| `401` | No bearer token, or a wrong one. | `application/json` (`ErrorEnvelope`) |
| `404` | No article has this id; the hint points to search. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/projects`

List the registered Projects

Returns the Projects the server holds Scope for, sorted, as
`host/org/repo` in the case they are stored in. A server with none
answers an empty list. Pass one as `project` on search and lookup.

Operation ID: `listProjects`

| Status | Description | Body |
|---|---|---|
| `200` | The Projects the server holds, sorted. | `application/json` (`ProjectList`) |
| `401` | No bearer token was sent, or it is wrong. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/search`

Search articles by keyword

Returns the articles that match the search words, best first. An exact
keyword match scores 4, a keyword containing the word 1, and a name
containing the word 2. Ties are ordered by id. No match is an empty
list, not an error.

Operation ID: `searchArticles`

| Status | Description | Body |
|---|---|---|
| `200` | The matching articles, best first. | `application/json` (`SearchResults`) |
| `400` | The search words are missing or longer than 1024 bytes, or 'project' is not a 'ProjectId'. | `application/json` (`ErrorEnvelope`) |
| `401` | The bearer token is missing or wrong. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

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

### `Article`

One article.

| Property | Type | Required | Description |
|---|---|---|---|
| `content` | `string` | yes | The Markdown body after the frontmatter. It comes from a Source and is untrusted; render it without raw HTML. |
| `hint` | `string` | no | Present when the request named a Project that is not registered: the article is the global one, and this says what to do next. |
| `id` | `string` | yes | The article id, its path under 'articles/' without '.md'. |
| `keywords` | `array` | yes | The keywords from the article's frontmatter, lower case. |
| `name` | `string` | yes | The article's display title, from its frontmatter. |

### `Error`

One error. It never contains addresses, paths or stack traces.

| Property | Type | Required | Description |
|---|---|---|---|
| `code` | `string` | yes | A stable, machine-readable error code. |
| `details` | `ErrorDetails` | yes | Next-step hints for the caller. |
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

### `ProjectId`

A Project: exactly three segments `host/org/repo` (ADR 0020). The host
is a lower-case DNS name that starts with a letter or digit; the org and
the repo use letters, digits, `.`, `_` and `-` and do not start with a
dot. Clients send it lower-cased; the server matches it ignoring case.

### `ProjectList`

The Projects the server holds Scope for.

| Property | Type | Required | Description |
|---|---|---|---|
| `projects` | `array` | yes | The Projects, sorted. |

### `SearchHit`

One article that matched a search.

| Property | Type | Required | Description |
|---|---|---|---|
| `id` | `string` | yes | The article id, for a lookup. |
| `keywords` | `array` | yes | The article's keywords, lower case. |
| `kind` | `string` | yes | The Kind of the Item, such as 'article' (ADR 0005). |
| `matched` | `array` | yes | What matched, in order of first match - keywords, and 'name' when the name matched. |
| `name` | `string` | yes | The display title from the frontmatter. |
| `scope` | `string` | yes | Where the Item applies, such as 'global' (ADR 0005). |
| `score` | `integer` | yes | The relevance score; higher is better. |

### `SearchResults`

The result of a search.

| Property | Type | Required | Description |
|---|---|---|---|
| `hint` | `string` | no | Present when the request named a Project that is not registered: the results are the global ones, and this says what to do next. |
| `query` | `string` | yes | The search words as received. |
| `results` | `array` | yes | The matching articles, best first, then by id. |
