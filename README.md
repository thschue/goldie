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
is `internal/contract/openapi.yaml`; `./run lint-spec` lints it.

## CLI

`goldie search <words>`, `goldie lookup <id>`, `goldie status` and `goldie version` talk to a
running server through the generated client. Each takes `--addr` (or `GOLDIE_ADDR`), `--token` (or
`GOLDIE_TOKEN`, or `GOLDIE_TOKEN_FILE` to read it from a file) and `--json`. `status` reports
whether the server answers and accepts the token (`--json` adds `tokenGiven`) and which Project the
CLI works for; without an accepted token it cannot read the API document, so it reports the API as
unknown, and `version` leaves out the server's API version. The Project is `--project` (only `status` takes it for now), else `GOLDIE_PROJECT`,
else `project:` in the repository's own `.goldie/config.yaml`, else the `upstream` (or `origin`) git
remote, normalised to `host/org/repo`. The remote is read from `.git/config`; git need not be
installed, and credentials in a remote URL are never printed. The sync state arrives with the sync
work (#6).

```sh
export GOLDIE_ADDR=http://localhost:8080 GOLDIE_TOKEN_FILE=/run/secrets/goldie-token
goldie search kubernetes
goldie lookup platform/deploy
```

## MCP over stdio

`goldie mcp --transport stdio` is an MCP server on stdin and stdout, with the tools
`search_knowledge` and `get_article`. An agent's MCP client starts it as a subprocess. It talks to a
running server with the same client settings as the CLI: `--addr` (or `GOLDIE_ADDR`) and `--token`
(or `GOLDIE_TOKEN`, or `GOLDIE_TOKEN_FILE`), a flag overriding its variable. A missing or invalid
setting stops it at startup with a message on stderr that names the setting. Stdout carries only MCP
messages; errors go to stderr. It exits when the client closes stdin. There is no local mode yet
(#10): it needs a server address.

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
request must finish within 10 seconds, so connected agents never hold up shutdown.

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
