# 30. The server is configured by flags, the environment or a config file

Status: accepted

## Context

PC-04 made the server environment-only. A multi-Source setup has to fit a whole JSON document into
one variable (`GOLDIE_SOURCES`), and an operator who runs the binary by hand cannot keep the settings
in a file or pass them as flags. Whatever is added must keep every existing check, the `_FILE` form
of a secret and the redacted startup report in one place, and must not put a secret where the process
list or a shared file shows it.

## Decision

`goldie server` takes each setting from, in this order of precedence, a flag, an environment
variable, a YAML config file, and the default (`--config <file>` or `GOLDIE_CONFIG`).

- **One validator.** The layers answer the `Lookup` that `platform.LoadConfig` already reads, so
  validation, `_FILE` handling and redaction stay in `LoadConfig`. A setting, with its `_FILE` form,
  comes from the one highest layer that sets it, so a flag is never joined by an environment `_FILE`
  into a conflict.
- **Names.** A file key is the variable without `GOLDIE_`, in lower case (`poll_interval`); a flag
  swaps its underscores for hyphens (`--poll-interval`). `sources` is a YAML list of the entries of
  the `GOLDIE_SOURCES` document, handed to the same parser as JSON. Every flag takes text, so a bad
  value reads the same from any layer.
- **Secrets by file only.** No secret is a flag and none is inline in the file; each has a `*_file`
  key and a `--*-file` flag. A secret given the other way stops the server, naming the flag or key.
- **Unknown keys fail.** A misspelt key would otherwise be ignored and the default would apply.
- **Errors and the report name the source.** A bad value stops the server naming the setting and
  `flag`, `environment` or `file`, never the value. The startup report keeps its flat keys and adds
  a `source` group with the layer of each setting.
- **Library.** The file layer uses `github.com/spf13/viper`, for parsing only: `readFile` hands it the
  YAML and reads back `AllSettings`. The issue named viper bound to the cobra and pflag the CLI uses;
  that pattern was not followed, because viper's binding and `AutomaticEnv` read the process
  environment and flags directly, which would bypass the `Lookup` that `--dev` masks and tests replace
  and would split validation from `LoadConfig`. So there is no pflag binding and no `AutomaticEnv`.
  The swap to `go.yaml.in/yaml/v3`, already a dependency, stays open behind `readFile`.

## Consequences

- The precedence and the file format are hard to reverse: operators will keep files, so a key name
  or a layer cannot change without a breaking release.
- A flag that holds a value shows in the process list. That is why a secret has none, and why a git
  URL with https userinfo or a password is refused as a flag or in the file (`--git-url`, `--sources`,
  `git_url`, `sources[].url`); the environment keeps its existing rule.
- viper adds 8 indirect modules. It lower-cases keys and treats a dot as nesting, so `Poll_Interval` is
  read as `poll_interval` and a dotted key is a nested one, which the unknown-key check then refuses.
- `goldie health` reads the environment only. A probe of a server whose admin address is set in the
  file needs `GOLDIE_ADMIN_ADDR` too.
- Client commands keep `.goldie/config.yaml` and their variables.
