# Goldie CLI reference

The rules of every `goldie` command: flags, environment variables, the Project, local mode and the
files the commands write. For a first run, start with the [README](../../README.md). Settings of the
server are in the [configuration reference](configuration.md); the MCP server is in the
[MCP reference](mcp.md).

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
`goldie health --config <file>` (or `GOLDIE_CONFIG`) probes the `admin_addr` of that YAML config file when
`GOLDIE_ADMIN_ADDR` is unset; the environment wins over the file. `goldie server --dev` reads no config
file and stops with an error if `--config` or `GOLDIE_CONFIG` is given.

`goldie skills list|show <name>`, `goldie agents list|show <name>` and `goldie loops list|show <name>`
read the skill bundles (name and file list), agent definitions (name and description) and loops
(`show` prints the interval or dynamic pacing, the stop condition, the prompt and the agents and
skills it names, with the Scope that answered for each) that answer for the Project. They take
`--project`, `--json` and the same address and token settings, and print the server's hint for an
unregistered Project. A name must be one path segment, and an empty or dot-only one is refused
before any request. Skill, agent and loop names match case-insensitively, and the answer shows the
name as written. Two skills, two agents or two loops in one Scope whose names differ only in case fail the
load; the error names both. File paths inside a skill stay exact, and two files of one skill
whose paths differ only in case fail the load the same way. `goldie skills show <name>
--file <path>` writes one file of the skill, as listed by `show <name>`, to stdout as its raw
bytes and nothing else. Redirect it to a file for anything binary. An unknown path exits 1 and names
`goldie skills show <name>`. `--file` belongs to `skills show` only. An empty or dot-only path is
refused before any request.

`goldie decisions list|show <name>` reads the decisions that answer for the Project. `list` prints
the accepted ones, one per line, with name, title and Scope; `show` prints the name, title, Scope,
status, date and the body, and finds a decision of any status. It takes `--project`, `--json` and the
same address and token settings, and prints the server's hint for an unregistered Project. A miss
exits 1 and names `goldie decisions list`; wrong use exits 2 before any request, as for `goldie loops`.

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
environment. A repository with `.goldie/articles/`, or with `decisions: {path: docs/adr}` in
`.goldie/config.yaml`, starts the same embedded server with its local
Scope added, over `--dir`, `GOLDIE_DIR`, `dir:` in `.goldie/config.yaml` or empty content (see
[the local Scope](../content-ci.md#the-local-scope-goldie)), and then `--addr` or `GOLDIE_ADDR` is
an error; `GOLDIE_TOKEN` is ignored. `decisions.path` is a folder of the repository, relative and
inside it, whose Nygard, MADR or frontmatter ADRs are served as local decisions without moving them;
`README.md` and templates are skipped, a malformed ADR is a warning on stderr that names its file,
and a path that is absolute, uses `..`, leaves the repository by a symlink or holds control
characters is an error naming `.goldie/config.yaml`. `--dir` or `GOLDIE_DIR` together with `--addr`,
or `GOLDIE_DIR`
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

On a terminal, `goldie init` guides you. It prints the Project and asks `Use this Project? [Y/n]`
(`n` stops and names `--project`), writes the config, asks for the server address (the default is
`--addr` or `GOLDIE_ADDR`), and reports `Server:` (reachable or not, probed once, no redirect
followed) and `Token:` (accepted, rejected or not set, from `--token`, `GOLDIE_TOKEN` or
`GOLDIE_TOKEN_FILE`, never from a prompt; the token is never printed). The questions and those two
lines go to stderr; the Project, the file, `Registered:` and a `Next` block go to stdout.
The `Next` block holds the commands to run, one per line:

```sh
export GOLDIE_ADDR=http://127.0.0.1:8080
export GOLDIE_TOKEN=<your token>            # only when the token is not set or was rejected
claude mcp add --transport http goldie http://127.0.0.1:8080/mcp --header "Authorization: Bearer $GOLDIE_TOKEN"
goldie instructions apply
goldie status
git add .goldie/config.yaml && git commit -m "Add goldie config"
```

When the server answered and accepted the token, `init` also asks `Add goldie's instruction block to
CLAUDE.md? [Y/n]` (stderr). Yes (or Enter) runs `goldie instructions apply --target claude` and prints
its message on stdout (`Wrote the goldie block to CLAUDE.md (was missing).`, or `CLAUDE.md is already up to
date.`), `Next` drops the `goldie instructions apply` line, and its commit command becomes
`git add .goldie/config.yaml CLAUDE.md && git commit -m "Add goldie config"`. No, or input that
ends at the question, changes nothing and `Next` keeps the line. A block edited by hand is never
forced: the command's message goes to stderr, `init` still exits 0 and `Next` keeps the line. Any
other failure (the server cannot render
the block, the file is a symlink) is reported, `.goldie/config.yaml` stays, `Next` is still printed and
`init` exits 1.

With no server address, `Next` says to start one (`goldie server --dev`) and run `goldie init` again,
and holds only the commit command. Nothing but `.goldie/config.yaml` is saved. Without a terminal (a
pipe, `</dev/null`, CI), or when the input is already at its end, `init` asks nothing and prints only
what is described above. Ctrl-D at a question that was shown stops with `init stopped; nothing more
was changed`, exit 1; at the Project question nothing is written, at a later one the config already
written stays.

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

`goldie pull [--target claude] [--dry-run | --check] [--quiet] [--if-pulled]` writes the skills and
agents that apply to the detected Project (`--project` overrides it) into the repository root of the
working directory, from any subdirectory: each skill to `.claude/skills/goldie-<name>/` with its
files, each agent to `.claude/agents/goldie-<name>.md`, and each loop's entry skill to
`.claude/skills/goldie-loop-<name>/SKILL.md` (its description, iteration prompt and stop condition; the
loop's agents and skills come with the rest). A pull then prints `Start a loop in Claude Code:` and
`/loop <interval> /goldie-loop-<name>` for each loop,
sorted by name, and `/loop /goldie-loop-<name>` for a dynamic one. A pull that finds the namespace up to
date says so and prints the same lines for the loops already in it. `--check` and `--dry-run` print
none. Those `goldie-` names are goldie's namespace. A pull replaces
the namespace as a whole, so a skill or agent removed from the platform disappears, and touches nothing
else: your own skills and agents stay. The bundle comes from `GET /api/v1/bundle`, a tar.gz whose ETag is
the digest of the set; `pull` keeps that digest in `.claude/goldie-pull.digest` and
sends it back, with a hash of the namespace as it wrote it, so an unchanged set costs no download and
no write. If a pulled file was edited or deleted since, `pull` asks without the digest and restores
the namespace. A skill file keeps its executable bit. The CLI checks
the whole archive before it writes: it refuses an absolute path, `..`, a symlink, hardlink, device or
fifo, and any entry outside the namespace, and it never writes through a symlinked `.claude`,
`.claude/skills` or `.claude/agents`. The new files are staged and moved in, and a failure puts the old
ones back, so a failed pull leaves the project as it was. `--dry-run` lists what would be written or
removed and writes nothing. `--check` writes nothing and exits non-zero, telling you to run
`goldie pull`, when the namespace was never pulled, the set changed or a pulled file was edited or
deleted. `--dry-run` and `--check` cannot be
combined. `--quiet` prints nothing on stdout when the pull succeeds (errors still go to stderr and
exit non-zero) and cannot be combined with `--dry-run` (exit 2). `--if-pulled` does nothing and
succeeds, sending no request, when the repository has no usable stamp or the directory is not in a
repository at all, so a session hook can run `goldie pull --quiet --if-pulled` and touch only
repositories that took part. With a stamp it pulls as usual. `pull` takes no `--json`.

```sh
goldie pull --check   # in CI or a hook: exits non-zero if never pulled or stale
```

```sh
export GOLDIE_ADDR=http://localhost:8080 GOLDIE_TOKEN_FILE=/run/secrets/goldie-token
goldie search kubernetes
goldie lookup platform/deploy
```


## Run a server over your own content

First get the binary. `go install github.com/thschue/goldie/cmd/goldie@latest` puts `goldie` on your
PATH. A binary from `go install`, or from a build without `./run ui`, serves a "goldie (UI not
built)" placeholder at `/`; the CLI and MCP work either way ([ADR
0019](../adr/0019-ui-build-output-not-committed.md)). To get the web UI, clone the repository,
run `./run ui` (needs Node), then `go build ./cmd/goldie`, which leaves `./goldie` in the clone
(run it as `./goldie`, or move it onto your PATH).

Then run `goldie server --dev` (`./goldie server --dev` for the clone build), open the address it
prints (the `GOLDIE_ADDR` in the export line), and sign in by pasting the token from that same
line (`GOLDIE_TOKEN=…`).

`goldie server --dev [dir]` runs the server on 127.0.0.1 over `dir` (else `GOLDIE_CONTENT_DIR`, else
the working directory) with a random token made for this run, and prints the line that points the
client commands at it. It needs no other setup: it ignores `GOLDIE_ADDR`, `GOLDIE_ADMIN_ADDR` and the
API token variables, so the same shell can run it again. The token is printed only in that line,
never logged. Ctrl-C stops it at once unless `GOLDIE_DRAIN_SECONDS` is set. Details are in the
[configuration reference](configuration.md#goldie-server---dev).

New to writing content? [Writing content](../content.md) shows the smallest article that loads.

```sh
goldie server --dev examples/knowledge
# export GOLDIE_ADDR=http://127.0.0.1:41234 GOLDIE_TOKEN=<64 hex characters>
```

Paste the printed line into another shell, then use `goldie search` and `goldie lookup` there. That
`export GOLDIE_ADDR=…` line is also what the MCP config in the next section reads.

When stdin and stderr are both terminals, the working directory is in a repository that is not a
Source repository, and `.goldie/config.yaml` does not exist, the server then asks on stderr `Set up
this repository for this dev server now? [Y/n]`. Yes runs the guided `goldie init` against this dev
server (its address and token, which are never printed or put in the environment), with all of its
output on stderr: stdout stays the export line alone, so `eval "$(goldie server --dev)"` is safe. No or
Ctrl-D writes nothing, and the server keeps serving. Ctrl-C at the question stops the server as usual.
Elsewhere (a pipe, CI, no repository) nothing is asked.

`goldie server --dev --git` serves a git Source instead of a directory, on the same loopback address with
the same random token. With `GOLDIE_GIT_URL` set (and `GOLDIE_GIT_REF` and `GOLDIE_GIT_PATH`, both
optional) it serves that repository. Without it, it serves `examples/demo` of
`https://github.com/thschue/goldie.git` at the tag `v<version>` of the binary, so the content matches
it; a build without a release version uses `main`. It says once on stderr which repository and path it
serves. Like `goldie server`, a failed first clone keeps the server running and not ready, and one
stderr line gives the reason, without credentials, and the hint to run `goldie server --dev` without
`--git` to try the built-in examples offline. The built-in repository takes no credentials: with
`GOLDIE_GIT_URL` unset, a set `GOLDIE_GIT_TOKEN` (or `_FILE`), `GOLDIE_GIT_SSH_KEY`,
`GOLDIE_GIT_SSH_KNOWN_HOSTS` or `GOLDIE_GIT_CA_BUNDLE` is refused with a usage error naming the
variable. `--git` takes no directory and needs `--dev`. Without `--git`, `--dev` ignores
`GOLDIE_GIT_URL`.

A directory with no content serves the example content built into the binary, and says so on
stderr; a directory with content is served as it is. The examples are `examples/demo`, a small demo
platform: four articles, a skill, an agent and a loop. Try `goldie search database`, then
`goldie skills list`, `goldie agents list` and `goldie loops list`. goldie's own agents and loops
are `examples/knowledge`, which is not built in.

A plain `goldie server` with no Source and no API token set stops with two configuration errors and
ends with `To try goldie on this machine: goldie server --dev`, the command above with the working
directory as `dir`. With only one of the two missing, or a value that is invalid, it shows the
errors alone.

`goldie validate [dir]` runs the same content load without starting a server, for a pipeline: it
exits 0 and prints nothing when the content loads, and exits 1 with one `goldie: content does not
load: <reason>` line on stderr when it does not. It needs no token or address. See
[Checking content in CI](../content-ci.md).

