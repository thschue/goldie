# goldie Platform Contract

The operational contract `goldie server` and `goldie mcp` obey, so operators can run them like any
other platform workload. Clauses are numbered `PC-xx`. A test that proves a clause is named after
it and runs against the real server over HTTP, never a mock.

Variables use the `GOLDIE_` prefix, with one exception: the standard `OTEL_*` OpenTelemetry variables
(PC-12, [ADR 0017](adr/0017-observability-through-opentelemetry.md)). Implementation lives in
`internal/platform`.

| Clause | Requirement |
|---|---|
| PC-01 | **Admin port.** Probes and version are served on a separate admin port (`GOLDIE_ADMIN_ADDR`), never on the public listener. On the public listener `/livez`, `/readyz` and `/version` (exact, case-sensitive) answer the 404 error envelope, so a monitor pointed at the wrong port never reads a UI page as healthy. Any other unknown path on the admin port answers 404 in the error envelope with a static `details.hint` that echoes nothing from the request; the public listener answers any unknown path outside `/api/`, `/mcp` (exact), the docs and `/swagger-ui` with the embedded UI (see below), and answers the same way when no API is wired. |
| PC-02 | **Liveness and readiness.** `/livez` answers 200 while the process runs. `/readyz` runs one check per Source (or one upstream check) and fails only while a Source has never loaded; a stale Source stays ready. Sources added at runtime never make a running server unready. |
| PC-03 | **Version.** `/version` returns the version, commit and build date as JSON. |
| PC-04 | **Environment-only config.** All server settings come from environment variables and are validated at startup. Invalid or missing values fail fast with an error naming the offending variable. (Declarative Sources and tokens held in Kubernetes Secrets are the one dynamic exception; settings stay env-only.) `goldie server -dev [dir]` takes the content directory as an argument; nothing else departs from environment-only configuration. |
| PC-05 | **Secrets by file.** Every secret variable has a `_FILE` form that reads the value from a file. Secrets never appear in plain env docs, JSON config, logs or error messages. |
| PC-06 | **Startup report.** The effective configuration is logged once at startup with secrets redacted. |
| PC-07 | **Structured logs.** Logs are JSON on stdout, and every line carries `component`. In `goldie server -dev` mode logs go to stderr, so stdout carries only the export line. |
| PC-08 | **Drain.** On SIGTERM the server stops accepting new connections, reports not ready, finishes in-flight requests within a bounded grace period, then exits 0. Rollouts drop no requests. |
| PC-09 | **Health subcommand.** `goldie health` probes the local admin port and exits non-zero when unhealthy, for images without a shell or `curl`. |
| PC-10 | **Generated component docs.** Configuration and endpoint reference docs are generated from code (`./run regen`) and CI fails on drift. |
| PC-11 | **Hardened image.** The image is distroless, non-root and runs with a read-only root filesystem. |
| PC-12 | **Telemetry.** The server's traces of the public listener, logs and metrics are exported over OTLP http/protobuf, configured by the standard `OTEL_*` variables, and are off unless an endpoint or exporter variable selects them (`OTEL_SDK_DISABLED=true` always turns them off). Goldie validates the variables that select and address the exporters, `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER{,_ARG}` and the span limits `OTEL_{SPAN_,}ATTRIBUTE_{VALUE_LENGTH,COUNT}_LIMIT`, `OTEL_SPAN_{EVENT,LINK}_COUNT_LIMIT` and `OTEL_{EVENT,LINK}_ATTRIBUTE_COUNT_LIMIT` (PC-04), and for metrics `OTEL_METRICS_EXPORTER`, `OTEL_METRIC_EXPORT_INTERVAL` and `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE` (cumulative, delta or lowmemory; default cumulative). These fail fast naming the variable, never its value. Metrics come from one provider held by the platform module (`Telemetry.Meter`), never a global one, with a cardinality limit of 2000 data points per instrument; overflow is aggregated, not dropped. Other `OTEL_*` variables (batch tuning, timeouts, TLS, compression) keep the SDK's own handling and are not validated, but what the SDK reports about them is never logged (one fixed warning is), because it can contain the value. The startup report (PC-06) lists the enabled signals, the protocol and each collector as scheme://host only. The admin port is never traced, and stdout logs keep PC-07. Server spans carry no client address, port or user agent unless `GOLDIE_TELEMETRY_CLIENT_INFO=true`, which adds the connection's IP as `client.address` and `user_agent.original`; `X-Forwarded-For` is never used for any span attribute. The client commands and `goldie mcp --transport stdio` export traces only (`goldie.role` `cli` or `mcp`, and no `goldie.mode`): one client span per API call, sent as a W3C `traceparent`; the server starts its own trace and links its span to it. Client spans carry no header, query string, userinfo or article id; the logs and metrics variables and `GOLDIE_TELEMETRY_CLIENT_INFO` do not apply to them; their SDK notices go to stderr, so stdout keeps the command output or MCP messages only; the flush on exit takes at most 2 seconds. Query text and article and Project ids are never log fields, metric labels or span attributes: a lookup's span carries the path `/api/v1/articles/{id}`, without the id. |

Changing a clause is a decision: update this file and, when it is hard to reverse, add an ADR.

## Settings and endpoints

The settings are documented in the generated [configuration reference](reference/configuration.md),
rendered from the registry in `internal/platform/settings.go`. Add a variable by adding a row there
and running `./run regen`.

- Any `GOLDIE_` variable accepts a `NAME_FILE` form that reads the value from a file; setting both is an
  error. Errors name the variable, never its value or the file path. Of the `OTEL_*` variables only the
  OTLP headers (`OTEL_EXPORTER_OTLP_HEADERS` and its `_TRACES_`/`_LOGS_`/`_METRICS_` forms) accept `_FILE`; for any
  other `OTEL_*` name the SDK would ignore it, so it is an error.
- Goldie reads each `OTEL_*` variable it validates through the injected lookup and passes the result to the
  exporters, sampler, span limits and resource explicitly, defaults included, so the SDK's own reading of
  the process environment for them is overridden. The SDK still reads the other `OTEL_*` variables itself. Goldie validates `OTEL_SDK_DISABLED`,
  `OTEL_EXPORTER_OTLP_{,TRACES_,LOGS_}{ENDPOINT,PROTOCOL,HEADERS}`, `OTEL_{TRACES,LOGS}_EXPORTER`,
  `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER{,_ARG}` and the span limits (a malformed value, such as a pair without `=` or
  a bad percent escape, fails startup). Every OTLP headers variable that is set is checked, and a header
  key must be an RFC 7230 token, because the SDK prints a rejected key or value to its own logger.
  `OTEL_RESOURCE_ATTRIBUTES` cannot override `service.version`,
  `goldie.role` or `goldie.mode`. The generated configuration reference lists them and names the ones left to the SDK.
  Only protocol `http/protobuf` is supported. With an exporter selected and no endpoint, goldie passes the
  SDK's default `https://localhost:4318`. The endpoint's scheme, the default's included, decides between
  plain HTTP and TLS, so `OTEL_EXPORTER_OTLP{,_TRACES,_LOGS}_INSECURE` has no effect; the CA and client
  certificate variables keep the SDK's handling. The one `OTEL_*` limit goldie sets itself is a 1024-byte
  default for `OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT`.
- `/readyz` returns `{"status":"ready|not_ready|draining","checks":[{"name","status"}]}` with 200 or
  503. Each check has its own timeout (2 s) and its error text is never echoed.
- `/version` returns `{"version","commit","date"}`, fixed at build time via `-ldflags -X` on
  `internal/platform.Version`, `.Commit` and `.BuildDate`; it carries no timestamp.
- `goldie health` probes `/livez` on the local admin port (`--ready` probes `/readyz`) and exits
  non-zero unless it answers 200. Use plain `health` for liveness probes: it stays green while draining.
- The first SIGTERM or SIGINT starts the drain; a second one exits immediately with a non-zero code.
  Unbounded streams (`GET /api/v1/activity`) end cleanly at the first stop signal, and new ones
  get 503 `shutting_down` during the drain, so no stream holds up the shutdown.
  After the drain, in-flight requests get up to 30 s to finish; past that the server exits non-zero.
  Set the pod's termination grace period above `GOLDIE_DRAIN_SECONDS` plus that 30 s.
- The public endpoints are documented in the generated [API reference](reference/api.md)
  (`./run regen`; `./run check` fails on drift), as is the configuration reference (PC-10). The image
  of PC-11 has its own issue.
- The public listener serves the embedded UI at `/` and for any other extensionless GET or HEAD path
  (not the PC-01 probe paths; the SPA's client-side routes), with `index.html` as `no-cache`, `/assets/*` as immutable, a fixed
  `Content-Type` per file, `nosniff` and a strict Content-Security-Policy on UI responses only. A
  missing file with an extension is a 404 envelope, never HTML; other methods get the 405 envelope.
  The UI is public: the API behind it still needs the token. Without `./run ui` the binary serves a
  placeholder page and logs one warning at startup ([ADR 0019](adr/0019-ui-build-output-not-committed.md)).
- Swagger UI is served at `/swagger-ui` on the public listener, from assets embedded in the binary
  (no CDN), and renders only the spec this server serves. `GOLDIE_SWAGGER_UI_ENABLED=false` turns it
  off (404). It is registered on the same mux as the API and takes the same Bearer token (see
  below). A browser does not add that header by itself, so opening the page needs a reverse proxy
  or a browser extension that adds `Authorization: Bearer <token>` to every request, the page's
  fetch of `/openapi.json` included. The page never embeds or stores the token. "Authorize" sets the
  token for "Try it out".
- `goldie server` needs `GOLDIE_CONTENT_DIR` (the `dir` Source, loaded at startup) and exactly one
  of `GOLDIE_API_TOKENS` and `GOLDIE_API_TOKEN`, best set through their `_FILE` forms.
  `GOLDIE_API_TOKENS` holds ADR 0008's named tokens with Roles, one per line: `name:role:token`, or
  the recommended `name:role:sha256:<hex>` with the token's hex SHA-256, so the server never holds
  the token. The Role is `reader` (search, look up, list, fetch bundles) or `operator` (also sync
  and activity). `GOLDIE_API_TOKEN` is the shorthand for one operator token named `api-token`.
  Tokens must be at least 32 bytes; `openssl rand -hex 32` makes one, and the token behind a hash
  must be as strong. A plaintext token must not start with `sha256:`, which marks a hash. A bad
  line, a reused name or token, or a short token makes the server exit non-zero naming the variable
  and the line number, never the token, its digest or its length.
  Every path under `/api/`, `/openapi.yaml`, `/openapi.json` and every `/swagger-ui` path require
  `Authorization: Bearer <token>` with at least the `reader` Role; no other header, query parameter
  or cookie is a credential. The server hashes the token and compares it with every configured
  digest in constant time, and answers 401 in the error envelope otherwise. `GET /api/v1/activity`,
  the live event stream of recent searches and lookups, needs the `operator` Role: other tokens get
  403 before the rate limit, so the refusal costs them nothing. The stream is a live view, not an
  audit log, and it ends at the first SIGTERM, before the drain (PC-08). `GET /api/v1/sync`, each
  Source's sync status and the content load's warnings, and `POST /api/v1/sync`, which starts a
  poll now (202, or 429 while one runs), need the `operator` Role the same way. A Source's
  `last_error` names a file relative to its root and never a host path, URL or credential. The OpenAPI documents
  and Swagger UI do not count against the rate limit. The handler sees the token's name, never its
  secret. The startup report shows both variables as `(redacted)`. Like every config variable,
  these may change in a `0.x` minor release (ADR 0011).
- Nested article ids are one `%2F`-encoded path segment (ADR 0016). A reverse proxy in front of the
  public listener must pass `%2F` through unchanged: Apache needs `AllowEncodedSlashes NoDecode`, and
  nginx must not carry a URI in `proxy_pass`.
