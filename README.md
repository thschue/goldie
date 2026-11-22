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

## Origins & credits

Goldie is based on Jessica Andersson's talk at Cloud Native Denmark 2026 and on the
[Solisol/goldenpath-demo](https://github.com/Solisol/goldenpath-demo) repository. Thank you, Jessica.
See [NOTICE](NOTICE).

## License

Apache-2.0. See [LICENSE](LICENSE).
