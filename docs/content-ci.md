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

`status: draft` does not hide an article yet: it is served like any other. Do not rely on it to keep
content private until the enforcement slice lands.

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
