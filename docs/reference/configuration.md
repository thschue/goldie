<!-- Generated from the settings registry in internal/platform/settings.go by `./run regen`. Do not edit. -->

# Goldie configuration reference

Configuration is environment only (PC-04). Every variable in the table below also accepts
a `NAME_FILE` form that reads the value from a file; setting both is an error (PC-05).

| Variable | Default | Meaning |
|---|---|---|
| `GOLDIE_ACTIVITY_PEERS` | (none) | The DNS name of the headless Service in front of every goldie replica, such as goldie-headless.goldie.svc, so that GET /api/v1/activity?replicas=all merges the activity of all of them (ADR 0022). Federation needs a StatefulSet whose serviceName is that headless Service: a replica finds its peers by the Service's address records and names each by the first label of its PTR record, its pod name. An address without one is skipped, so a Deployment gets only the local stream; one warning per process gives the count of skipped peers. A peer's stream is opened at its resolved address on this server's listen port with the caller's own bearer token, so every peer checks the operator role itself. That token crosses the network between replicas, so protect it with mutual TLS in front of each pod or a NetworkPolicy that lets only goldie pods reach the port. A merged stream (?replicas=all) is for operator tokens, holds at most 8 per replica, merges at most 16 peers (the rest are ignored), and an unreachable peer shows as down without ever naming its address. Unset, there are no peers and ?replicas=all is the local stream. A value that is not a DNS name (an IP address, a scheme, port, path or anything but letters, digits, hyphens and dots) stops the server and goldie health. |
| `GOLDIE_ACTIVITY_REORDER_WINDOW` | `2s` | How long a live event of a merged activity stream (?replicas=all) waits so that older events from other replicas can overtake it: a Go duration such as 2s or 500ms, above 0 and at most 1m. An event that reaches the merge later than this after it was recorded is sent at once and marked late. A value that is not such a duration stops the server and goldie health. |
| `GOLDIE_ADDR` | `:8080` | Public (app) listener. |
| `GOLDIE_ADMIN_ADDR` | `:8081` | Admin listener: /livez, /readyz, /version. Must differ from GOLDIE_ADDR. |
| `GOLDIE_API_TOKEN` | (none) | Secret: set it with GOLDIE_API_TOKEN_FILE. goldie server needs this or GOLDIE_API_TOKENS, not both; both set stops the server and goldie health. The shorthand for one operator token, named api-token: the bearer token an /api/ or /mcp request may send. Printable ASCII without spaces, at least 32 bytes (generate one with openssl rand -hex 32); a trailing newline in the file is ignored and not counted. |
| `GOLDIE_API_TOKENS` | (none) | Secret: set it with GOLDIE_API_TOKENS_FILE. goldie server needs this or GOLDIE_API_TOKEN, not both; both set stops the server and goldie health. The named API tokens, one per line: name:role:sha256:&lt;hex&gt;, the recommended form, so the server never holds the token itself, or name:role:token. The name (1 to 64 letters, digits, '.', '_' or '-') identifies the client in activity and telemetry; each name and token is used once. The role is reader (search, look up, list, fetch bundles) or operator (also trigger syncs and read activity). &lt;hex&gt; is the SHA-256 of the token in hex (printf %s "$TOKEN" \| sha256sum); the token behind a hash must be as strong as openssl rand -hex 32 makes it. A plaintext token is printable ASCII without spaces, at least 32 bytes, and must not start with sha256:, which marks a hash. Blank lines and lines starting with # are ignored. GOLDIE_API_TOKENS_FILE keeps every line and drops only the trailing newline. A bad line stops the server, naming the line number, never the token. |
| `GOLDIE_CONTENT_DIR` | (none) | goldie server needs this or GOLDIE_GIT_URL, not both; neither set stops the server. goldie validate reads it too, when no directory argument is given. Directory of the dir Source; articles are read from global/articles under it at startup, and invalid content stops the server. |
| `GOLDIE_DRAIN_SECONDS` | `25` | Time in seconds between failing readiness on SIGTERM and shutting the listeners down. 0 to 3600. |
| `GOLDIE_GIT_ALLOW_FILE_URL` | `false` | true or false. true lets GOLDIE_GIT_URL be a file:// URL, a repository on the server's own filesystem. |
| `GOLDIE_GIT_CA_BUNDLE` | (none) | PEM certificates to trust, in addition to the system's, for an https GOLDIE_GIT_URL behind an internal CA. The startup report logs only (set) or (unset). |
| `GOLDIE_GIT_REF` | (none) | Branch, tag or full ref name (refs/...) the git Source serves; it must not start with -. Unset, the remote's HEAD. |
| `GOLDIE_GIT_SSH_KEY` | (none) | Secret: set it with GOLDIE_GIT_SSH_KEY_FILE. Unencrypted PEM private key for an ssh GOLDIE_GIT_URL. The file's lines are kept; only the line breaks (LF or CRLF) at its end are dropped. |
| `GOLDIE_GIT_SSH_KNOWN_HOSTS` | (none) | Secret: set it with GOLDIE_GIT_SSH_KNOWN_HOSTS_FILE. known_hosts lines an ssh GOLDIE_GIT_URL's host key is checked against; nothing skips that check. The file's lines are kept; only the line breaks (LF or CRLF) at its end are dropped. |
| `GOLDIE_GIT_TOKEN` | (none) | Secret: set it with GOLDIE_GIT_TOKEN_FILE. Access token for an https GOLDIE_GIT_URL, sent as the basic-auth password. Every trailing CR and LF in the file is dropped. |
| `GOLDIE_GIT_URL` | (none) | Repository of the git Source goldie server serves, instead of GOLDIE_CONTENT_DIR: https://host/path, ssh://[user@]host[:port]/path, user@host:path (scp-like, user required), or file:///path when GOLDIE_GIT_ALLOW_FILE_URL is true. No other form is accepted: not http, git://, git+ssh://, a bare path or a &lt;transport&gt;::address remote helper such as ext::. Setting it together with GOLDIE_CONTENT_DIR stops the server and goldie health; a bad URL, or one starting with -, stops the server naming this variable. The server starts without waiting for the first clone: /readyz fails source:git and /api/v1 answers 503 not_ready until the first load succeeds, and a later failed poll keeps the last good content and logs a warning. Keep credentials out of it: use GOLDIE_GIT_TOKEN or GOLDIE_GIT_SSH_KEY; the startup report logs the URL with its userinfo, query and fragment redacted. The URL is operator-trusted (ADR-0023): any https or ssh host is accepted, private and internal addresses included. Redirects are followed, but credentials are dropped when a redirect leaves the origin, and a redirect to http is refused. file:// is refused unless GOLDIE_GIT_ALLOW_FILE_URL is true. URLs from anyone but the operator need a new decision first. |
| `GOLDIE_LOG_LEVEL` | `info` | Log level: debug, info, warn or error. |
| `GOLDIE_MIN_CLIENT_VERSION` | (none) | The oldest goldie client the server serves, as a semantic version such as 1.4.0 (MAJOR.MINOR.PATCH, optionally -&lt;prerelease&gt;, without a leading v). The goldie CLI sends X-Goldie-Client: goldie/&lt;version&gt; on every request; on every /api/ path (except sign-in and sign-out, ADR 0027) and on /mcp, a client whose version is older (semver precedence, so a prerelease is older than its release) is answered 426 in the error envelope, code upgrade_required, with a hint naming this minimum. Requests without the header, or whose header is not goldie/&lt;semver&gt; (curl, other tools, a dev build), are let through: the header is a courtesy, not a credential. Requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. Unset, there is no gate. A value that is not a semantic version stops the server and goldie health. |
| `GOLDIE_POLL_INTERVAL` | `1m` | How often every Source is polled for changed content, the dir Source and the git Source alike: a Go duration such as 90s, 1m or 1h30m, at least 10s. A value under 10s or not a duration stops the server and goldie health. |
| `GOLDIE_RATE_LIMIT` | `20/s,burst=40` | The request limit per token on every /api/ path (except sign-in and sign-out, ADR 0027) and on /mcp: &lt;n&gt;/s or &lt;n&gt;/m, n requests per second or per minute for each token name, optionally followed by ,burst=&lt;b&gt;, the most requests a token may send at once (b defaults to n). n and b are positive integers, and the rate is at most 1e9 per second (60000000000/m). The burst has no upper bound, so a very large burst effectively disables the limit. The limit is held in memory per process, so each replica and each restart starts with full buckets. A request over the limit is answered 429 in the error envelope, with a Retry-After header giving the whole seconds to wait. Requests without a valid token are rejected with 401 before the limit and do not count. An MCP tool call counts twice: once for the /mcp request and once for the API request it makes. Every client using the GOLDIE_API_TOKEN shorthand shares one bucket, under the token name api-token. A malformed value stops the server and goldie health. |
| `GOLDIE_SESSION_KEY` | (none) | Secret: set it with GOLDIE_SESSION_KEY_FILE. The key that signs the browser session cookie (ADR 0027), at least 32 bytes (generate one with openssl rand -hex 32); a trailing newline in the file is ignored. Unset, the server uses a random key per process, so sessions end on a restart and a replica does not accept another replica's cookie; set the same key on every replica to share sessions, and change it to sign everyone out. A key shorter than 32 bytes stops the server, never echoing the key. It is never derived from an API token. |
| `GOLDIE_SOURCES` | (none) | An ordered list of Sources as one JSON document, instead of GOLDIE_CONTENT_DIR or GOLDIE_GIT_URL; setting it together with either stops the server and goldie health. List order is preference order. Each entry is an object with name (a DNS label, unique), type (git or dir), url (git) or dir (dir), and optionally ref, path, poll (a Go duration of at least 10s; unset, GOLDIE_POLL_INTERVAL), token_env, ssh_key_env and claims {global, projects}; projects are globs. The *_env fields name the variables that hold the secret, read like any setting so NAME_FILE works; the document itself holds no secret. An empty list, an unknown key or any bad entry stops the server and goldie health, naming this variable and the entry, never a value. This version parses and validates the list; serving several Sources comes later. Example: [{"name": "platform", "type": "git", "url": "https://git.example.com/platform/golden-paths.git", "ref": "main", "path": "content", "poll": "60s", "token_env": "GOLDIE_SOURCE_PLATFORM_TOKEN", "claims": {"global": true, "projects": ["git.example.com/platform/*"]}}, {"name": "team-a", "type": "git", "url": "ssh://git@git.example.com/team-a/paths.git", "ssh_key_env": "GOLDIE_SOURCE_TEAM_A_KEY", "claims": {"projects": ["git.example.com/team-a/*"]}}, {"name": "local", "type": "dir", "dir": "/srv/knowledge", "claims": {"global": true}}] |
| `GOLDIE_SWAGGER_UI_ENABLED` | `true` | Serve Swagger UI at /swagger-ui on the public listener, from assets embedded in the binary. true or false; false answers 404. |
| `GOLDIE_TELEMETRY_CLIENT_INFO` | `false` | true or false. true adds the caller's IP address (client.address, from the connection, never from X-Forwarded-For, which any caller can forge) and user agent (user_agent.original) to server spans. Off, spans carry no client address, port or user agent. Spans never carry the Host header (server.address, server.port). Takes effect only when trace export is on. |

## Connection timeouts

These are fixed, not settings. The public listener closes a connection that takes more than
10 seconds to send request headers, more than 30 seconds to send the whole request, or that
sits idle for 60 seconds between requests. It sets no server-wide write timeout, so long
responses keep working; `/mcp` bounds its own requests. The admin listener bounds the whole
request and response at 10 seconds. Shutdown does not wait on these timeouts: it follows
`GOLDIE_DRAIN_SECONDS`.

## goldie server --dev

A first run of plain `goldie server`, with neither a Source (`GOLDIE_CONTENT_DIR` or `GOLDIE_GIT_URL`)
nor an API token (`GOLDIE_API_TOKENS` or `GOLDIE_API_TOKEN`) set, stops with the two errors and
ends with the line "To try goldie on this machine: goldie server --dev". When only one is missing, or
a value is invalid, there is no hint.

`goldie server --dev [dir]` serves `dir`, else `GOLDIE_CONTENT_DIR`, else the working directory.
When that directory has no content, it serves the example content built into the binary (the same
as `examples/knowledge`) and says so once on stderr, naming the directory. A directory with content
is served as it is, never mixed with the examples.
In this mode log records go to stderr, so only the export line goes to stdout. The server runs
until it is stopped, so run it in the background or redirect stdout to a file and read the
export line from there. The file holds the API token, so create it private, and in a subshell so
your own umask stays as it is: `(umask 077; goldie server --dev examples/knowledge > dev.env &)`.
The file is empty until the server prints its line, so run `. ./dev.env` once the server is up.
The subshell leaves the shell no job to bring to the foreground or kill by job number, so stop
the server by name: `pkill -f 'goldie server --dev'`. This stops every dev server you
run, not just the one in this directory.
Both listeners bind to 127.0.0.1 on ports the system picks, and the API takes one random
token (32 bytes from the system's secure random source, in hex), made for this run. It prints
`export GOLDIE_ADDR=http://127.0.0.1:<port> GOLDIE_TOKEN=<token>` once, to stdout, and the
token appears nowhere else: not in a log line, the startup report or telemetry. It ignores
`GOLDIE_ADDR`, `GOLDIE_ADMIN_ADDR`, `GOLDIE_API_TOKEN` and `GOLDIE_API_TOKENS` and their
_FILE forms, so a shell that ran the export line can run it again, and logs one line saying so
when any is set. `GOLDIE_DRAIN_SECONDS` defaults to 0 here; every other setting applies as for
`goldie server`.

## OpenTelemetry

The server's traces of the public listener, logs and metrics are exported over OTLP (http/protobuf) when an
endpoint is set. These variables keep their standard names and are read as defined by
OpenTelemetry, with the validation noted. Admin-port requests are not traced. Request
headers and query strings are not recorded, and a lookup's path is recorded as the route
template /api/v1/articles/{id}, without the id. goldie reads every variable listed here through
its own lookup and passes it to the SDK explicitly. Other OTEL_* variables, such as
OTEL_BSP_*, OTEL_BLRP_*, timeouts, TLS and compression, keep the OpenTelemetry SDK's own
handling: goldie neither validates nor documents them, but a value the SDK rejects is never
printed; goldie logs one fixed warning instead. Only the OTLP headers variables
accept a _FILE form; for any other OTEL_* name it is an error.

The client commands and `goldie mcp --transport stdio` export traces only, with the resource
attribute `goldie.role` `cli` or `mcp` (the server's is `server`; `goldie.mode` stays a server
mode and is not set on them): one client span per API call, sent to the server as a W3C `traceparent`. The
server starts its own trace and links its span to the client span. A client span records no
query string or header, and an article lookup's path only as /api/v1/articles/{id}, which
appears percent-encoded in `url.full` as /api/v1/articles/%7Bid%7D. The logs and metrics
variables do not apply to them, nor does `GOLDIE_TELEMETRY_CLIENT_INFO`. An invalid value of a
variable they read makes them fail at start, naming the variable (PC-04). They send their
spans for at most 2 seconds on exit, and the fixed warning goes to stderr.

| Variable | Default | Meaning |
|---|---|---|
| `OTEL_SDK_DISABLED` | (none) | true turns telemetry off even when an endpoint is set. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (none) | OTLP collector base URL (http or https, without surrounding space); signals go to /v1/traces, /v1/logs and /v1/metrics below it. Setting it, or a signal endpoint, turns on trace, log and metrics export: a generic endpoint also turns metrics on, and OTEL_METRICS_EXPORTER=none opts out of them. With an exporter selected and no endpoint, signals go to https://localhost:4318. The scheme decides between plain HTTP and TLS, so OTEL_EXPORTER_OTLP_INSECURE and its signal forms have no effect. |
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
| `OTEL_RESOURCE_ATTRIBUTES` | (none) | Extra resource attributes as key=value pairs with percent-encoded values; a malformed value fails startup. goldie always sets service.version, goldie.role (server) and goldie.mode (all), which this cannot override. |

### Metrics goldie emits

The server exports these counters when metrics export is on (see `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_METRICS_EXPORTER` above). The meter is named `goldie`.
Each metric has fixed attributes, so no request value (query text, article id, Project id,
Source URL) becomes a label, and each metric keeps at most 2000 attribute combinations. goldie registers no
other instruments: there are no HTTP server duration or runtime metrics.

| Metric | Unit | Attributes | Meaning |
|---|---|---|---|
| `goldie.searches` | `{request}` | `result`: `hit` or `miss` | Searches answered; a miss is a search with no results. |
| `goldie.lookups` | `{request}` | `result`: `hit` or `miss` | Article lookups answered; a miss is an id that was not found. |
| `goldie.syncs` | `{poll}` | `source`: `git` or `dir`; `result`: `ok` or `error` | Finished polls of the configured Source; error when the poll recorded a Source error. |

## Client commands

`goldie search`, `lookup`, `status` and `version` read these variables, and so does
`goldie mcp --transport stdio` (GOLDIE_ADDR, GOLDIE_TOKEN and GOLDIE_DIR).
Only GOLDIE_TOKEN has a _FILE form (GOLDIE_TOKEN_FILE); the client reads no other _FILE variable.

| Variable | Default | Meaning |
|---|---|---|
| `GOLDIE_ADDR` | (none) | Server URL, http:// or https://, that the client commands and goldie mcp --transport stdio talk to; --addr overrides it. The client does not read GOLDIE_ADDR_FILE. version skips a value that is not a URL, such as the server's listen address. |
| `GOLDIE_TOKEN` | (none) | Secret: set it with GOLDIE_TOKEN_FILE. The bearer token that search, lookup, status and goldie mcp --transport stdio send; --token overrides it. status works without it. |
| `GOLDIE_DIR` | (none) | A directory of content (global/articles under it). search, lookup, status and goldie mcp --transport stdio start an embedded server over it on loopback, with its own random token, and stop it when they end; --dir overrides it. Setting it together with --addr, or with GOLDIE_ADDR when --dir is not given, is an error; an explicit --dir wins over GOLDIE_ADDR in the environment, and GOLDIE_TOKEN is ignored. Content that does not load stops the command. In a repository with .goldie/articles, the embedded server also serves the local Scope; with neither --dir nor GOLDIE_DIR it serves the dir from .goldie/config.yaml, or empty content. version ignores it. |
| `GOLDIE_PROJECT` | (none) | The Project as host/org/repo, or a git remote URL (userinfo is dropped). Overrides .goldie/config.yaml and detection from the upstream or origin git remote; --project overrides it. search and lookup send it as project=; status shows it. |
