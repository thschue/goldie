# Context

The domain vocabulary of goldie. Use these words, with these meanings, in code, docs, issues and
reviews. The spec is issue #1; decisions that are hard to reverse live in [docs/adr/](docs/adr/).

Goldie is a single binary that serves golden-path knowledge (approved technologies, patterns and
conventions) just in time to humans and agents.

## Content

**Item**: one unit of golden-path knowledge, authored as Markdown with frontmatter in a git Source.
Identified by Kind plus name, so an article and a skill can share a name. The name is the path-derived slug (for an article, its path under `articles/` without `.md`); for an article the frontmatter `name` is only the display title (ADR 0015).

**Kind**: the type of an Item: `article`, `skill`, `agent`, `loop`, `instruction` (a clause) or
`decision`. Each Kind has its own layout and frontmatter within a Scope.

**Scope**: where an Item applies. `global` (everyone), `project` (one repository, under
`projects/<host>/<org>/<repo>/`) or `local` (the repository's own `.goldie/`).

**Project**: one repository, identified as `host/org/repo` (ADR 0020) and matched ignoring case.
It is registered by its `projects/<host>/<org>/<repo>/` directory in a Source, articles or not; a
request naming an unregistered Project gets the global answer with a hint; copy shown to users
calls the global answer "global guidance only".

**Resolution**: the rule that decides which Item wins when several share an identity. Scope first
(local > project > global), then Source preference (first listed wins).

**Enforced Item**: an Item marked `enforced: true`. A narrower Scope cannot shadow it, and losing it
on Source preference is a validation error, so enforcement is never dropped silently.

**Suppression**: a project's declaration, in `project.yaml` or `.goldie/config.yaml`, that a
non-enforced global Item does not apply to it.

**Status**: an Item's lifecycle value: `draft` (hidden from agents, visible in the UI), `published`
or `deprecated` (with `replaced_by`).

**Decision**: an architecture decision (ADR) served as an Item. Its lifecycle is `proposed`,
`accepted`, `rejected`, `deprecated` or `superseded`. Accepted Decisions are binding for agents.

**Catalog**: the compact list of available Items (and Decision titles) injected into an agent's
session so it knows what it can ask goldie for.

**Managed block**: the marker-delimited region goldie writes into `CLAUDE.md` or `AGENTS.md` from
instruction clauses. Content outside it is never touched; a hand-edited block is not overwritten
without `--force`.

**Goldie namespace**: the `goldie-*` paths under `.claude` that `goldie pull` owns and replaces
wholesale. Nothing outside the namespace is ever written.

## Sources and sync

**Source**: a place goldie reads Items from: a `git` repository, a `dir`, or an `upstream` goldie
server. Sources are listed in preference order.

**Claim**: what a Source is allowed to publish (global and/or project globs). Deny by default, so a
team repository cannot publish global guidance or claim another team's project.

**Snapshot**: the validated, immutable view of one Source at one revision. Swapped atomically on
sync; the last good Snapshot keeps serving when a Source breaks.

**Miss**: a search or lookup that found nothing. Misses are the platform team's signal for missing
guidance; they are visible in the activity feed and in telemetry.

## Access

**Session**: a short-lived signed credential goldie issues after login (plus a refresh token).
Browsers hold it only in a hardened cookie.

**Role**: `reader` or `operator`. GitHub org members become readers, members of the configured teams
operators; machine tokens carry a role too.

## Modules

One-way dependencies, enforced by import rules in `.golangci.yml` (see
[ADR 0001](docs/adr/0001-single-binary-with-modules-and-roles.md)):

| Module | Package | Responsibility |
|---|---|---|
| Engine | `internal/engine` | Items, Kinds, validation, digests, keyword search, Resolution, Sources, Snapshots. Pure: it serves no HTTP and owns no inbound transport (that is the API's job). A Source may still be a client of a remote, for example by cloning a git repository. |
| API | `internal/api` | HTTP transport over the Engine: handlers, auth, sessions, activity |
| UI | `internal/ui` | Vue SPA, embedded, talking only to the API |
| MCP | `internal/mcp` | Talks to the API only through the client |
| CLI | `internal/cli` | Talks to the API only through the client |
| Contract | `internal/contract` | The OpenAPI document plus what every API client shares: the hardened client, the cleaning of untrusted server text (`CleanText`, `CleanLine`), reading API answers (`ParseOK`, `DecodeAPIError`), and client-side Project detection (`internal/contract/project`) |
| Platform | `internal/platform` | Config, logging, admin port, readiness, drain, telemetry |
| Composition root | `internal/app` | Wires modules into roles; the only client-side code that may reach the server (embedded server) |

A **role** is what one process does (`server` in mode `all|api|ui|mcp`, `mcp`, or a client command).
