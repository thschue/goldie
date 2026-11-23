# Checking content in CI

`goldie validate [dir]` runs the load the server runs at startup, so content that passes here loads
on the server. It starts no server and needs no token or address; `GOLDIE_ADDR` and
`GOLDIE_ADMIN_ADDR` do not matter.

The directory is the argument, else `GOLDIE_CONTENT_DIR`, else the working directory (the same order
as `goldie server -dev`).

| Result | Exit | stdout | stderr |
| --- | --- | --- | --- |
| Content loads | 0 | empty | empty |
| Content does not load | 1 | empty | `goldie: content does not load: <reason>` |

The reason names a file relative to the directory, never an absolute path. It is the same line
`goldie server -dev` ends with for that directory. A second directory argument is a usage error
(exit 1, usage on stderr).

## Lifecycle frontmatter

Besides `name` and `keywords`, an article may set three lifecycle fields. Any other value fails the
load, and the reason names the file and the field.

| Field | Values | Default |
| --- | --- | --- |
| `status` | `draft`, `published` or `deprecated`, lower case | `published` |
| `enforced` | a YAML bool, `true` or `false` (`yes`, `on` and `"true"` are rejected) | `false` |
| `replaced_by` | a reference `<kind>:<name>`, such as `article:platform/deploy` | none |

A `replaced_by` reference (ADR 0021) resolves in the referring article's view: a project article
sees its Project, then global; a global article sees global only. A project copy, even a draft,
never stands in for an enforced global article, so a reference to an enforced article resolves to
the global one. It fails the load when it does not resolve, names the article itself, names a draft
or is part of a cycle.

A reference is at most 256 bytes:

- The Kind is lower-case letters and must be one goldie knows; today that is `article`.
- The name is slash separated, with no empty, `.` or `..` segment, so it has no leading or trailing
  `/`.
- The name holds only printable characters: no Unicode space (the plain space included), no
  control, format or bidi character, no private-use or unassigned code point, no extra colon and no
  backslash. The invisible characters outside those categories are refused too: the variation
  selectors, the combining grapheme joiner U+034F, the Hangul fillers, the Mongolian free variation
  selectors and the blank Braille pattern U+2800. It is the same set the clients strip from text
  they show.

An article whose path holds a character outside that set still loads, but it cannot be referenced.

Two more checks fail the load:

- A project article may not shadow a global Item that is `enforced: true` and published or
  deprecated (an enforced draft does not enforce). The error names both files.
- A lookup line must resolve in the article's own view, by the same rule as `replaced_by`, and a
  draft does not resolve. A lookup line is a fenced code block line or a one-line inline code span
  whose whole content is `goldie lookup <id>`. A line with extra words or `--project` is skipped.
  A placeholder id is skipped too: `goldie lookup <id>` or `goldie lookup …`, as in the README.
  An id with a hidden or control character, or one longer than a reference may be, is not a
  placeholder and fails the load. The scan follows CommonMark: a fence is indented at most 3
  spaces, a fence in a list item counts, and an indented code block or a `<pre>` HTML block is
  not scanned.

An article with `status: draft` is left out of search and lookup, and a draft project article does
not shadow the global article with the same id. An operator token can ask for drafts with
`drafts=true` on search and lookup; a reader token asking for them gets a 403, and MCP never asks.
Even then a draft project article never shadows an enforced global one.

Search and lookup answer `status` and `replaced_by` for a deprecated article (and `status: draft`
for a draft an operator asked for); a published article carries neither. `goldie lookup` of a
deprecated article prints `Warning: this article is deprecated. Use <replaced_by> instead.` on
stderr, and MCP's `get_article` starts with the same line.

## Suppression in project.yaml

A Project can hide global articles that do not apply to it (CONTEXT: Suppression) in
`projects/<host>/<org>/<repo>/project.yaml`:

```yaml
suppress:
  - article:platform/deploy
  - article:style
```

The suppressed articles are left out of that Project's search and lookup, drafts included. Other
Projects and the global view still see them. A reference is the same `<kind>:<name>` as
`replaced_by`, compared ignoring case, and it resolves in the Project's view: its own published
articles, then published global ones. A draft project copy neither shadows the global article nor
stops its suppression.

The Project's own articles see the same view: a lookup line or a `replaced_by` in one of them that
names a suppressed article fails the load, and the error names the article and `project.yaml`. A
global article, and another Project's, still resolve it.

These fail the load, and the reason names `project.yaml`:

- a reference to an `enforced: true` global article that is published or deprecated, or to an
  article of the Project itself (including one that shadows a global article);
- a malformed reference, or an entry that is not a string;
- a `suppress` that is not a list, more than 1000 entries, or a file larger than 1 MiB;
- an unknown key (such as `supress:`), a duplicate key, more than one YAML document, or invalid YAML;
- a `project.yaml` that is a symlink, a directory, or anything else but a regular file, such as a
  FIFO.

A reference that names nothing the Project can see (a deleted article, a draft, or another Project's
article) does not fail the load, and the article is suppressed once it exists and is published. The
engine records a warning in its sync status, one per reference and file, and `goldie validate` prints
each on stderr as `goldie: warning: <file>: suppress "<reference>" names no published Item this
Project can see`, with the exit code still 0. The reference is shown escaped to ASCII and cut. The
same warnings are on `GET /api/v1/sync` (`{"warnings": [{"file", "message"}]}`) for operator tokens only (ADR
0008): a reader token gets 403 and no token 401, because a warning shows what a Source wrote. The
Project detail does not carry them.

`GET /api/v1/projects/{id}` and `goldie status` list what a Project suppresses, as `article:<id>`.
A lookup of a suppressed article answers 404 with a hint that names the reference and the
`project.yaml` that sets it.

Only the file directly in a Project directory counts. A `project.yaml` directly under `projects/`,
a host or an org directory is a layout error; one deeper inside a Project is ignored.

## The local Scope: `.goldie/`

A repository's own `.goldie/` is the local Scope (CONTEXT: Scope), read by the engine's local
Source (`engine.NewLocalSource`). The server does not serve it yet; the embedded server of #390
will.

- `.goldie/articles/**/*.md` are local articles, written like global ones. Resolution is local,
  then project, then global, for `ArticleIn` and `SearchIn`. A local article answers in every
  view, whichever Project is asked for, because the engine is not told which Project the checkout
  is. On an equal search score, local hits sort before project hits, then global ones.
- `.goldie/config.yaml` takes `suppress` as `project.yaml` does, hiding global articles in every
  view. It may also hold `project`, `addr` and `dir`, which the client reads and the engine
  ignores. Any other key fails the load.
- A published local article that shadows an `enforced: true` article at any wider Scope, the
  global one or any Project's, fails the load. The error names both as `article:<name>`. A local
  draft shadows nothing, and a local suppression of an enforced article fails the load.
- References from local articles (`replaced_by`, lookup lines, `suppress`) resolve in the local
  view: local, then global. A dangling or suppressed one fails the load. Global and Project
  articles never see local ones.
- `.goldie`, `articles` and `config.yaml` are read through `os.Root`. A symlink, a FIFO or the
  wrong file type fails the load, with the same size caps and case-insensitive ids as elsewhere.
  Errors name `.goldie/...` paths only. A missing `.goldie` is an empty local Scope.

## Pipeline snippet

Put the `goldie` binary on `PATH` in your pipeline, then fail the job on a non-zero exit:

```yaml
# GitHub Actions
jobs:
  content:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      # install the goldie binary here
      - name: Validate content
        run: goldie validate knowledge
```

Any CI system works the same way: run `goldie validate <dir>` and treat exit 1 as a failed check.
