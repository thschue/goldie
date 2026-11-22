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
