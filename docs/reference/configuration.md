<!-- Generated from the settings registry in internal/platform/settings.go by `./run regen`. Do not edit. -->

# Goldie configuration reference

Configuration is environment only (PC-04). Every variable also accepts a `NAME_FILE` form
that reads the value from a file; setting both is an error (PC-05).

| Variable | Default | Meaning |
|---|---|---|
| `GOLDIE_ADDR` | `:8080` | Public (app) listener. |
| `GOLDIE_ADMIN_ADDR` | `:8081` | Admin listener: /livez, /readyz, /version. Must differ from GOLDIE_ADDR. |
| `GOLDIE_API_TOKEN` | (none) | Required by goldie server. Secret: set it with GOLDIE_API_TOKEN_FILE. The static bearer token that every /api/ request must send. Printable ASCII without spaces; a trailing newline in the file is ignored. |
| `GOLDIE_CONTENT_DIR` | (none) | Required by goldie server. Directory of the dir Source; articles are read from global/articles under it at startup, and invalid content stops the server. |
| `GOLDIE_DRAIN_SECONDS` | `25` | Time in seconds between failing readiness on SIGTERM and shutting the listeners down. 0 to 3600. |
| `GOLDIE_LOG_LEVEL` | `info` | Log level: debug, info, warn or error. |
| `GOLDIE_SWAGGER_UI_ENABLED` | `true` | Serve Swagger UI at /swagger-ui on the public listener, from assets embedded in the binary. true or false; false answers 404. |

## OpenTelemetry

Traces of the public listener and logs are exported over OTLP (http/protobuf) when an
endpoint is set. These variables keep their standard names and are read as defined by
OpenTelemetry, with the validation noted. Admin-port requests are not traced. Request
headers and query strings are not recorded. Other OTEL_* variables, such as samplers,
OTEL_BSP_*, OTEL_BLRP_*, timeouts, TLS and compression, keep the OpenTelemetry SDK's own
handling: goldie neither validates nor documents them. Only the OTLP headers variables
accept a _FILE form; for any other OTEL_* name it is an error.

| Variable | Default | Meaning |
|---|---|---|
| `OTEL_SDK_DISABLED` | (none) | true turns telemetry off even when an endpoint is set. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (none) | OTLP collector base URL (http or https). Setting it, or a signal endpoint, turns on trace and log export. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | (none) | Collector URL for traces only. |
| `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` | (none) | Collector URL for logs only. |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | (none) | Only http/protobuf is supported, and it is the default. Also _TRACES_PROTOCOL and _LOGS_PROTOCOL. |
| `OTEL_EXPORTER_OTLP_HEADERS` | (none) | Collector request headers as key=value pairs, for example an API token. Secret: also accepts OTEL_EXPORTER_OTLP_HEADERS_FILE (and _TRACES_ and _LOGS_ forms). |
| `OTEL_TRACES_EXPORTER` | (none) | otlp or none. none turns off trace export. |
| `OTEL_LOGS_EXPORTER` | (none) | otlp or none. none turns off log export. |
| `OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT` | `1024` | Maximum length of a span attribute value; longer values are truncated. Server spans copy request data such as the user agent and path. |
| `OTEL_SERVICE_NAME` | `goldie` | Overrides the service.name resource attribute. |
| `OTEL_RESOURCE_ATTRIBUTES` | (none) | Extra resource attributes as key=value pairs with percent-encoded values; a malformed value fails startup. goldie always sets service.version and goldie.mode (all), which this cannot override. |
