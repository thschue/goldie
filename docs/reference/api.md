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

With `replicas=all` the stream merges this replica's stream with one
per peer, as `GOLDIE_ACTIVITY_PEERS` names them; without peers it is
this replica's stream. An event of type `replica_status` (an
`ActivityReplicaStatus`) says a peer is down, and again when it is
back; those for peers that are down when the stream opens come before
the merged history. Each peer is asked with the caller's own token for
its own events only, and checks the operator role itself. A peer is
down when its stream ends or falls silent, or sends no `live` marker.
An event that reached the merge after its reorder window carries
`late: true`, and `dropped` counts the merge's own losses. At most 16
peers are merged; the rest are ignored.

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
`Last-Event-ID`: a reconnect replays the retained history, so on
reconnect clients replace their view with the history before `live`.
Within one connection they dedupe by `replica` and `id`; across
connections ids are no key, because they start again at 1 when a
replica restarts under the same name. An idle stream sends the
comment `: keepalive` every 15 seconds. The stream costs one
rate-limit token when it opens, and ends at the server's first stop
signal. At most 32 streams are open per server process, and at most 8
of them with `replicas=all`, each of which costs a stream on every
peer; one more is a 503 whose hint says to retry later. The history
before `live` holds at most 10,000 events per replica, so a merged
history can be larger, and the Go client reads at most 16 MiB of the
stream before `live`.

Go clients use `StreamActivity` on the client `NewAPIClient` builds:
`GetActivityWithResponse` reads the whole body, so it does not return
until the stream ends.

Operation ID: `getActivity`

| Status | Description | Body |
|---|---|---|
| `200` | The event stream, not an audit log. Sent with 'Cache-Control: no-store'; the headers are sent before the first event, at once, except with 'replicas=all', which first waits up to a few seconds for its peers. | `text/event-stream` (`string`) |
| `400` | 'replicas' is not 'all', or is repeated. | `application/json` (`ErrorEnvelope`) |
| `401` | The bearer token is missing or not a configured token. | `application/json` (`ErrorEnvelope`) |
| `403` | The token is valid but its role is not operator. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server cannot open a stream: it has not loaded its content yet (code 'not_ready'), it already serves the most streams it allows (code 'too_many_streams'), or it is shutting down (code 'shutting_down'). The hint says when to retry. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/agents`

List the agents

Returns the agents that answer in the Project's view, sorted by name,
one per name: the nearest Scope wins.

Operation ID: `listAgents`

| Status | Description | Body |
|---|---|---|
| `200` | The agents. | `application/json` (`AgentList`) |
| `400` | For the agent list, 'project' is not a 'ProjectId'. | `application/json` (`ErrorEnvelope`) |
| `401` | For the agent list, no bearer token was sent, or it is wrong. | `application/json` (`ErrorEnvelope`) |
| `403` | For the agent list, drafts were asked for with a token whose role is not operator. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/agents/{name}`

Read one agent

Returns one agent by name. A miss is a 404 whose hint points to the list.

Operation ID: `getAgent`

| Status | Description | Body |
|---|---|---|
| `200` | The agent. | `application/json` (`Agent`) |
| `400` | For the agent read, 'project' is not a 'ProjectId'. | `application/json` (`ErrorEnvelope`) |
| `401` | For the agent read, no bearer token was sent, or it is wrong. | `application/json` (`ErrorEnvelope`) |
| `403` | For the agent read, drafts were asked for with a token whose role is not operator. | `application/json` (`ErrorEnvelope`) |
| `404` | No agent has this name; the hint points to the agent list. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
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

### `GET /api/v1/instructions`

Read the instruction block

Returns the instruction block for the Project and target: the clauses that
resolve in the Project's view and name the target, in order, with the
variables replaced. `{{mcp_name}}` is the server setting
`GOLDIE_MCP_NAME` (default `goldie`) and `{{cli}}` is `goldie`. When no
clause applies, the block is the built-in default. The `digest` is the
hex SHA-256 of the block, so a client can tell whether its copy is current.

Operation ID: `getInstructions`

| Status | Description | Body |
|---|---|---|
| `200` | The block and its digest. | `application/json` (`InstructionBlock`) |
| `400` | For the instruction block, 'target' is missing or not a known target (the hint names them), or 'project' is not a 'ProjectId'. | `application/json` (`ErrorEnvelope`) |
| `401` | For the instruction block, no bearer token was sent, or it is wrong. | `application/json` (`ErrorEnvelope`) |
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
`<kind>:<id>` (for example `article:style` or `agent:reviewer`), sorted. A Project that suppresses nothing answers an
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

### `GET /api/v1/projects/{id}/items`

Export a Project's resolved view

Returns the articles that win in the Project's view (Resolution): the
Project's own copy over the global one with the same id, every global
Item the Project does not shadow or suppress, and no draft. Only
winners are sent: nothing of another Project and no shadowed copy.
Sorted by `id`, ascending. Each Item carries its `scope` and whether it
is `enforced`, so an embedded server can build on this view and check
that a local Item does not shadow an enforced one. The Project's
Suppressions (ADR 0021) are listed as in `getProject`.

A Project the server does not hold is answered with the global view and
a `hint`, as search and lookup do. A view over the export limit, the
same 16 MiB the goldie client reads, is refused whole with 422
`view_too_large`; it is never truncated, because a partial view would
hide enforced Items.

Operation ID: `getProjectItems`

| Status | Description | Body |
|---|---|---|
| `200` | The Project's resolved view. | `application/json` (`ProjectView`) |
| `400` | The id sent is not a valid 'ProjectId'; the body does not repeat it. | `application/json` (`ErrorEnvelope`) |
| `401` | No bearer token, or a wrong one; the body shows no Item. | `application/json` (`ErrorEnvelope`) |
| `422` | The view is over the export limit; the error code is 'view_too_large'. Nothing of the view is sent. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/search`

Search articles, skills and agents by keyword

Returns the Items of one Kind that match the search words, best first.
Without `kind` only articles are searched, so a client that follows a hit
with an article lookup keeps working. An exact keyword match scores 4, a
keyword containing the word 1, and a name containing the word 2. A skill
or agent matches on its name and keywords only, never on its
description (ADR 0005); a skill's keywords are the `keywords` in the
frontmatter of its `SKILL.md`. Ties are ordered by id. No match is an
empty list, not an error. A skill or agent hit's `id` is its name: read
it with `GET /api/v1/skills/{name}` or `GET /api/v1/agents/{name}`.

Operation ID: `searchArticles`

| Status | Description | Body |
|---|---|---|
| `200` | The matching Items, best first. | `application/json` (`SearchResults`) |
| `400` | The search words are missing or longer than 1024 bytes, 'kind' is not 'article', 'skill' or 'agent', or 'project' is not a 'ProjectId'. | `application/json` (`ErrorEnvelope`) |
| `401` | The bearer token is missing or wrong. | `application/json` (`ErrorEnvelope`) |
| `403` | The search asked for drafts with a token whose role is not operator. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `DELETE /api/v1/session`

Sign out

Clears the session cookie. A cross-origin request is refused with a
403, so another site cannot sign a user out. The response is
`Cache-Control: no-store`.

Operation ID: `deleteSession`

| Status | Description | Body |
|---|---|---|
| `204` | The cookie is cleared in 'Set-Cookie'. | none |
| `403` | The sign-out came from another origin, so the cookie is kept. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/session`

Read who the session cookie signs in

Returns the name and current role of the token behind a valid
session cookie (or a bearer token); a token removed from the
configuration ends its sessions. Any other request is a 401. The
response is `Cache-Control: no-store`.

Operation ID: `getSession`

| Status | Description | Body |
|---|---|---|
| `200` | The caller. With a session cookie, the CSRF token is in 'X-Goldie-CSRF'. | `application/json` (`Session`) |
| `401` | No valid session cookie or bearer token was sent. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `POST /api/v1/session`

Sign in with a token and get a session cookie

Exchanges a configured API token, sent as `token` in the JSON body,
for a signed session cookie, so a browser never holds the token. The
cookie is `HttpOnly`, `SameSite=Strict` and `Path=/`, and over TLS
`Secure` with the `__Host-` prefix (both dropped only on the
plain-http loopback dev server, `goldie server --dev`; any other
server, one behind a TLS proxy included, always sets both). It names
the token and its times under a signature (ADR 0027), expires after 30
minutes without use (each request with the cookie extends it) and 7
days after sign-in at the latest, and ends when the token is removed or
given a new value. It authenticates `GET` and `HEAD`. It authenticates
an unsafe method only from a same-origin request that also sends the
CSRF token (`X-Goldie-CSRF`, returned here and by `GET
/api/v1/session`); a cross-site `Origin` or `Sec-Fetch-Site`, or a
missing or wrong token, is a 403. The client sends the token back in an
`X-Goldie-CSRF` request header on each unsafe request. A bearer token
beats the cookie, and a bearer caller gets and needs no CSRF token.
This operation takes no bearer token, and a
cross-origin request is refused with a 403. Failed sign-ins are
capped per remote address: past the cap within a window, every
sign-in from that address, a valid token included, is refused with a
429 until the window ends. The key is the connection's remote
address (an IPv6 address by its /64); `X-Forwarded-For` and similar
headers are ignored, so clients behind a reverse proxy share one key
and one lockout. The cap is per server process, and successful
sign-ins do not count. The server tracks a bounded number of
addresses; past it, new addresses share one count, so a flood of
addresses can lock sign-in for addresses not yet tracked. The
response is `Cache-Control: no-store`.

Operation ID: `postSession`

| Status | Description | Body |
|---|---|---|
| `200` | The token is valid; the session cookie is in 'Set-Cookie' and the CSRF token in 'X-Goldie-CSRF'. | `application/json` (`Session`) |
| `401` | The body holds no configured token. | `application/json` (`ErrorEnvelope`) |
| `403` | The request came from another origin. | `application/json` (`ErrorEnvelope`) |
| `429` | This remote address made too many failed sign-ins. The answer is the same for a valid and an invalid token. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/skills`

List the skills

Returns the skills that answer in the Project's view, sorted by name, one
per name: the nearest Scope wins with its whole bundle. Each carries its
file list.

Operation ID: `listSkills`

| Status | Description | Body |
|---|---|---|
| `200` | The skills. | `application/json` (`SkillList`) |
| `400` | For the skill list, 'project' is not a 'ProjectId'. | `application/json` (`ErrorEnvelope`) |
| `401` | For the skill list, no bearer token was sent, or it is wrong. | `application/json` (`ErrorEnvelope`) |
| `403` | For the skill list, drafts were asked for with a token whose role is not operator. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/skills/{name}`

Read one skill

Returns one skill by name with its file list. A client reads each listed
`files[].path` with `GET /api/v1/skills/{name}/files/{path}`. A miss is a
404 whose hint points to the list. The digest and the executable bit
are not part of the answer yet.

Operation ID: `getSkill`

| Status | Description | Body |
|---|---|---|
| `200` | The skill. | `application/json` (`Skill`) |
| `400` | For the skill read, 'project' is not a 'ProjectId'. | `application/json` (`ErrorEnvelope`) |
| `401` | For the skill read, no bearer token was sent, or it is wrong. | `application/json` (`ErrorEnvelope`) |
| `403` | For the skill read, drafts were asked for with a token whose role is not operator. | `application/json` (`ErrorEnvelope`) |
| `404` | No skill has this name; the hint points to the skill list. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/skills/{name}/files/{path}`

Read one file of a skill

Returns the bytes of one file of the skill's bundle exactly as written,
for a path listed in the skill's `files`. Every file is served with the
one content type `application/octet-stream` and
`X-Content-Type-Options: nosniff`, whatever its name or content, so a
client or browser never runs or renders it. A name or path that is not a
file of the bundle that answers, one that leaves the bundle, and an
absolute path are all the same 404.

Operation ID: `getSkillFile`

| Status | Description | Body |
|---|---|---|
| `200` | The file's bytes. | `application/octet-stream` (`string`) |
| `400` | For the skill file, 'project' is not a 'ProjectId'. | `application/json` (`ErrorEnvelope`) |
| `401` | For the skill file, no bearer token was sent, or it is wrong. | `application/json` (`ErrorEnvelope`) |
| `403` | For the skill file, drafts were asked for with a token whose role is not operator. | `application/json` (`ErrorEnvelope`) |
| `404` | No such skill, or the path is not a file of its bundle; the hint points to the skill list and the skill's files. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | The token sent more requests than 'GOLDIE_RATE_LIMIT' allows. The limit is per token name and per server process; requests without a valid token are rejected with 401 first and never count. Wait the seconds in 'Retry-After', then retry. | `application/json` (`ErrorEnvelope`) |
| `503` | The server has not loaded its content yet, so it cannot answer; the error code is 'not_ready'. Retry shortly. Once this server process has loaded its content it never answers this again, even if a later load fails; a restarted or new process starts unloaded. | `application/json` (`ErrorEnvelope`) |
| `default` | An error, in the error envelope. | `application/json` (`ErrorEnvelope`) |

### `GET /api/v1/sync`

Read the content sync status and warnings

Returns, per Source, the revision served and when it was last polled,
when that last succeeded and why it last failed, and the findings of the
last content load that did not fail it, for
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

### `POST /api/v1/sync`

Poll the Source now

Starts one poll of the Source now instead of waiting for
`GOLDIE_POLL_INTERVAL`, for operator tokens only, and answers at once
without waiting for it, for example after merging into the repository a
git Source reads. Read the outcome from `GET /api/v1/sync`: `last_poll`
moves when the poll ends, `revision` when it found a new commit, and
`last_error` when it failed. The request takes no body. At most one
poll runs at a time.

Operation ID: `postSync`

| Status | Description | Body |
|---|---|---|
| `202` | A poll was started. | `application/json` (`SyncTriggered`) |
| `401` | No bearer token was sent to trigger a poll, or it is not a configured token. | `application/json` (`ErrorEnvelope`) |
| `403` | A reader token was sent to trigger a poll; only an operator token may. | `application/json` (`ErrorEnvelope`) |
| `426` | The request named a goldie client older than 'GOLDIE_MIN_CLIENT_VERSION' in its 'X-Goldie-Client: goldie/&lt;version&gt;' header. The hint names the minimum version: upgrade goldie, then retry. Requests without the header, or whose header is not 'goldie/&lt;semver&gt;', are not refused; requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. | `application/json` (`ErrorEnvelope`) |
| `429` | A poll is already running, so none was started ('error.code' is 'sync_running'), or the token sent more requests than 'GOLDIE_RATE_LIMIT' allows ('rate_limited', with 'Retry-After'). Wait, then read 'GET /api/v1/sync'. | `application/json` (`ErrorEnvelope`) |
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
| `kind` | `string` | yes | A search, an article lookup, or a get of a skill or an agent. |
| `late` | `boolean` | yes | The event reached a merge of replicas' streams after its reorder window, so it is out of time order. Always 'false' on a replica's own stream. |
| `miss` | `boolean` | yes | A search with no results, or a lookup answering 404. |
| `project` | `string` | yes | The registered spelling of the Project, an unregistered id lower-cased, or '""'. |
| `query` | `string` | yes | The search words, the article id a lookup asked for, or the skill or agent name a get asked for, as text. |
| `replica` | `string` | yes | The name of the replica that recorded the event: the first label of its host name (a StatefulSet pod name), or 'goldie'. Never an address. |
| `time` | `string` | yes | When the server recorded the request. |
| `token` | `string` | yes | The name of the token that sent the request, never its secret. |
| `trace_id` | `string` | yes | The request's trace id, or '""' without a span. |

### `ActivityLive`

The `data` of the one SSE event of type `live`: the events before it
are the history, the events after it are live.

### `ActivityReplicaStatus`

The `data` of an SSE event of type `replica_status`, on a stream with
`replicas=all`: a peer is down, or up again. A peer that is up when
the stream opens gets none. Clients ignore fields they do not know.

| Property | Type | Required | Description |
|---|---|---|---|
| `last_seen` | `string` | yes | When the merge last heard from the peer, or attached it. |
| `replica` | `string` | yes | The name of the peer, the first label of its pod name. Never an address. |
| `up` | `boolean` | yes | Whether the peer's stream is open. |

### `Agent`

One agent definition.

| Property | Type | Required | Description |
|---|---|---|---|
| `description` | `string` | yes | The frontmatter description. It comes from a Source and is untrusted. |
| `hint` | `string` | no | Present when the request named a Project that is not registered: the agent answer is the global one, and this says what to do next. |
| `name` | `string` | yes | The agent's name, its file name without '.md'. |

### `AgentList`

The agents that answer in a view.

| Property | Type | Required | Description |
|---|---|---|---|
| `agents` | `array` | yes | The agents, sorted by name. |
| `hint` | `string` | no | Present when the request named a Project that is not registered: the agent list answer is the global one, and this says what to do next. |

### `Article`

One article.

| Property | Type | Required | Description |
|---|---|---|---|
| `content` | `string` | yes | The Markdown body after the frontmatter. It comes from a Source and is untrusted; render it without raw HTML. |
| `enforced` | `boolean` | no | Whether the article is enforced (frontmatter 'enforced: true'), so no narrower Scope can shadow it. Always sent by this server; absent from an older server, where it means unknown, not false. |
| `hint` | `string` | no | Present when the request named a Project that is not registered: the article is the global one, and this says what to do next. |
| `id` | `string` | yes | The article id, its path under 'articles/' without '.md'. |
| `keywords` | `array` | yes | The keywords from the article's frontmatter, lower case. |
| `name` | `string` | yes | The article's display title, from its frontmatter. |
| `overrides` | `array` | no | The Scopes this article shadows in the requested view, nearest first; '[]' when it shadows nothing. Never null. Always sent by this server; absent from an older server, where it means unknown, not empty. |
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

### `InstructionBlock`

The rendered instruction block for a Project and target.

| Property | Type | Required | Description |
|---|---|---|---|
| `block` | `string` | yes | The block text, variables replaced. It is derived from Source content and is untrusted. |
| `digest` | `string` | yes | The hex SHA-256 of 'block'. |
| `hint` | `string` | no | Present when the request named a Project that is not registered: the block is the global one, and this says what to do next. |

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
| `suppressions` | `array` | yes | The global Items the Project suppresses, as '&lt;kind&gt;:&lt;id&gt;' (for example 'article:style' or 'agent:reviewer'), sorted. A reference that names nothing is not listed. Empty when there are none. |

### `ProjectId`

A Project: exactly three segments `host/org/repo` (ADR 0020). The host
is a lower-case DNS name that starts with a letter or digit; the org and
the repo use letters, digits, `.`, `_` and `-` and do not start with a
dot. Clients send it lower-cased; the server matches it ignoring case.

### `ProjectItem`

One winning Item of a Project's view.

| Property | Type | Required | Description |
|---|---|---|---|
| `content` | `string` | yes | The Markdown body of the Item, after the frontmatter. Untrusted, it comes from a Source; render it without raw HTML. |
| `enforced` | `boolean` | yes | Whether the Item is enforced, as its frontmatter says. |
| `id` | `string` | yes | The Item id, its path under 'articles/' without '.md'. |
| `keywords` | `array` | yes | The Item keywords from its frontmatter, lower case. |
| `name` | `string` | yes | The Item display title, from its frontmatter. |
| `replaced_by` | `string` | no | The reference to the Item that replaces this one, as written in its frontmatter, '&lt;kind&gt;:&lt;name&gt;' (ADR 0021). Present only when the Item names a replacement. It comes from a Source and is untrusted. |
| `scope` | `string` | yes | 'global', or the Project ('host/org/repo') whose copy won. |
| `status` | `string` | yes | The Item's lifecycle, always present. Clients ignore values they do not know. |

### `ProjectList`

The Projects the server holds Scope for.

| Property | Type | Required | Description |
|---|---|---|---|
| `projects` | `array` | yes | The Projects, sorted. |

### `ProjectView`

A Project's resolved view, the winning Items and what it suppresses.

| Property | Type | Required | Description |
|---|---|---|---|
| `hint` | `string` | no | Present when the request named a Project that is not registered: the view is the global one, and this says what to do next. |
| `items` | `array` | yes | The winning Items, sorted by 'id', ascending. No draft. |
| `suppressions` | `array` | yes | The global Items the Project suppresses, as '&lt;kind&gt;:&lt;id&gt;', sorted. Empty when there are none. |

### `SearchHit`

One Item that matched a search.

| Property | Type | Required | Description |
|---|---|---|---|
| `enforced` | `boolean` | no | Whether the hit is enforced (frontmatter 'enforced: true'), so no narrower Scope can shadow it. Always sent by this server; absent from an older server, where it means unknown, not false. |
| `id` | `string` | yes | The Item's id, an article id for a lookup or a skill or agent name. |
| `keywords` | `array` | yes | The Item's keywords, lower case. |
| `kind` | `string` | yes | The Kind of the Item, such as 'article' (ADR 0005). |
| `matched` | `array` | yes | What matched, in order of first match - keywords, and 'name' when the name matched. |
| `name` | `string` | yes | The display title from the frontmatter of an article. A skill's or agent's name is its id. |
| `overrides` | `array` | no | The Scopes this hit shadows in the requested view, nearest first; '[]' when it shadows nothing. Never null. Always sent by this server; absent from an older server, where it means unknown, not empty. |
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
| `results` | `array` | yes | The matching Items, best first, then by id. |

### `Session`

Who a session signs in.

| Property | Type | Required | Description |
|---|---|---|---|
| `name` | `string` | yes | The name of the token behind the session. |
| `role` | `string` | yes | The token's role, read from the configuration now. Clients ignore values they do not know. |

### `SessionRequest`

The token to sign in with.

| Property | Type | Required | Description |
|---|---|---|---|
| `token` | `string` | yes | A configured API token, in plain text. |

### `Skill`

One skill bundle.

| Property | Type | Required | Description |
|---|---|---|---|
| `files` | `array` | yes | The bundle's files, sorted by path, SKILL.md included. |
| `hint` | `string` | no | Present when the request named a Project that is not registered: the skill answer is the global one, and this says what to do next. |
| `name` | `string` | yes | The bundle's directory name. |

### `SkillFile`

One file of a skill bundle.

| Property | Type | Required | Description |
|---|---|---|---|
| `path` | `string` | yes | The slash path relative to the bundle. |
| `size` | `integer` | yes | The file's size in bytes. |

### `SkillList`

The skills that answer in a view.

| Property | Type | Required | Description |
|---|---|---|---|
| `hint` | `string` | no | Present when the request named a Project that is not registered: the skill list answer is the global one, and this says what to do next. |
| `skills` | `array` | yes | The skills, sorted by name. |

### `SourceSyncStatus`

How polling one Source is going.

| Property | Type | Required | Description |
|---|---|---|---|
| `last_error` | `string` | no | Why the last poll failed, absent when it succeeded. It names a file relative to the Source root and never a host path, URL or credential. The served content stays the last good one. |
| `last_poll` | `string` | no | When the last poll ended, whether it succeeded or not. Absent before the first poll ends. |
| `last_success` | `string` | no | When the last poll that loaded the content ended. Absent before the first one. |
| `revision` | `string` | no | The commit the served content comes from. Absent for a Source without revisions, such as a directory. |

### `SyncStatus`

How the last content load went, for operators.

| Property | Type | Required | Description |
|---|---|---|---|
| `sources` | `array` | yes | One entry per Source. This server has exactly one. |
| `warnings` | `array` | yes | Load findings that did not fail the load, in load order. |

### `SyncTriggered`

The answer to starting a poll.

| Property | Type | Required | Description |
|---|---|---|---|
| `hint` | `string` | yes | What to do next. |
