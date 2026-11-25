# goldie

Goldie puts your team's golden path in front of developers and AI agents at the moment they decide:
which database to use, how to deploy, which library is approved.

It is for platform teams, who write the golden path as Markdown in git, and for the developers and
agents who receive it, in the browser, in a terminal or inside their coding agent.

The problem: the golden path is written down, but nobody finds it when it matters. Agents and people
pick a technology from habit or from training data, and the approved answer sits unread in a wiki.
Goldie answers the question when it is asked, and tells an agent where to look before it guesses.

## How it works

You keep knowledge in a **Source**, a git repository (or a directory) of Markdown files. Each file is
an **Item**: an article, a skill, an agent, a loop or a decision. An Item applies in a **Scope**:
`global` for everyone, or one **Project** (a repository, named `host/org/repo`) for the teams that own
it. Goldie answers for the Project you work in, with its own Items first.

A goldie server reads the Sources and serves them three ways:

- **UI:** search and read in the browser.
- **CLI:** `goldie search`, `goldie lookup` and more, in a terminal or a script.
- **MCP:** the same answers inside Claude Code and other agents.

Instruction blocks add a short, managed section to a repository's `CLAUDE.md` or `AGENTS.md` that
tells its agents to ask goldie first. The vocabulary is in [CONTEXT.md](CONTEXT.md).

## Try it in 5 minutes

You need Go and, for the web UI, Node. Goldie ships with example content, so no Source is needed to run it; step 2 clones the demo content from GitHub and needs network access (`goldie server --dev` alone works offline).

**1. Build it.** From a clone of this repository:

```sh
./run ui && go build ./cmd/goldie && export PATH="$PWD:$PATH"
```

This leaves `goldie` in the clone and on your PATH for this terminal only. Every new terminal needs
`export PATH=/path/to/clone:$PATH` (the clone's absolute path). `go install
github.com/thschue/goldie/cmd/goldie@latest` puts `goldie` on your PATH for good, but without the web
UI: `/` then shows a "goldie (UI not built)" placeholder. The CLI and MCP work either way.

**2. Start a server** from an empty directory. This serves the demo content of this repository from
git, at the release tag of your `goldie` (`main` for a build from a clone, which has no release), and
says which repository and path it serves. The first clone takes a second or two, and a search gets a
503 until it is ready. It runs in a subshell, so you stay in the clone:

```sh
mkdir -p /tmp/goldie-try && (cd /tmp/goldie-try && goldie server --dev --git)
```

To serve your own repository instead, set `GOLDIE_GIT_URL` (and `GOLDIE_GIT_REF` and
`GOLDIE_GIT_PATH` if you need them) before the same command:

```sh
GOLDIE_GIT_URL=https://github.com/your-org/your-content.git goldie server --dev --git
```

Offline, or to see the built-in examples, run `goldie server --dev` from an empty directory instead.

The server keeps running in this terminal, and Ctrl-C stops it. Open a second terminal for the steps
below, in the clone, with `goldie` on its PATH as in step 1.

It prints an `export GOLDIE_ADDR=… GOLDIE_TOKEN=…` line with a token made for this run. To serve a
directory of content instead, stop the first server with Ctrl-C and run this from the clone. In
the clone, `examples/knowledge` holds goldie's own agents and loops:

```sh
goldie server --dev examples/knowledge
# export GOLDIE_ADDR=http://127.0.0.1:41234 GOLDIE_TOKEN=<64 hex characters>
```

Paste the printed `export GOLDIE_ADDR=…` line into another shell. It is also what the MCP config in
step 5 reads. A plain
`goldie server` with nothing set suggests this command: `To try goldie on this machine: goldie server --dev`.
More in the [CLI reference](docs/reference/cli.md#run-a-server-over-your-own-content).

**3. Open the UI.** Go to the address in that line and sign in with the token. A binary built without
`./run ui` shows only the "goldie (UI not built)" placeholder; the CLI and MCP still work.

The server publishes its OpenAPI 3.1 contract at `/openapi.yaml` and `/openapi.json`, behind the same
Bearer token as the API (at least the `reader` Role). The source is `internal/contract/openapi.yaml`;
`./run lint-spec` lints it. Swagger UI at `/swagger-ui` (`GOLDIE_SWAGGER_UI_ENABLED=false` turns it
off) takes the same token. A signed-in browser session opens Swagger UI and `/openapi.json` without
further setup, and the web UI links to it. A client without a session sends
`Authorization: Bearer <token>`. "Try it out" for writes needs the Authorize button, because a
session cookie alone cannot write to the API.

**4. Search** from the shell where you pasted the line:

```sh
goldie search database
```

`goldie lookup <id>` reads an article, and `goldie skills list` lists the skills.

**5. Connect an agent.** In a git repository you work in, with the line from step 2 pasted, run:

```sh
goldie init
```

On a terminal it guides you: it finds the Project from the repository's git remote, asks you to
confirm it, writes `.goldie/config.yaml`, checks the server and your token, offers to add goldie's
block to `CLAUDE.md`, and prints the commands to run next (without `goldie instructions apply` when
you said yes, and with `CLAUDE.md` added to the `git add`):

```sh
export GOLDIE_ADDR=http://127.0.0.1:8080
claude mcp add --transport http goldie http://127.0.0.1:8080/mcp --header "Authorization: Bearer $GOLDIE_TOKEN"
goldie instructions apply
goldie status
git add .goldie/config.yaml && git commit -m "Add goldie config"
```

The address in your output is your server's. `claude mcp add` connects Claude Code to the server,
`goldie instructions apply` adds goldie's block to `CLAUDE.md`, and `goldie status` checks that it
all answers. Ask the agent about databases and it calls goldie. The Project says `Registered: no`
for a repository the examples don't cover; answers are then global, which is fine for a first look.
Details: [`goldie init`](docs/reference/cli.md), and for other agents or MCP over stdio, the
[MCP reference](docs/reference/mcp.md).

## Run it in a container

With Docker Compose, from a clone of this repository, `docker compose up` builds the image (UI included)
and serves the example content, with no setup. The app is at http://127.0.0.1:8080, reachable from your
machine only. An init step makes a random token and session key in a volume. Read the token, and sign in
with it:

```sh
docker compose up --build -d
docker compose run --rm --no-deps --entrypoint cat init /secrets/api-token
```

`docker compose down -v` stops it and deletes the token. The ports, the secrets and serving your own
content are in the [configuration reference](docs/reference/configuration.md#run-in-a-container).

## Use it on your own content

To serve your own repository, follow [Serve your Source from git](docs/git-source.md). A Source holds
`global/articles/`, `global/skills/`, `global/agents/`, `global/loops/` and
`global/decisions/`, and `projects/<host>/<org>/<repo>/` for one Project's own. Start with
[Writing content](docs/content.md): the smallest article that loads. Then
[add a skill](docs/skills.md), and check a Source in CI with `goldie validate`
([Checking content in CI](docs/content-ci.md)).

## What's in the MVP

- Search and read articles: [CLI](docs/reference/cli.md), [MCP](docs/reference/mcp.md), the UI.
- Skills, agents and loops, pulled into a repository: [adding a skill](docs/skills.md).
- Decisions: `goldie decisions list` ([CLI](docs/reference/cli.md)).
- Instruction blocks: `goldie instructions apply` ([CLI](docs/reference/cli.md)).
- Activity: who asked what, live in the UI ([API](docs/reference/api.md)).
- MCP over stdio and HTTP: [MCP reference](docs/reference/mcp.md).

What comes next is in the [milestones](https://github.com/thschue/goldie/milestones).

## Documentation

Write the golden path:

- [Writing content](docs/content.md): articles, Scopes and Projects.
- [Skills](docs/skills.md): skills, agents and loops.
- [Checking content in CI](docs/content-ci.md): validate a Source in a pipeline.

Run goldie:

- [Configuration](docs/reference/configuration.md): every setting.
- [Operating goldie](docs/platform-contract.md): health, readiness and telemetry, for operators.

Use it:

- [CLI reference](docs/reference/cli.md) and [MCP reference](docs/reference/mcp.md).
- [API reference](docs/reference/api.md): every endpoint.

Contribute:

- [CONTRIBUTING.md](CONTRIBUTING.md): the workflow, `./run check` and the agent loops.
- [CONTEXT.md](CONTEXT.md): the vocabulary.
- [docs/adr/](docs/adr/): decisions that are hard to reverse.

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md). `./run check` runs format, build, lint and tests, and must
pass before a pull request leaves draft. The agent loops are described there too.

## Origins & credits

Goldie is based on Jessica Andersson's talk at Cloud Native Denmark 2026 and on the
[Solisol/goldenpath-demo](https://github.com/Solisol/goldenpath-demo) repository. Thank you, Jessica.
See [NOTICE](NOTICE).

## License

Apache-2.0. See [LICENSE](LICENSE).
