<!-- Generated from the settings registry in internal/platform/settings.go by `./run regen`. Do not edit. -->

# Goldie configuration reference

Configuration is environment only (PC-04). Every variable in the table below also accepts
a `NAME_FILE` form that reads the value from a file; setting both is an error (PC-05).

| Variable | Default | Meaning |
|---|---|---|
| `GOLDIE_ADDR` | `:8080` | Public (app) listener. |
| `GOLDIE_ADMIN_ADDR` | `:8081` | Admin listener: /livez, /readyz, /version. Must differ from GOLDIE_ADDR. |
| `GOLDIE_API_TOKEN` | (none) | Secret: set it with GOLDIE_API_TOKEN_FILE. goldie server needs this or GOLDIE_API_TOKENS, not both; both set stops the server and goldie health. The shorthand for one operator token, named api-token: the bearer token an /api/ or /mcp request may send. Printable ASCII without spaces, at least 32 bytes (generate one with openssl rand -hex 32); a trailing newline in the file is ignored and not counted. |
| `GOLDIE_API_TOKENS` | (none) | Secret: set it with GOLDIE_API_TOKENS_FILE. goldie server needs this or GOLDIE_API_TOKEN, not both; both set stops the server and goldie health. The named API tokens, one per line: name:role:sha256:&lt;hex&gt;, the recommended form, so the server never holds the token itself, or name:role:token. The name (1 to 64 letters, digits, '.', '_' or '-') identifies the client in activity and telemetry; each name and token is used once. The role is reader (search, look up, list, fetch bundles) or operator (also trigger syncs and read activity). &lt;hex&gt; is the SHA-256 of the token in hex (printf %s "$TOKEN" \| sha256sum); the token behind a hash must be as strong as openssl rand -hex 32 makes it. A plaintext token is printable ASCII without spaces, at least 32 bytes, and must not start with sha256:, which marks a hash. Blank lines and lines starting with # are ignored. GOLDIE_API_TOKENS_FILE keeps every line and drops only the trailing newline. A bad line stops the server, naming the line number, never the token. |
| `GOLDIE_CONTENT_DIR` | (none) | Required by goldie server. Directory of the dir Source; articles are read from global/articles under it at startup, and invalid content stops the server. |
| `GOLDIE_DRAIN_SECONDS` | `25` | Time in seconds between failing readiness on SIGTERM and shutting the listeners down. 0 to 3600. |
| `GOLDIE_LOG_LEVEL` | `info` | Log level: debug, info, warn or error. |
| `GOLDIE_MIN_CLIENT_VERSION` | (none) | The oldest goldie client the server serves, as a semantic version such as 1.4.0 (MAJOR.MINOR.PATCH, optionally -&lt;prerelease&gt;, without a leading v). The goldie CLI sends X-Goldie-Client: goldie/&lt;version&gt; on every request; on every /api/ path and on /mcp, a client whose version is older (semver precedence, so a prerelease is older than its release) is answered 426 in the error envelope, code upgrade_required, with a hint naming this minimum. Requests without the header, or whose header is not goldie/&lt;semver&gt; (curl, other tools, a dev build), are let through: the header is a courtesy, not a credential. Requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. Unset, there is no gate. A value that is not a semantic version stops the server and goldie health. |
| `GOLDIE_RATE_LIMIT` | `20/s,burst=40` | The request limit per token on every /api/ path and on /mcp: &lt;n&gt;/s or &lt;n&gt;/m, n requests per second or per minute for each token name, optionally followed by ,burst=&lt;b&gt;, the most requests a token may send at once (b defaults to n). n and b are positive integers, and the rate is at most 1e9 per second (60000000000/m). The burst has no upper bound, so a very large burst effectively disables the limit. The limit is held in memory per process, so each replica and each restart starts with full buckets. A request over the limit is answered 429 in the error envelope, with a Retry-After header giving the whole seconds to wait. Requests without a valid token are rejected with 401 before the limit and do not count. An MCP tool call counts twice: once for the /mcp request and once for the API request it makes. Every client using the GOLDIE_API_TOKEN shorthand shares one bucket, under the token name api-token. A malformed value stops the server and goldie health. |
| `GOLDIE_SWAGGER_UI_ENABLED` | `true` | Serve Swagger UI at /swagger-ui on the public listener, from assets embedded in the binary. true or false; false answers 404. |
| `GOLDIE_TELEMETRY_CLIENT_INFO` | `false` | true or false. true adds the caller's IP address (client.address, from the connection, never from X-Forwarded-For, which any caller can forge) and user agent (user_agent.original) to server spans. Off, spans carry no client address, port or user agent. Takes effect only when trace export is on. |

## Connection timeouts

These are fixed, not settings. The public listener closes a connection that takes more than
10 seconds to send request headers, more than 30 seconds to send the whole request, or that
sits idle for 60 seconds between requests. It sets no server-wide write timeout, so long
responses keep working; `/mcp` bounds its own requests. The admin listener bounds the whole
request and response at 10 seconds. Shutdown does not wait on these timeouts: it follows
`GOLDIE_DRAIN_SECONDS`.

## goldie server -dev

`goldie server -dev [dir]` serves `dir`, else `GOLDIE_CONTENT_DIR`, else the working directory.
Both listeners bind to 127.0.0.1 on ports the system picks, and the API takes one random
token (32 bytes from the system's secure random source, in hex), made for this run. It prints
`export GOLDIE_ADDR=http://127.0.0.1:<port> GOLDIE_TOKEN=<token>` once, to stdout, and the
token appears nowhere else: not in a log line, the startup report or telemetry. It ignores
`GOLDIE_ADDR`, `GOLDIE_ADMIN_ADDR`, `GOLDIE_API_TOKEN` and `GOLDIE_API_TOKENS` and their
_FILE forms, so a shell that ran the export line can run it again, and logs one line saying so
when any is set. `GOLDIE_DRAIN_SECONDS` defaults to 0 here; every other setting applies as for
`goldie server`.

## OpenTelemetry

Traces of the public listener and logs are exported over OTLP (http/protobuf) when an
endpoint is set. These variables keep their standard names and are read as defined by
OpenTelemetry, with the validation noted. Admin-port requests are not traced. Request
headers and query strings are not recorded. goldie reads every variable listed here through
its own lookup and passes it to the SDK explicitly. Other OTEL_* variables, such as
OTEL_BSP_*, OTEL_BLRP_*, timeouts, TLS and compression, keep the OpenTelemetry SDK's own
handling: goldie neither validates nor documents them, but a value the SDK rejects is never
printed; goldie logs one fixed warning instead. Only the OTLP headers variables
accept a _FILE form; for any other OTEL_* name it is an error.

| Variable | Default | Meaning |
|---|---|---|
| `OTEL_SDK_DISABLED` | (none) | true turns telemetry off even when an endpoint is set. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (none) | OTLP collector base URL (http or https, without surrounding space); signals go to /v1/traces, /v1/logs and /v1/metrics below it. Setting it, or a signal endpoint, turns on trace, log and metrics export. With an exporter selected and no endpoint, signals go to https://localhost:4318. The scheme decides between plain HTTP and TLS, so OTEL_EXPORTER_OTLP_INSECURE and its signal forms have no effect. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | (none) | Collector URL for traces only, used as-is; wins over the base URL. |
| `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` | (none) | Collector URL for logs only, used as-is; wins over the base URL. |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | (none) | Collector URL for metrics only, used as-is; wins over the base URL. |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | (none) | Only http/protobuf is supported, and it is the default. Also _TRACES_PROTOCOL, _LOGS_PROTOCOL and _METRICS_PROTOCOL. |
| `OTEL_EXPORTER_OTLP_HEADERS` | (none) | Secret: set it with OTEL_EXPORTER_OTLP_HEADERS_FILE. Collector request headers as key=value pairs, for example an API token. |
| `OTEL_EXPORTER_OTLP_TRACES_HEADERS` | (none) | Secret: set it with OTEL_EXPORTER_OTLP_TRACES_HEADERS_FILE. Collector request headers for traces only, as key=value pairs; wins over OTEL_EXPORTER_OTLP_HEADERS. |
| `OTEL_EXPORTER_OTLP_LOGS_HEADERS` | (none) | Secret: set it with OTEL_EXPORTER_OTLP_LOGS_HEADERS_FILE. Collector request headers for logs only, as key=value pairs; wins over OTEL_EXPORTER_OTLP_HEADERS. |
| `OTEL_EXPORTER_OTLP_METRICS_HEADERS` | (none) | Secret: set it with OTEL_EXPORTER_OTLP_METRICS_HEADERS_FILE. Collector request headers for metrics only, as key=value pairs; wins over OTEL_EXPORTER_OTLP_HEADERS. |
| `OTEL_TRACES_EXPORTER` | (none) | otlp or none. none turns off trace export. |
| `OTEL_LOGS_EXPORTER` | (none) | otlp or none. none turns off log export. |
| `OTEL_METRICS_EXPORTER` | (none) | otlp or none. none turns off metrics export. |
| `OTEL_METRIC_EXPORT_INTERVAL` | `60000` | Milliseconds between metric exports, a positive integer; anything else fails startup. |
| `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE` | `cumulative` | cumulative, delta or lowmemory (delta for counters and histograms, cumulative for up-down counters). |
| `OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT` | `1024` | Maximum length of a span attribute value; longer values are truncated. Server spans copy request data such as the user agent and path, so a higher value lets clients grow the spans held in memory. Falls back to OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT. A value that is not a non-negative integer fails startup. |
| `OTEL_TRACES_SAMPLER` | `parentbased_always_on` | Trace sampler: always_on, always_off, traceidratio or their parentbased_ forms. |
| `OTEL_TRACES_SAMPLER_ARG` | `1` | Sampling ratio from 0 to 1, for the traceidratio samplers. |
| `OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT` | `128` | Maximum attributes per span (falls back to OTEL_ATTRIBUTE_COUNT_LIMIT). OTEL_SPAN_EVENT_COUNT_LIMIT, OTEL_SPAN_LINK_COUNT_LIMIT, OTEL_EVENT_ATTRIBUTE_COUNT_LIMIT and OTEL_LINK_ATTRIBUTE_COUNT_LIMIT cap events, links and their attributes, 128 each. A value that is not a non-negative integer fails startup. |
| `OTEL_SERVICE_NAME` | `goldie` | Overrides the service.name resource attribute. |
| `OTEL_RESOURCE_ATTRIBUTES` | (none) | Extra resource attributes as key=value pairs with percent-encoded values; a malformed value fails startup. goldie always sets service.version and goldie.mode (all), which this cannot override. |

## Client commands

`goldie search`, `lookup`, `status` and `version` read these variables, and so does
`goldie mcp --transport stdio` (GOLDIE_ADDR, GOLDIE_TOKEN and GOLDIE_DIR).
Only GOLDIE_TOKEN has a _FILE form (GOLDIE_TOKEN_FILE); the client reads no other _FILE variable.

| Variable | Default | Meaning |
|---|---|---|
| `GOLDIE_ADDR` | (none) | Server URL, http:// or https://, that the client commands and goldie mcp --transport stdio talk to; --addr overrides it. The client does not read GOLDIE_ADDR_FILE. version skips a value that is not a URL, such as the server's listen address. |
| `GOLDIE_TOKEN` | (none) | Secret: set it with GOLDIE_TOKEN_FILE. The bearer token that search, lookup, status and goldie mcp --transport stdio send; --token overrides it. status works without it. |
| `GOLDIE_DIR` | (none) | A directory of content (global/articles under it). search, lookup, status and goldie mcp --transport stdio start an embedded server over it on loopback, with its own random token, and stop it when they end; --dir overrides it. Setting it together with --addr, or with GOLDIE_ADDR when --dir is not given, is an error; an explicit --dir wins over GOLDIE_ADDR in the environment, and GOLDIE_TOKEN is ignored. Content that does not load stops the command. version ignores it. |
| `GOLDIE_PROJECT` | (none) | The Project as host/org/repo, or a git remote URL (userinfo is dropped). Overrides .goldie/config.yaml and detection from the upstream or origin git remote; --project overrides it. search and lookup send it as project=; status shows it. |
