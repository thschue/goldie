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

`goldie`, `goldie help` and `goldie --help` list the commands in two groups, Run a server (`server`,
`validate`, `health`) and Ask a server (`search`, `lookup`, `skills`, `agents`, `status`, `init`,
`mcp`, `version`).
`goldie <command> --help` (or `goldie help <command>`) shows a command's flags and examples. Flags
are GNU style (`--dev`, `--addr`); a single-dash long flag such as `-dev` is an error. An unknown
command or flag prints one error line and a `Run 'goldie <command> --help'.` line to stderr and exits 2;
help you ask for goes to stdout and exits 0.
`goldie --version` prints the same as `goldie version`; on a subcommand `--version` stays an unknown flag.
`goldie health` with no admin port answering exits 1 and says to check that the server runs and
`GOLDIE_ADMIN_ADDR` matches its admin address.

`goldie skills list|show <name>` and `goldie agents list|show <name>` read the skill bundles (name
and file list) and agent definitions (name and description) that answer for the Project. They take
`--project`, `--json` and the same address and token settings, and print the server's hint for an
unregistered Project. A name must be one path segment, and an empty or dot-only one is refused
before any request. Skill and agent names match case-insensitively, and the answer shows the
name as written. Two skills, or two agents, in one Scope whose names differ only in case fail the
load; the error names both. File paths inside a skill stay exact. `goldie skills show <name>
--file <path>` writes one file of the skill, as listed by `show <name>`, to stdout as its raw
bytes and nothing else. Redirect it to a file for anything binary. An unknown path exits 1 and names
`goldie skills show <name>`. `--file` belongs to `skills show` only. An empty or dot-only path is
refused before any request.

`goldie search <words>`, `goldie lookup <id>`, `goldie status` and `goldie version` talk to a
running server through the generated client, or an embedded one over local content (below). Each
takes `--addr` (or `GOLDIE_ADDR`), `--token` (or `GOLDIE_TOKEN`, or `GOLDIE_TOKEN_FILE` to read it
from a file) and `--json`. Prefer `GOLDIE_TOKEN` or `GOLDIE_TOKEN_FILE` to `--token`: a `--token`
value shows in the process list (`ps`) of the machine. The CLI follows no redirects, so a server
cannot send the token elsewhere, and every request carries `X-Goldie-Client: goldie/<version>`
(`goldie mcp` adds `X-Goldie-Via: mcp`).
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
need not be installed, and credentials in a remote URL are never printed. When the server holds the
Project and suppresses global articles for it, `status` lists them on a `Suppressions:` line after
the `Project:` line (`--json` adds a `suppressions` array); a lookup of one fails with a hint naming
the Suppression and the `project.yaml`. The sync state arrives
with the sync work (#6). `goldie lookup` of a deprecated article prints
`Warning: this article is deprecated. Use <replacement> instead.` on stderr, naming the replacement
when there is one; stdout, `--json` included, carries only the article.

`goldie search`, `lookup`, `skills`, `agents`, `status` and `mcp` also work without a server:
`--dir PATH` (or `GOLDIE_DIR`) starts an embedded server over a directory of content
(`global/articles`, `global/skills` and `global/agents` under it) on loopback, with its own random
token, and stops it when the command ends. Output is the same as against a remote server over the
same content. `--dir` overrides `GOLDIE_DIR`. It also overrides a `GOLDIE_ADDR` from the
environment. A repository with `.goldie/articles/` starts the same embedded server with its local
Scope added, over `--dir`, `GOLDIE_DIR`, `dir:` in `.goldie/config.yaml` or empty content (see
[the local Scope](docs/content-ci.md#the-local-scope-goldie)), and then `--addr` or `GOLDIE_ADDR` is
an error; `GOLDIE_TOKEN` is ignored. `--dir` or `GOLDIE_DIR` together with `--addr`, or `GOLDIE_DIR`
and `GOLDIE_ADDR` both set in the environment, is an error naming both. Content that does not load
stops the command with `content does not load: <reason>`. When `search`, `lookup`, `status` or an
`mcp` tool cannot reach the embedded server, the error says so and does not mention `--addr` or
`GOLDIE_ADDR`. `version` and `init` ignore `--dir`: `init` checks registration only against a
running server.

A `200 OK` that is not a goldie API answer, such as a proxy's login page, is reported as "the
server's answer isn't a goldie API answer; check the address", by the CLI and by `mcp` alike, and
nothing of the page is shown.

`goldie init [--project ID]` sets a repository up: it finds the Project as `status` does and writes
`project: <id>` to `.goldie/config.yaml` at the repository root (the nearest directory with a
`.git`), then prints the Project and how it was found, the file it wrote, and whether the server
has the Project registered (`yes`, `no` with a hint on how to register it, or `not checked` with the
reason: no address, no token, a rejected token, or a server that is unreachable, redirects, answers
with an error status, or whose answer couldn't be read). The check is one `GET /api/v1/projects` and
ignores case; without a token it sends no request. `init` takes no `--json`. It never overwrites an
existing `.goldie/config.yaml`, never follows a symlinked `.goldie` or `config.yaml`, and refuses to
run outside a git repository or in a Source repository (`global/articles` or `projects/` at its
root); a refusal writes nothing.

`goldie instructions apply [--target claude|agents] [--force]` writes goldie's instruction block
between `<!-- goldie:begin digest=sha256:… -->` and `<!-- goldie:end -->` in `CLAUDE.md` (the default)
or `AGENTS.md` at the repository root, creating the file if needed. The block comes from
`GET /api/v1/instructions` for the detected Project (`--project` overrides it). Text outside the
markers is never touched, and a block that is already current is not rewritten. `apply` refuses a
block edited by hand unless you pass `--force`, which discards those edits, and refuses a damaged file (a
begin marker without an end, or two blocks) even with `--force`. It also refuses a block whose text could
change how the rest of your file renders (an unclosed code fence, raw HTML, a link reference definition),
a `CLAUDE.md` or `AGENTS.md` that is a symlink, and a run outside a git repository; a refusal writes
nothing. `goldie instructions check` writes nothing and exits non-zero when the block is missing, outdated
or edited. The file is replaced by a rename of a temporary file in the same directory. Lost update:
between `apply` reading the file and renaming over it, an edit by another process is lost; there is no
lock, and the window is one local write.

If your `CLAUDE.md` imports `AGENTS.md` (an `@AGENTS.md` line), apply only the agents target:
`goldie instructions apply --target agents`. Applying both would show the block twice.

Check in CI that the committed block is current (set `GOLDIE_ADDR` and `GOLDIE_TOKEN` as secrets):

```sh
goldie instructions check --target agents   # exits non-zero if missing, outdated or edited
```

```sh
export GOLDIE_ADDR=http://localhost:8080 GOLDIE_TOKEN_FILE=/run/secrets/goldie-token
goldie search kubernetes
goldie lookup platform/deploy
```

## Try it on your own content

`goldie server --dev [dir]` runs the server on 127.0.0.1 over `dir` (else `GOLDIE_CONTENT_DIR`, else
the working directory) with a random token made for this run, and prints the line that points the
client commands at it. It needs no other setup: it ignores `GOLDIE_ADDR`, `GOLDIE_ADMIN_ADDR` and the
API token variables, so the same shell can run it again. The token is printed only in that line,
never logged. Ctrl-C stops it at once unless `GOLDIE_DRAIN_SECONDS` is set. Details are in the
[configuration reference](docs/reference/configuration.md#goldie-server---dev).

```sh
goldie server --dev examples/knowledge
# export GOLDIE_ADDR=http://127.0.0.1:41234 GOLDIE_TOKEN=<64 hex characters>
```

Paste the printed line into another shell, then use `goldie search` and `goldie lookup` there. That
`export GOLDIE_ADDR=…` line is also what the MCP config in the next section reads.

A directory with no content serves the example content built into the binary, and says so on
stderr; a directory with content is served as it is. The examples hold one article, `agent-loops`.
Try `goldie search loop`.

A plain `goldie server` with no Source and no API token set stops with two configuration errors and
ends with `To try goldie on this machine: goldie server --dev`, the command above with the working
directory as `dir`. With only one of the two missing, or a value that is invalid, it shows the
errors alone.

`goldie validate [dir]` runs the same content load without starting a server, for a pipeline: it
exits 0 and prints nothing when the content loads, and exits 1 with one `goldie: content does not
load: <reason>` line on stderr when it does not. It needs no token or address. See
[Checking content in CI](docs/content-ci.md).

## MCP over stdio

`goldie mcp --transport stdio` is an MCP server on stdin and stdout, with the tools
`search_knowledge`, `get_article`, `list_projects`, `current_context`, `find_skills`, `get_skill`,
`list_skills` and `list_agents`. `search_knowledge` and `get_article` take an optional `project`
(`host/org/repo`, as `list_projects` names it; case is ignored) that puts that Project's articles
first; a value that isn't a Project id, or has any non-ASCII character, is refused before any
request. For a Project that isn't registered, the answer is global and carries the server's hint. At
startup, and only then, it finds the default Project of the directory it runs in the way the CLI
does: `GOLDIE_PROJECT`, then `.goldie/config.yaml`, then the git remotes `upstream` and `origin`. A
call that leaves `project` out, or empty, answers for that default; an explicit `project` beats it,
and one made only of white space is refused. `current_context` reports the default and where it came
from, in the words of `goldie status`, or that there is none (outside a repository, for example,
where the answers are global), naming a remote whose URL isn't `host/org/repo` by name only. A
broken or unreadable `.goldie/config.yaml` doesn't stop the session: `current_context` reports the
problem as `status` does, calls that rely on the default fail with it before any request, and calls
with an explicit `project` still answer. Fix the file and restart the MCP server to pick it up.

The skill and agent tools take the same optional `project`. `find_skills` searches skills only, by
keyword or name. `get_skill` answers a skill's file paths and the text of its `SKILL.md`, each line
quoted with `| `; its optional `file` (a path from that list, such as `scripts/run.sh`) reads that
file of the bundle instead, quoted the same way. A name that is empty, dots only or holds a
separator, and a `file` that leaves the bundle, is refused before any request. A skill without a
`SKILL.md` is reported as that, not as unknown. `get_article` quotes the article text with `| `
too; the server's own hint stays a plain line after it. Every tool parameter's description carries
an example value. `list_skills` and `list_agents` list every name for the Project, sorted, an agent
with its description.

The server has a resource, `goldie://instructions` (`resources/list`, `resources/read`): the
instruction block the API renders for the default Project, or the global block without one, with
`{{mcp_name}}` set from `GOLDIE_MCP_NAME`. It renders for the target `claude`, the default of
`goldie instructions`; the target changes only the file the block is written into. The text is cleaned
of hidden characters like any Source content and a failed read is an
error without the address or token. `current_context` ends with the same clauses, or says they could
not be read.

The server also offers prompts and two more resources. Each skill is a prompt named `goldie-<skill name>`
(for the session's default Project), and using it inserts the cleaned text of its `SKILL.md` into
the conversation. Unlike `get_skill` and the resources, the prompt text is cleaned but not quoted
with `| `. Prompt text is untrusted: a Source wrote it, and the server inserts it as a user message,
cleaned but not quoted, so any instruction in it reaches the model. Choosing a prompt means you
vouch for that skill's Source. For the quoted form, use `get_skill` or the resources. The prompt
list is read once, the first time your client asks for it, and kept until the MCP server restarts.
A skill added later appears after you restart your agent or its MCP server; an unknown prompt is
answered with a message that points to `list_skills` or to that restart. The server sends no
`list_changed` notification. The two other resources are URI templates:
`goldie://skills/{name}/{+path}` reads one file of a skill's bundle (the path keeps its slashes,
as in `scripts/run.sh`), as text with each line quoted with `| `, and `goldie://agents/{name}`
reads an agent's name and description. The session's default Project picks the bundle, never the
URI. A skill, file or agent that doesn't exist answers as resource not found. So does a URI that
is malformed or leaves the bundle. The error names `list_skills` and `list_agents` as the next
step. A failed prompt or resource read says it was a prompt or resource, not a tool.
When the default Project has a problem, a prompt or resource error names the problem and tells the
agent to ask the user to fix it and restart the MCP server: those surfaces take no `project`
argument, so it does not point at `list_projects`.

An agent's MCP client starts it as a subprocess. It talks to a running server with the same client
settings as the CLI: `--addr` (or `GOLDIE_ADDR`) and `--token` (or `GOLDIE_TOKEN`, or
`GOLDIE_TOKEN_FILE`), a flag overriding its variable, or over local content with `--dir` (or
`GOLDIE_DIR`, see [CLI](#cli)). Prefer the variables to `--token`: a `--token` value shows in the
process list (`ps`) and is saved in the MCP client's config, while the variables can name a file. A
missing or invalid setting stops it at startup with a message on stderr that names the setting. Like
the CLI, it follows no redirects and reads at most 16 MiB of an answer; a tool call that meets
either says so, without the address. Stdout carries only MCP messages; errors go to stderr. It exits
when the client closes stdin. Only the `.goldie/` local Scope (#269, #272) is still pending in local
mode.

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
