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
retained events in `id` order, oldest first, then one event of type
`live` whose `data` is `{}` (an `ActivityLive`), sent even when there
is no history, then each new event as it is recorded. It is a live
view, not an audit log: the buffer is bounded and shared, so any
reader's requests push older events out of it, and a restart empties
it.

Every event is one SSE event whose `data` is one line of JSON, an
`ActivityEvent`. Its `id` increases per server process (per replica)
and starts again at 1 when the process restarts; `replica` names the
replica that recorded it. All text is escaped
as JSON strings, so no query can end an event or start one. A
subscriber that cannot keep up loses events and then gets an event of
type `dropped`, an `ActivityDropped` with the number lost, before the
next event. Clients must ignore SSE event types and JSON fields they
do not know.

Events carry no SSE `id:` field and the server does not resume from
`Last-Event-ID`: a reconnect replays the retained history, so clients
dedupe by `replica` and `id` (ids repeat across replicas). An idle
stream sends the comment `: keepalive` every 15 seconds. The stream costs one rate-limit token when it opens, and ends
at the server's first stop signal. At most 32 streams are open per
server process; one more is a 503 whose hint says to retry later.

Go clients use `StreamActivity` on the client `NewAPIClient` builds:
`GetActivityWithResponse` reads the whole body, so it does not return
until the stream ends.

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
| `403` | The lookup asked for drafts with a token whose role is not operator. | `application/json` (`ErrorEnvelope`) |
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

### `GET /api/v1/projects/{id}`

Read one registered Project

Returns the Project's id, in the case the server stores it, and the
global Items its `project.yaml` suppresses (ADR 0021) as
`article:<id>`, sorted. A Project that suppresses nothing answers an
empty list. A lookup of a suppressed Item answers 404 with a hint that
names the Suppression and the file that sets it, and search leaves it
out.

Operation ID: `getProject`

| Status | Description | Body |
|---|---|---|
| `200` | The Project. | `application/json` (`ProjectDetail`) |
| `400` | The id is not a 'ProjectId'. | `application/json` (`ErrorEnvelope`) |
| `401` | No bearer token, or a wrong one; the body shows no Suppressions. | `application/json` (`ErrorEnvelope`) |
| `404` | The server holds no such Project; the hint says how to register it. | `application/json` (`ErrorEnvelope`) |
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
| `403` | The search asked for drafts with a token whose role is not operator. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/sync`

Read the content sync warnings

Returns the findings of the last content load that did not fail it, for
operator tokens only, such as a `suppress` reference that names nothing
in a Project's `project.yaml`. Each warning names a file relative to the
Source root and shows what the Source wrote escaped to printable ASCII
and cut, which is why only operators read them. `goldie validate` prints
the same warnings for a content directory.

Operation ID: `getSync`

| Status | Description | Body |
|---|---|---|
| `200` | The warnings of the current content, empty when there are none. | `application/json` (`SyncStatus`) |
| `401` | No bearer token was sent for the sync status, or it is not a configured token. | `application/json` (`ErrorEnvelope`) |
| `403` | A reader token was sent for the sync status; only an operator token may read it. | `application/json` (`ErrorEnvelope`) |
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

### `ActivityDropped`

The `data` of an SSE event of type `dropped`: this subscriber lost
events because it did not keep up. Clients ignore fields they do not
know.

| Property | Type | Required | Description |
|---|---|---|---|
| `dropped` | `integer` | yes | How many events were lost before the next one. |

### `ActivityEvent`

One search or lookup on the activity stream, the `data` of an SSE
event without a type. Every field is always sent; text fields are `""`
when empty. Clients ignore fields they do not know.

| Property | Type | Required | Description |
|---|---|---|---|
| `client_version` | `string` | yes | The version in 'X-Goldie-Client: goldie/&lt;version&gt;', or '""'. |
| `id` | `integer` | yes | Increases per server process (per replica); starts again at 1 on restart. |
| `kind` | `string` | yes | A search or a lookup. |
| `late` | `boolean` | yes | The event reached a merge of replicas' streams after its reorder window, so it is out of time order. Always 'false' on a replica's own stream. |
| `miss` | `boolean` | yes | A search with no results, or a lookup answering 404. |
| `project` | `string` | yes | The registered spelling of the Project, an unregistered id lower-cased, or '""'. |
| `query` | `string` | yes | The search words, or the article id a lookup asked for, as text. |
| `replica` | `string` | yes | The name of the replica that recorded the event: the first label of its host name (a StatefulSet pod name), or 'goldie'. Never an address. |
| `time` | `string` | yes | When the server recorded the request. |
| `token` | `string` | yes | The name of the token that sent the request, never its secret. |
| `trace_id` | `string` | yes | The request's trace id, or '""' without a span. |

### `ActivityLive`

The `data` of the one SSE event of type `live`: the events before it
are the history, the events after it are live.

### `Article`

One article.

| Property | Type | Required | Description |
|---|---|---|---|
| `content` | `string` | yes | The Markdown body after the frontmatter. It comes from a Source and is untrusted; render it without raw HTML. |
| `hint` | `string` | no | Present when the request named a Project that is not registered: the article is the global one, and this says what to do next. |
| `id` | `string` | yes | The article id, its path under 'articles/' without '.md'. |
| `keywords` | `array` | yes | The keywords from the article's frontmatter, lower case. |
| `name` | `string` | yes | The article's display title, from its frontmatter. |
| `replaced_by` | `string` | no | The reference to the Item that replaces this article, as written in its frontmatter, '&lt;kind&gt;:&lt;name&gt;' (ADR 0021). Present only when the article is not published (deprecated or draft) and names a replacement. It comes from a Source and is untrusted: clean it before printing it. |
| `status` | `ItemStatus` | no |  |

### `ContentWarning`

One finding of a content load that did not fail it, such as a `suppress`
reference that names nothing. It shows what a Source wrote, escaped to
printable ASCII and cut, which is why only operators read it.

| Property | Type | Required | Description |
|---|---|---|---|
| `file` | `string` | yes | The file the finding is about, relative to the Source root. |
| `message` | `string` | yes | What is wrong, printable ASCII, at most 1 KiB with the file. |

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

### `ItemStatus`

An Item's lifecycle when it is not published: `deprecated` (still
served; read `replaced_by` instead when present) or `draft` (only with
`drafts=true`, for operators). Absent for a published Item. Clients
ignore values they do not know.

### `ProjectDetail`

One registered Project and what its `project.yaml` suppresses.

| Property | Type | Required | Description |
|---|---|---|---|
| `id` | `ProjectId` | yes |  |
| `suppressions` | `array` | yes | The global Items the Project suppresses, as 'article:&lt;id&gt;', sorted. A reference that names nothing is not listed. Empty when there are none. |

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
| `replaced_by` | `string` | no | The reference to the Item that replaces this one, as written in its frontmatter, '&lt;kind&gt;:&lt;name&gt;' (ADR 0021). Present only when the Item is not published (deprecated or draft) and names a replacement. It comes from a Source and is untrusted. |
| `scope` | `string` | yes | Where the Item applies, such as 'global' (ADR 0005). |
| `score` | `integer` | yes | The relevance score; higher is better. |
| `status` | `ItemStatus` | no |  |

### `SearchResults`

The result of a search.

| Property | Type | Required | Description |
|---|---|---|---|
| `hint` | `string` | no | Present when the request named a Project that is not registered: the results are the global ones, and this says what to do next. |
| `query` | `string` | yes | The search words as received. |
| `results` | `array` | yes | The matching articles, best first, then by id. |

### `SyncStatus`

How the last content load went, for operators.

| Property | Type | Required | Description |
|---|---|---|---|
| `warnings` | `array` | yes | Load findings that did not fail the load, in load order. |
