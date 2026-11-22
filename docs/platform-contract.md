# goldie Platform Contract

The operational contract `goldie server` and `goldie mcp` obey, so operators can run them like any
other platform workload. Clauses are numbered `PC-xx`. A test that proves a clause is named after
it and runs against the real server over HTTP, never a mock.

Variables use the `GOLDIE_` prefix, with one exception: the standard `OTEL_*` OpenTelemetry variables
(PC-12, [ADR 0017](adr/0017-observability-through-opentelemetry.md)). Implementation lives in
`internal/platform`.

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
| PC-12 | **Telemetry.** Traces of the public listener and logs are exported over OTLP http/protobuf, configured by the standard `OTEL_*` variables, and are off unless an endpoint or exporter variable selects them (`OTEL_SDK_DISABLED=true` always turns them off). Goldie validates the variables that select and address the exporters, `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` (PC-04); they fail fast naming the variable, never its value. Other `OTEL_*` variables (samplers, batch tuning, timeouts, TLS, compression) keep the SDK's own handling and are not validated. The admin port is never traced, and stdout logs keep PC-07. |

Changing a clause is a decision: update this file and, when it is hard to reverse, add an ADR.

## Settings and endpoints

The settings are documented in the generated [configuration reference](reference/configuration.md),
rendered from the registry in `internal/platform/settings.go`. Add a variable by adding a row there
and running `./run regen`.

- Any `GOLDIE_` variable accepts a `NAME_FILE` form that reads the value from a file; setting both is an
  error. Errors name the variable, never its value or the file path. Of the `OTEL_*` variables only the
  OTLP headers (`OTEL_EXPORTER_OTLP_HEADERS` and its `_TRACES_`/`_LOGS_` forms) accept `_FILE`; for any
  other `OTEL_*` name the SDK would ignore it, so it is an error.
- `OTEL_*` is read by the OpenTelemetry SDK as well as by goldie. Goldie validates `OTEL_SDK_DISABLED`,
  `OTEL_EXPORTER_OTLP_{,TRACES_,LOGS_}{ENDPOINT,PROTOCOL,HEADERS}`, `OTEL_{TRACES,LOGS}_EXPORTER`,
  `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` (a malformed value, such as a pair without `=` or
  a bad percent escape, fails startup). `OTEL_RESOURCE_ATTRIBUTES` cannot override `service.version`
  or `goldie.mode`. The generated configuration reference lists them and names the ones left to the SDK.
  Only protocol `http/protobuf` is supported. The one `OTEL_*` limit goldie sets itself is a 1024-byte
  default for `OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT`.
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
- `goldie server` needs `GOLDIE_CONTENT_DIR` (the `dir` Source, loaded at startup) and
  `GOLDIE_API_TOKEN`, best set as `GOLDIE_API_TOKEN_FILE`; without either it exits non-zero naming the
  variable. Every path under `/api/` requires `Authorization: Bearer <token>`, compared in constant
  time, and answers 401 in the error envelope otherwise. The startup report shows the token as
  `(redacted)`. Until #11, the token does not cover `/openapi.*` or `/swagger-ui`. This single token
  stands in for ADR 0008's named tokens with Roles, which #11 brings; like every config variable,
  `GOLDIE_API_TOKEN` may change in a `0.x` minor release (ADR 0011).
- Nested article ids are one `%2F`-encoded path segment (ADR 0016). A reverse proxy in front of the
  public listener must pass `%2F` through unchanged: Apache needs `AllowEncodedSlashes NoDecode`, and
  nginx must not carry a URI in `proxy_pass`.
