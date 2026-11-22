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
`/openapi.json`. The source is `internal/contract/openapi.yaml`; `./run lint-spec` lints it.

## CLI

`goldie search <words>`, `goldie lookup <id>`, `goldie status` and `goldie version` talk to a
running server through the generated client. Each takes `--addr` (or `GOLDIE_ADDR`), `--token` (or
`GOLDIE_TOKEN`, or `GOLDIE_TOKEN_FILE` to read it from a file) and `--json`. `status` reports
whether the server answers and accepts the token (`--json` adds `tokenGiven`) and which Project the
CLI works for. The Project is `--project`, else `GOLDIE_PROJECT`, else `project:` in
`.goldie/config.yaml`, else the `upstream` (or `origin`) git remote, normalised to `host/org/repo`.
The remote is read from `.git/config`; git need not be installed, and credentials in a remote URL are
never printed. The sync state arrives with the sync work (#6).

```sh
export GOLDIE_ADDR=http://localhost:8080 GOLDIE_TOKEN_FILE=/run/secrets/goldie-token
goldie search kubernetes
goldie lookup platform/deploy
```

## Origins & credits

Goldie is based on Jessica Andersson's talk at Cloud Native Denmark 2026 and on the
[Solisol/goldenpath-demo](https://github.com/Solisol/goldenpath-demo) repository. Thank you, Jessica.
See [NOTICE](NOTICE).

## License

Apache-2.0. See [LICENSE](LICENSE).
