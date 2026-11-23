# goldie

Goldie is a single binary that serves golden-path knowledge (approved technologies, patterns and
conventions) just in time to humans and agents. Items are authored as Markdown in git Sources and
reach people through a server (API, UI, MCP), a CLI and managed instruction blocks.

Goldie is under construction; the spec is issue #1. Start with [CLAUDE.md](CLAUDE.md),
[CONTEXT.md](CONTEXT.md) and [CONTRIBUTING.md](CONTRIBUTING.md).

```sh
./run check     # format, build, lint, import-rule fixture, OpenAPI lint and tests
```

The server publishes its OpenAPI 3.1 contract on the public listener at `/openapi.yaml` and
`/openapi.json`, behind the same Bearer token as the API (at least the `reader` Role). The source
is `internal/contract/openapi.yaml`; `./run lint-spec` lints it. Swagger UI at `/swagger-ui`
(`GOLDIE_SWAGGER_UI_ENABLED=false` turns it off) takes the same token. A browser does not send it by
itself: put a reverse proxy or a browser extension in front that adds `Authorization: Bearer <token>`
to every request, the page's fetch of `/openapi.json` included.

## CLI

`goldie search <words>`, `goldie lookup <id>`, `goldie status` and `goldie version` talk to a
running server through the generated client, or an embedded one over local content (below). Each
takes `--addr` (or `GOLDIE_ADDR`), `--token` (or `GOLDIE_TOKEN`, or `GOLDIE_TOKEN_FILE` to read it
from a file) and `--json`. Prefer `GOLDIE_TOKEN` or `GOLDIE_TOKEN_FILE` to `--token`: a `--token`
value shows in the process list (`ps`) of the machine. The CLI follows no redirects, so a server
cannot send the token elsewhere, and every request carries `X-Goldie-Client: goldie/<version>`.
`status` reports whether the server answers and accepts the token (`--json` adds `tokenGiven`) and
which Project the CLI works for; without an accepted token it cannot read the API document, so it
reports the API as unknown, and `version` leaves out the server's API version. `search`, `lookup`
and `status` take `--project`, and `search` and `lookup` send the resolved Project to the server as
`project=`. If the server does not have that Project registered, it answers with the global results
plus a hint, which the CLI prints (and carries in `--json`, for `status` too). An unusable Project
(a broken `GOLDIE_PROJECT`, `--project` or `.goldie/config.yaml`) is an error before any request is
made for `search` and `lookup`; `status` instead reports it on its `Project:` line and exits 0
(`--json` adds `projectError`). If a git remote `upstream` or `origin` exists but is not in
`host/org/repo` form and nothing else gives a Project, `status` names that remote (never its URL) on
the `Project:` line, and `--json` adds `projectHint`. The Project is `--project`, else
`GOLDIE_PROJECT`, else `project:` in the repository's own `.goldie/config.yaml`, else the `upstream`
(or `origin`) git remote, normalised to `host/org/repo`. The remote is read from `.git/config`; git
need not be installed, and credentials in a remote URL are never printed. The sync state arrives
with the sync work (#6).

`goldie search`, `lookup`, `status` and `mcp` also work without a server: `--dir PATH` (or
`GOLDIE_DIR`) starts an embedded server over a directory of content (`global/articles` under it) on
loopback, with its own random token, and stops it when the command ends. Output is the same as
against a remote server over the same content. `--dir` overrides `GOLDIE_DIR`, and also a
`GOLDIE_ADDR` from the environment; `GOLDIE_TOKEN` is ignored. `--dir` or `GOLDIE_DIR` together with
`--addr`, or `GOLDIE_DIR` and `GOLDIE_ADDR` both set in the environment, is an error naming both.
Content that does not load stops the command with `content does not load: <reason>`. When
`search`, `lookup`, `status` or an `mcp` tool cannot reach the embedded server, the error says so and
does not mention `--addr` or `GOLDIE_ADDR`. `version` and `init` ignore `--dir`: `init` checks
registration only against a running server.

A `200 OK` that is not a goldie API answer, such as a proxy's login page, is reported as "the
server's answer isn't a goldie API answer; check the address", by the CLI and by `mcp` alike, and
nothing of the page is shown.

`goldie init [--project ID]` sets a repository up: it finds the Project as `status` does and writes
`project: <id>` to `.goldie/config.yaml` at the repository root (the nearest directory with a
`.git`), then prints the Project and how it was found, the file it wrote, and whether the server
has the Project registered (`yes`, `no` with a hint on how to register it, or `not checked` with the
reason: no address, no token, or a rejected token). The check is one `GET /api/v1/projects` and
ignores case; without a token it sends no request. `init` takes no `--json`. It never overwrites an
existing `.goldie/config.yaml`, never follows a symlinked `.goldie` or `config.yaml`, and refuses to
run outside a git repository or in a Source repository (`global/articles` or `projects/` at its
root); a refusal writes nothing.

```sh
export GOLDIE_ADDR=http://localhost:8080 GOLDIE_TOKEN_FILE=/run/secrets/goldie-token
goldie search kubernetes
goldie lookup platform/deploy
```

## Try it on your own content

`goldie server -dev [dir]` runs the server on 127.0.0.1 over `dir` (else `GOLDIE_CONTENT_DIR`, else
the working directory) with a random token made for this run, and prints the line that points the
client commands at it. It needs no other setup: it ignores `GOLDIE_ADDR`, `GOLDIE_ADMIN_ADDR` and the
API token variables, so the same shell can run it again. The token is printed only in that line,
never logged. Ctrl-C stops it at once unless `GOLDIE_DRAIN_SECONDS` is set. Details are in the
[configuration reference](docs/reference/configuration.md#goldie-server--dev).

```sh
goldie server -dev examples/knowledge
# export GOLDIE_ADDR=http://127.0.0.1:41234 GOLDIE_TOKEN=<64 hex characters>
```

Paste the printed line into another shell, then use `goldie search` and `goldie lookup` there.

`goldie validate [dir]` runs the same content load without starting a server, for a pipeline: it
exits 0 and prints nothing when the content loads, and exits 1 with one `goldie: content does not
load: <reason>` line on stderr when it does not. It needs no token or address. See
[Checking content in CI](docs/content-ci.md).

## MCP over stdio

`goldie mcp --transport stdio` is an MCP server on stdin and stdout, with the tools
`search_knowledge`, `get_article`, `list_projects` and `current_context`. `search_knowledge` and
`get_article` take an optional `project` (`host/org/repo`, as `list_projects` names it; case is
ignored) that puts that Project's articles first; a value that isn't a Project id, or has any
non-ASCII character, is refused before any request. For a Project that isn't registered, the answer
is global and carries the server's hint. At startup, and only then, it finds the default Project
of the directory it runs in the way the CLI does: `GOLDIE_PROJECT`, then `.goldie/config.yaml`,
then the git remotes `upstream` and `origin`. A call that leaves `project` out, or empty, answers for
that default; an explicit `project` beats it, and one made only of white space is refused.
`current_context` reports the default and where it came from, in the words of `goldie status`, or
that there is none (outside a repository, for example, where the answers are global), naming a
remote whose URL isn't `host/org/repo` by name only. A broken or unreadable `.goldie/config.yaml`
doesn't stop the session: `current_context` reports the problem as `status` does, calls that rely on
the default fail with it before any request, and calls with an explicit `project` still answer. Fix
the file and restart the MCP server to pick it up. An agent's MCP client starts it as a subprocess. It talks to a
running server with the same client settings as the CLI: `--addr` (or `GOLDIE_ADDR`) and `--token`
(or `GOLDIE_TOKEN`, or `GOLDIE_TOKEN_FILE`), a flag overriding its variable, or over local content
with `--dir` (or `GOLDIE_DIR`, see [CLI](#cli)). Prefer the variables to
`--token`: a `--token` value shows in the process list (`ps`) and is saved in the MCP client's
config, while the variables can name a file. A missing or invalid setting stops it at startup with a
message on stderr that names the setting. Like the CLI, it follows no redirects and reads at most
16 MiB of an answer; a tool call that meets either says so, without the address. Stdout carries only
MCP messages; errors go to stderr. It exits when the client closes stdin. Only the `.goldie/` local
Scope (#269, #272) is still pending in local mode.

```json
{"mcpServers": {"goldie": {"command": "goldie", "args": ["mcp", "--transport", "stdio"],
  "env": {"GOLDIE_ADDR": "http://localhost:8080", "GOLDIE_TOKEN_FILE": "/run/secrets/goldie-token"}}}}
```

## MCP over HTTP

`goldie server` also serves the same MCP server on its public listener at `/mcp`, over streamable
HTTP, for agents that connect remotely. Every request needs an API token (`GOLDIE_API_TOKEN` or one
of `GOLDIE_API_TOKENS`) as `Authorization: Bearer <token>`; without it the answer is the 401
envelope. The tools call the API inside the process with the caller's token, so the API checks that
token and no other. The endpoint is stateless and answers with JSON: there are no sessions, no
standing event stream and no tool-list change notifications (the tool set is fixed), and each
request must finish within 10 seconds, so connected agents never hold up shutdown. The server never
detects a Project from its own working directory or environment: over HTTP there is no default
Project, `current_context` says so, and an agent passes `project` to scope an answer.

The server advertises its capabilities explicitly: tools, and not the SDK's default `logging`
capability, which nothing in goldie uses, so there are no MCP log notifications. If an API handler
fails unexpectedly behind a tool call, the tool answers with the 500 envelope and the server logs one
line with the API request's method and path, never the failure's details.

```json
{"mcpServers": {"goldie": {"type": "http", "url": "http://localhost:8080/mcp",
  "headers": {"Authorization": "Bearer ${GOLDIE_TOKEN}"}}}}
```

## Origins & credits

Goldie is based on Jessica Andersson's talk at Cloud Native Denmark 2026 and on the
[Solisol/goldenpath-demo](https://github.com/Solisol/goldenpath-demo) repository. Thank you, Jessica.
See [NOTICE](NOTICE).

## License

Apache-2.0. See [LICENSE](LICENSE).
