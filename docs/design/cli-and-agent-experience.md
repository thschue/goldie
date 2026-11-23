# Goldie CLI and agent experience

| | |
|---|---|
| Status | **Proposed** 2026-11-23. The maintainer approves it by merging. |
| Applies to | everything a person meets in a terminal (`goldie` commands, their help, output and errors, the README's quickstart) and everything an agent meets (MCP tools and prompts, their results and errors, managed instruction blocks) |
| Checked by | the `ux-designer`, in refinement and in review, against this document |

The UI has [its handoff](ui-handoff.md). This is the same bar for the other two surfaces. Goldie
has three kinds of users: a person in the browser, a person in a terminal, and an agent. Each
deserves a first run that works and a next step that is obvious.

## 1. Principles

1. **No dead end.** Every error says what to do next, in a form someone can copy and run. An error
   that only says what is missing is half an error.
2. **The first run works.** A newcomer with the release binary, an empty directory and an empty
   environment can follow the README to a served answer without reading anything else.
3. **Written for the reader in front of it.** A person at a terminal gets prose and aligned text; a
   pipe, a log collector or an agent gets JSON. Never make a person read JSON to find the error.
4. **Short by default, complete on request.** Root help fits on one screen; detail lives in
   `<command> --help` and the reference docs.
5. **Agents are users too.** Tool descriptions say when to use the tool, not just what it does.
   Results are compact and carry ids the agent can pass to the next call.

## 2. The CLI

### 2.1 Help

- `goldie`, `goldie help` and `goldie --help` print a one-line description, then the commands in
  groups (**Run a server**, **Ask a server**), one line each, then `Run 'goldie <command> --help'
  for details.` The whole thing fits in 24 lines of 80 columns.
- `goldie <command> --help` and `goldie help <command>` show usage, a sentence on what the command
  is for, its flags with defaults, and at least one example that works as written.
- Notes that apply to one command or flag (the `--token` process-list warning, what `--dev`
  ignores) live in that command's help, never in the root help.
- Asked-for help goes to stdout with exit 0. Help after misuse goes to stderr with exit 2.

### 2.2 Errors

- One line saying what went wrong, in plain words, then one line saying what to do next. For a
  usage error, that line is `Run 'goldie <command> --help'.`; never the whole usage.
- When the likely reader is a newcomer (nothing configured at all), the next step is the one that
  gets them running (`goldie server --dev [dir]`), not the full list of operator settings.
- Errors never contain addresses, paths outside what the user typed, tokens or stack traces.

### 2.3 Output and exit codes

- Data on stdout, diagnostics and progress on stderr. `--json` changes stdout only.
- Exit 0 on success, 1 when the command failed, 2 on misuse (unknown command, flag or argument).
- Logs on a terminal are for a person: the server's startup line names the address, the content
  and how to stop it. Structured JSON logs are for everything that is not a terminal (a proposed
  decision, see §5).
- Flags are GNU style (`--flag`, `-f`), as cobra gives them (#437).

### 2.4 The first run

The journey the `ux-designer` walks on every audit and on any PR that changes it:

1. `goldie` and `goldie --help` on their own.
2. `goldie server` with an empty environment: it must point at `--dev`.
3. `goldie server --dev` in an empty directory, then over `examples/knowledge`.
4. The printed export line pasted into another shell, then `goldie search` and `goldie lookup`.
5. `goldie init` in a fresh git repository.
6. `goldie mcp --transport stdio` from an MCP client config copied from the README.

Each step either works or says exactly what to do next.

## 3. The agent experience

### 3.1 MCP tools and prompts

- Each tool's description starts with when an agent should call it, in one sentence, then what it
  returns. No marketing, no internal names the agent can't use.
- Parameters are named in the API's words (snake_case, ADR 0024) and each has a description and an
  example value.
- Results lead with the answer. Ids an agent may pass to another tool are given exactly as that tool
  takes them.
- A miss says it is a miss and what the agent can try next (a broader search, another project),
  never an empty result with no words.
- Errors follow §2.2: what went wrong and the next step, in a sentence an agent can act on.

### 3.2 Managed instruction blocks

- A block tells an agent when to ask goldie and how, in as few lines as do the job. Every line
  costs context in every session of every repository that carries it.
- The commands and tool names in a block work as written in the repository it lands in.

## 4. Review evidence

A PR that changes a surface in this document shows the result, not just the diff: the PR body (or
the `ux` CI artifact) holds a transcript of the changed help, errors or first-run steps, run on the
built binary. For MCP changes, the tool list and one call's result as an MCP client sees them.
Missing evidence for a changed surface is blocking, as screenshots are for the UI.

## 5. Open decisions

- Text logs when stderr is a terminal, JSON otherwise (touches the platform contract's logging
  clause).
- Built-in example content for `goldie server --dev` in an empty directory: decided and built
  (#451); the binary embeds `examples/knowledge` and serves it only when the directory has no content.
