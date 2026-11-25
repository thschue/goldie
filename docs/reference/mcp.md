# Goldie MCP reference

`goldie mcp` and the `/mcp` endpoint give an agent the same answers as the CLI and the UI. This page
covers MCP over stdio and over HTTP. For a first run, start with the [README](../../README.md); the
CLI is in the [CLI reference](cli.md).

## MCP over stdio

`goldie mcp --transport stdio` is an MCP server on stdin and stdout, with the tools
`search_knowledge`, `get_article`, `list_projects`, `current_context`, `find_skills`, `get_skill`,
`list_skills`, `list_agents`, `list_loops`, `get_loop` and `find_decisions`. `search_knowledge` and `get_article` take an optional `project`
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

`list_loops` lists every loop of the Project's view, sorted, with its description, its interval (or
that it is dynamic) and its stop sentence; no loop is an answer, not an error. `get_loop` answers one
loop: its fields, the agents and skills it needs with the Scope that answered, the digest the API
serves (to compare with a pulled copy), and the iteration prompt quoted with `| `. A miss is a tool
error that points to `list_loops`.

`find_decisions` takes a query and the same optional `project` and answers the accepted decisions
that match, each with its name, title, Scope, keywords and what matched; no match is an answer, not an error. The resource
`goldie://decisions/{name}` reads one accepted decision: title, status, Scope, date and the body quoted
with `| `. A decision that is unknown, or not accepted, is resource-not-found naming `find_decisions`.

The server has a resource, `goldie://instructions` (`resources/list`, `resources/read`): the
instruction block the API renders for the default Project, or the global block without one, with
`{{mcp_name}}` set from `GOLDIE_MCP_NAME`. It renders for the target `claude`, the default of
`goldie instructions`; the target changes only the file the block is written into. The text is cleaned
of hidden characters like any Source content and a failed read is an
error without the address or token. `current_context` ends with the same clauses, or says they could
not be read.

The server also offers prompts and two more resources. Each loop is a prompt named
`goldie-loop-<loop name>`: it returns the `/loop <interval> <prompt>` command (`/loop <prompt>`
for a dynamic loop) and, when the loop needs agents or skills, a note to run `goldie pull` first. If a
skill's prompt has the same name, the skill keeps it. Each skill is a prompt named `goldie-<skill name>`
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
URI: resource URIs carry no Project, because a resource is addressed by its URI alone and the
session already has a default Project. To read another Project's skill or its files, call
`get_skill` with `project`. A skill, file or agent that doesn't exist answers as resource not
found. So does a URI that is malformed or leaves the bundle. The error names `list_skills` and
`list_agents` as the next step. A failed prompt or resource read says it was a prompt or resource, not a tool.
When the default Project has a problem, a prompt or resource error names the problem and tells the
agent to ask the user to fix it and restart the MCP server: those surfaces take no `project`
argument, so it does not point at `list_projects`.

An agent's MCP client starts it as a subprocess. It talks to a running server with the same client
settings as the CLI: `--addr` (or `GOLDIE_ADDR`) and `--token` (or `GOLDIE_TOKEN`, or
`GOLDIE_TOKEN_FILE`), a flag overriding its variable, or over local content with `--dir` (or
`GOLDIE_DIR`, see [CLI](cli.md#cli)). Prefer the variables to `--token`: a `--token` value shows in the
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

