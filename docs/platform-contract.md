# goldie Platform Contract

The operational contract `goldie server` and `goldie mcp` obey, so operators can run them like any
other platform workload. Clauses are numbered `PC-xx`. A test that proves a clause is named after
it and runs against the real server over HTTP, never a mock.

Variables use the `GOLDIE_` prefix. Implementation lives in `internal/platform`.

| Clause | Requirement |
|---|---|
| PC-01 | **Admin port.** Probes and version are served on a separate admin port (`GOLDIE_ADMIN_ADDR`), never on the public listener. |
| PC-02 | **Liveness and readiness.** `/livez` answers 200 while the process runs. `/readyz` runs one check per Source (or one upstream check) and fails only while a Source has never loaded; a stale Source stays ready. Sources added at runtime never make a running server unready. |
| PC-03 | **Version.** `/version` returns the version, commit and build date as JSON. |
| PC-04 | **Environment-only config.** All server settings come from environment variables and are validated at startup. Invalid or missing values fail fast with an error naming the offending variable. (Declarative Sources and tokens held in Kubernetes Secrets are the one dynamic exception; settings stay env-only.) |
| PC-05 | **Secrets by file.** Every secret variable has a `_FILE` form that reads the value from a file. Secrets never appear in plain env docs, JSON config, logs or error messages. |
| PC-06 | **Startup report.** The effective configuration is logged once at startup with secrets redacted. |
| PC-07 | **Structured logs.** Logs are JSON on stdout, and every line carries `component`. |
| PC-08 | **Drain.** On SIGTERM the server stops accepting new connections, reports not ready, finishes in-flight requests within a bounded grace period, then exits 0. Rollouts drop no requests. |
| PC-09 | **Health subcommand.** `goldie health` probes the local admin port and exits non-zero when unhealthy, for images without a shell or `curl`. |
| PC-10 | **Generated component docs.** Configuration and endpoint reference docs are generated from code (`./run regen`) and CI fails on drift. |
| PC-11 | **Hardened image.** The image is distroless, non-root and runs with a read-only root filesystem. |

Changing a clause is a decision: update this file and, when it is hard to reverse, add an ADR.

## Settings and endpoints (skeleton)

| Variable | Default | Meaning |
|---|---|---|
| `GOLDIE_ADDR` | `:8080` | Public (app) listener. |
| `GOLDIE_ADMIN_ADDR` | `:8081` | Admin listener: `/livez`, `/readyz`, `/version`. Must differ from `GOLDIE_ADDR`. |
| `GOLDIE_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `GOLDIE_DRAIN_SECONDS` | `25` | Time between failing readiness on SIGTERM and shutting the listeners down. `0` to `3600`. |

- Any variable accepts a `NAME_FILE` form that reads the value from a file; setting both is an error.
  Errors name the variable, never its value or the file path.
- `/readyz` returns `{"status":"ready|not_ready|draining","checks":[{"name","status"}]}` with 200 or
  503. Each check has its own timeout (2 s) and its error text is never echoed.
- `/version` returns `{"version","commit","date"}`, fixed at build time via `-ldflags -X` on
  `internal/platform.Version`, `.Commit` and `.BuildDate`; it carries no timestamp.
- `goldie health` probes `/livez` on the local admin port (`--ready` probes `/readyz`) and exits
  non-zero unless it answers 200. Use plain `health` for liveness probes: it stays green while draining.
- The first SIGTERM or SIGINT starts the drain; a second one exits immediately with a non-zero code.
- PC-10 (generated docs) and PC-11 (image) have their own issues; this table is hand-written until
  `./run regen` generates it.
