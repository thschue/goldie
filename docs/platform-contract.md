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

## Settings and endpoints

The settings are documented in the generated [configuration reference](reference/configuration.md),
rendered from the registry in `internal/platform/settings.go`. Add a variable by adding a row there
and running `./run regen`.

- Any variable accepts a `NAME_FILE` form that reads the value from a file; setting both is an error.
  Errors name the variable, never its value or the file path.
- `/readyz` returns `{"status":"ready|not_ready|draining","checks":[{"name","status"}]}` with 200 or
  503. Each check has its own timeout (2 s) and its error text is never echoed.
- `/version` returns `{"version","commit","date"}`, fixed at build time via `-ldflags -X` on
  `internal/platform.Version`, `.Commit` and `.BuildDate`; it carries no timestamp.
- `goldie health` probes `/livez` on the local admin port (`--ready` probes `/readyz`) and exits
  non-zero unless it answers 200. Use plain `health` for liveness probes: it stays green while draining.
- The first SIGTERM or SIGINT starts the drain; a second one exits immediately with a non-zero code.
  After the drain, in-flight requests get up to 30 s to finish; past that the server exits non-zero.
  Set the pod's termination grace period above `GOLDIE_DRAIN_SECONDS` plus that 30 s.
- The public endpoints are documented in the generated [API reference](reference/api.md)
  (`./run regen`; `./run check` fails on drift), as is the configuration reference (PC-10). The image
  of PC-11 has its own issue.
- Swagger UI is served at `/swagger-ui` on the public listener, from assets embedded in the binary
  (no CDN), and renders only the spec this server serves. `GOLDIE_SWAGGER_UI_ENABLED=false` turns it
  off (404). It is registered on the same mux as the API, so the auth of #11 covers it.
