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
sees its Project, then global; a global article sees global only. It fails the load when it does not
resolve, names the article itself, names a draft or is part of a cycle.

A reference is at most 256 bytes:

- The Kind is lower-case letters and must be one goldie knows; today that is `article`.
- The name is slash separated, with no empty, `.` or `..` segment, so it has no leading or trailing
  `/`.
- The name holds only printable characters: no Unicode space (the plain space included), no
  control, format or bidi character, no extra colon and no backslash.

An article whose path holds a character outside that set still loads, but it cannot be referenced.

Two more checks fail the load:

- A project article may not shadow a global Item that is `enforced: true` and published or
  deprecated (an enforced draft does not enforce). The error names both files.
- A lookup line must resolve in the article's own view, by the same rule as `replaced_by`, and a
  draft does not resolve. A lookup line is a fenced code block line or a one-line inline code span
  whose whole content is `goldie lookup <id>`. A line with extra words or `--project` is skipped.

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
articles, then published global ones.

These fail the load, and the reason names `project.yaml`:

- a reference to an `enforced: true` global article that is published or deprecated, or to an
  article of the Project itself (including one that shadows a global article);
- a malformed reference, or an entry that is not a string;
- a `suppress` that is not a list, more than 1000 entries, or a file larger than 1 MiB;
- an unknown key (such as `supress:`), a duplicate key, more than one YAML document, or invalid YAML;
- a `project.yaml` that is a symlink or a directory.

A reference that names nothing the Project can see (a deleted article, a draft, or another Project's
article) does not fail the load, and the article is suppressed once it exists and is published. The
engine records a warning in its sync status, one per reference and file, but no API, CLI command or
`goldie validate` shows it yet (#367).

Only the file directly in a Project directory counts. A `project.yaml` directly under `projects/`,
a host or an org directory is a layout error; one deeper inside a Project is ignored.

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
