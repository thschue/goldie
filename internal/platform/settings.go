package platform

import (
	"sort"
	"strings"
	"time"
)

// SettingSpec is one entry of the settings registry. The registry is the
// single source of variable names, defaults and meanings: LoadConfig reads
// names and defaults from it and the configuration reference
// (docs/reference/configuration.md, PC-10) is rendered from it, so code and
// docs cannot disagree.
type SettingSpec struct {
	Name    string // environment variable, GOLDIE_ prefixed (PC-04)
	Default string // value used when the variable is unset or empty
	Meaning string // one-line description; plain text, escaped when rendered

	// Secret marks a value that is never logged: the startup report redacts
	// it, the reference prints the NAME_FILE note, and among the
	// TelemetrySettings only Secret rows accept a _FILE form (PC-05, PC-06).
	Secret bool

	// Value returns the setting's effective value from Config, which the
	// startup report logs. Every Settings row has one; TelemetrySettings and
	// ClientSettings rows are not part of Config and have none. Report
	// redacts a Secret row's value after calling it, so a Secret row's
	// accessor returns the plain field and never redacts on its own. A plain
	// row whose value may hold a credential or is not fit for a log line
	// returns the form the report logs: GOLDIE_GIT_URL redacted,
	// GOLDIE_GIT_CA_BUNDLE as set or unset.
	Value func(Config) any
}

// Settings lists every configuration variable. To add one, add its Env
// constant, its Config field and a row here with its Value accessor (Secret
// if its value must never be logged), then run `./run regen`.
var Settings = []SettingSpec{
	{Name: EnvAddr, Value: func(c Config) any { return c.Addr }, Default: ":8080", Meaning: "Public (app) listener."},
	{Name: EnvAdminAddr, Value: func(c Config) any { return c.AdminAddr }, Default: ":8081", Meaning: "Admin listener: /livez, /readyz, /version. Must differ from GOLDIE_ADDR."},
	{Name: EnvLogLevel, Value: func(c Config) any { return c.LogLevel }, Default: "info", Meaning: "Log level: debug, info, warn or error."},
	{Name: EnvDrainSeconds, Value: func(c Config) any { return int(c.DrainTimeout / time.Second) }, Default: "25", Meaning: "Time in seconds between failing readiness on SIGTERM and shutting the listeners down. 0 to 3600."},
	{Name: EnvSwaggerUIEnabled, Value: func(c Config) any { return c.SwaggerUIEnabled }, Default: "true", Meaning: "Serve Swagger UI at /swagger-ui on the public listener, from assets embedded in the binary. true or false; false answers 404."},
	{Name: EnvContentDir, Value: func(c Config) any { return c.ContentDir }, Meaning: "goldie server needs this or GOLDIE_GIT_URL, not both; neither set stops the server. goldie validate reads it too, when no directory argument is given. Directory of the dir Source; articles are read from global/articles under it at startup, and invalid content stops the server."},
	{Name: EnvAPIToken, Value: func(c Config) any { return c.APIToken }, Secret: true, Meaning: "goldie server needs this or GOLDIE_API_TOKENS, not both; both set stops the server and goldie health. The shorthand for one operator token, named api-token: the bearer token an /api/ or /mcp request may send. Printable ASCII without spaces, at least 32 bytes (generate one with openssl rand -hex 32); a trailing newline in the file is ignored and not counted."},
	{Name: EnvAPITokens, Value: func(c Config) any { return c.APITokens }, Secret: true, Meaning: "goldie server needs this or GOLDIE_API_TOKEN, not both; both set stops the server and goldie health. The named API tokens, one per line: name:role:sha256:<hex>, the recommended form, so the server never holds the token itself, or name:role:token. The name (1 to 64 letters, digits, '.', '_' or '-') identifies the client in activity and telemetry; each name and token is used once. The role is reader (search, look up, list, fetch bundles) or operator (also trigger syncs and read activity). <hex> is the SHA-256 of the token in hex (printf %s \"$TOKEN\" | sha256sum); the token behind a hash must be as strong as openssl rand -hex 32 makes it. A plaintext token is printable ASCII without spaces, at least 32 bytes, and must not start with sha256:, which marks a hash. Blank lines and lines starting with # are ignored. GOLDIE_API_TOKENS_FILE keeps every line and drops only the trailing newline. A bad line stops the server, naming the line number, never the token."},
	{Name: EnvSessionKey, Value: func(c Config) any { return c.SessionKey }, Secret: true, Meaning: "The key that signs the browser session cookie (ADR 0027), at least 32 bytes (generate one with openssl rand -hex 32); a trailing newline in the file is ignored. Unset, the server uses a random key per process, so sessions end on a restart and a replica does not accept another replica's cookie; set the same key on every replica to share sessions, and change it to sign everyone out. A key shorter than 32 bytes stops the server, never echoing the key. It is never derived from an API token."},
	{Name: EnvRateLimit, Value: func(c Config) any { return c.rateLimitText }, Default: "20/s,burst=40", Meaning: "The request limit per token on every /api/ path (except sign-in and sign-out, ADR 0027) and on /mcp: <n>/s or <n>/m, n requests per second or per minute for each token name, optionally followed by ,burst=<b>, the most requests a token may send at once (b defaults to n). n and b are positive integers, and the rate is at most 1e9 per second (60000000000/m). The burst has no upper bound, so a very large burst effectively disables the limit. The limit is held in memory per process, so each replica and each restart starts with full buckets. A request over the limit is answered 429 in the error envelope, with a Retry-After header giving the whole seconds to wait. Requests without a valid token are rejected with 401 before the limit and do not count. An MCP tool call counts twice: once for the /mcp request and once for the API request it makes. Every client using the GOLDIE_API_TOKEN shorthand shares one bucket, under the token name api-token. A malformed value stops the server and goldie health."},
	{Name: EnvMinClientVersion, Value: func(c Config) any { return c.minClientVersionText }, Meaning: "The oldest goldie client the server serves, as a semantic version such as 1.4.0 (MAJOR.MINOR.PATCH, optionally -<prerelease>, without a leading v). The goldie CLI sends X-Goldie-Client: goldie/<version> on every request (goldie mcp adds X-Goldie-Via: mcp, which the gate ignores); on every /api/ path (except sign-in and sign-out, ADR 0027) and on /mcp, a client whose version is older (semver precedence, so a prerelease is older than its release) is answered 426 in the error envelope, code upgrade_required, with a hint naming this minimum. Requests without the header, or whose header is not goldie/<semver> (curl, other tools, a dev build), are let through: the header is a courtesy, not a credential. Requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. Unset, there is no gate. A value that is not a semantic version stops the server and goldie health."},
	{Name: EnvActivityPeers, Value: func(c Config) any { return c.ActivityPeers }, Meaning: "The DNS name of the headless Service in front of every goldie replica, such as goldie-headless.goldie.svc, so that GET /api/v1/activity?replicas=all merges the activity of all of them (ADR 0022). Federation needs a StatefulSet whose serviceName is that headless Service: a replica finds its peers by the Service's address records and names each by the first label of its PTR record, its pod name. An address without one is skipped, so a Deployment gets only the local stream; one warning per process gives the count of skipped peers. A peer's stream is opened at its resolved address on this server's listen port with the caller's own bearer token, so every peer checks the operator role itself. That token crosses the network between replicas, so protect it with mutual TLS in front of each pod or a NetworkPolicy that lets only goldie pods reach the port. A merged stream (?replicas=all) is for operator tokens, holds at most 8 per replica, merges at most 16 peers (the rest are ignored), and an unreachable peer shows as down without ever naming its address. Unset, there are no peers and ?replicas=all is the local stream. A value that is not a DNS name (an IP address, a scheme, port, path or anything but letters, digits, hyphens and dots) stops the server and goldie health."},
	{Name: EnvActivityReorderWindow, Value: func(c Config) any { return c.activityReorderWindowText }, Default: "2s", Meaning: "How long a live event of a merged activity stream (?replicas=all) waits so that older events from other replicas can overtake it: a Go duration such as 2s or 500ms, above 0 and at most 1m. An event that reaches the merge later than this after it was recorded is sent at once and marked late. A value that is not such a duration stops the server and goldie health."},
	{Name: EnvTelemetryClientInfo, Value: func(c Config) any { return c.telemetryClientInfo }, Default: "false", Meaning: "true or false. true adds the caller's IP address (client.address, from the connection, never from X-Forwarded-For, which any caller can forge) and user agent (user_agent.original) to server spans. Off, spans carry no client address, port or user agent. Spans never carry the Host header (server.address, server.port). Takes effect only when trace export is on."},
	{Name: EnvGitURL, Value: func(c Config) any { return c.RedactedGitURL() }, Meaning: "Repository of the git Source goldie server serves, instead of GOLDIE_CONTENT_DIR: https://host/path, ssh://[user@]host[:port]/path, user@host:path (scp-like, user required), or file:///path when GOLDIE_GIT_ALLOW_FILE_URL is true. No other form is accepted: not http, git://, git+ssh://, a bare path or a <transport>::address remote helper such as ext::. Setting it together with GOLDIE_CONTENT_DIR stops the server and goldie health; a bad URL, or one starting with -, stops the server naming this variable. The server starts without waiting for the first clone: /readyz fails source:git and /api/v1 answers 503 not_ready until the first load succeeds, and a later failed poll keeps the last good content and logs a warning. Keep credentials out of it: use GOLDIE_GIT_TOKEN or GOLDIE_GIT_SSH_KEY; the startup report logs the URL with its userinfo, query and fragment redacted. The URL is operator-trusted (ADR-0023): any https or ssh host is accepted, private and internal addresses included. Redirects are followed, but credentials are dropped when a redirect leaves the origin, and a redirect to http is refused. file:// is refused unless GOLDIE_GIT_ALLOW_FILE_URL is true. URLs from anyone but the operator need a new decision first."},
	{Name: EnvGitRef, Value: func(c Config) any { return c.GitRef }, Meaning: "Branch, tag or full ref name (refs/...) the git Source serves; it must not start with -. Unset, the remote's HEAD."},
	{Name: EnvGitToken, Value: func(c Config) any { return c.GitToken }, Secret: true, Meaning: "Access token for an https GOLDIE_GIT_URL, sent as the basic-auth password. Every trailing CR and LF in the file is dropped."},
	{Name: EnvGitSSHKey, Value: func(c Config) any { return c.GitSSHKey }, Secret: true, Meaning: "Unencrypted PEM private key for an ssh GOLDIE_GIT_URL. The file's lines are kept; only the line breaks (LF or CRLF) at its end are dropped."},
	{Name: EnvGitSSHKnownHosts, Value: func(c Config) any { return c.GitSSHKnownHosts }, Secret: true, Meaning: "known_hosts lines an ssh GOLDIE_GIT_URL's host key is checked against; nothing skips that check. The file's lines are kept; only the line breaks (LF or CRLF) at its end are dropped."},
	{Name: EnvGitCABundle, Value: func(c Config) any { return setOrUnset(c.GitCABundle) }, Meaning: "PEM certificates to trust, in addition to the system's, for an https GOLDIE_GIT_URL behind an internal CA. The startup report logs only (set) or (unset)."},
	{Name: EnvGitAllowFileURL, Value: func(c Config) any { return c.GitAllowFileURL }, Default: "false", Meaning: "true or false. true lets GOLDIE_GIT_URL be a file:// URL, a repository on the server's own filesystem."},
	{Name: EnvSources, Value: func(c Config) any { return c.sourcesText }, Meaning: "An ordered list of Sources as one JSON document, instead of GOLDIE_CONTENT_DIR or GOLDIE_GIT_URL; setting it together with either stops the server and goldie health. List order is preference order. Each entry is an object with name (a DNS label, unique), type (git or dir), url (git) or dir (dir), and optionally ref, path, poll (a Go duration of at least 10s; unset, GOLDIE_POLL_INTERVAL), token_env, ssh_key_env and claims {global, projects}; projects are globs. The *_env fields name the variables that hold the secret, read like any setting so NAME_FILE works; the document itself holds no secret. An empty list, an unknown key or any bad entry stops the server and goldie health, naming this variable and the entry, never a value. This version parses and validates the list; serving several Sources comes later. Example: [{\"name\": \"platform\", \"type\": \"git\", \"url\": \"https://git.example.com/platform/golden-paths.git\", \"ref\": \"main\", \"path\": \"content\", \"poll\": \"60s\", \"token_env\": \"GOLDIE_SOURCE_PLATFORM_TOKEN\", \"claims\": {\"global\": true, \"projects\": [\"git.example.com/platform/*\"]}}, {\"name\": \"team-a\", \"type\": \"git\", \"url\": \"ssh://git@git.example.com/team-a/paths.git\", \"ssh_key_env\": \"GOLDIE_SOURCE_TEAM_A_KEY\", \"claims\": {\"projects\": [\"git.example.com/team-a/*\"]}}, {\"name\": \"local\", \"type\": \"dir\", \"dir\": \"/srv/knowledge\", \"claims\": {\"global\": true}}]"},
	{Name: EnvPollInterval, Value: func(c Config) any { return c.pollIntervalText }, Default: "1m", Meaning: "How often every Source is polled for changed content, the dir Source and the git Source alike: a Go duration such as 90s, 1m or 1h30m, at least 10s. A value under 10s or not a duration stops the server and goldie health."},
	{Name: EnvMCPName, Value: func(c Config) any { return c.MCPName }, Default: "goldie", Meaning: "The name the MCP server is registered under in your agents' configuration. It replaces {{mcp_name}} in the instruction blocks GET /api/v1/instructions serves, so a block tells an agent to ask the MCP server by this name; {{cli}} is always goldie. 1 to 64 letters, digits, hyphens, dots or underscores; anything else stops the server and goldie health."},
}

// TelemetrySettings lists the standard OpenTelemetry variables goldie honours.
// They keep their standard names, so they are not GOLDIE_ prefixed and are not
// part of Config; SetupTelemetry validates them. Unset, telemetry is off.
var TelemetrySettings = []SettingSpec{
	{Name: "OTEL_SDK_DISABLED", Meaning: "true turns telemetry off even when an endpoint is set."},
	{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Meaning: "OTLP collector base URL (http or https, without surrounding space); signals go to /v1/traces, /v1/logs and /v1/metrics below it. Setting it, or a signal endpoint, turns on trace, log and metrics export: a generic endpoint also turns metrics on, and OTEL_METRICS_EXPORTER=none opts out of them. With an exporter selected and no endpoint, signals go to https://localhost:4318. The scheme decides between plain HTTP and TLS, so OTEL_EXPORTER_OTLP_INSECURE and its signal forms have no effect."},
	{Name: "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", Meaning: "Collector URL for traces only, used as-is; wins over the base URL."},
	{Name: "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", Meaning: "Collector URL for logs only, used as-is; wins over the base URL."},
	{Name: "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", Meaning: "Collector URL for metrics only, used as-is; wins over the base URL."},
	{Name: "OTEL_EXPORTER_OTLP_PROTOCOL", Meaning: "Only http/protobuf is supported, and it is the default. Also _TRACES_PROTOCOL, _LOGS_PROTOCOL and _METRICS_PROTOCOL."},
	{Name: "OTEL_EXPORTER_OTLP_HEADERS", Secret: true, Meaning: "Collector request headers as key=value pairs, for example an API token."},
	{Name: "OTEL_EXPORTER_OTLP_TRACES_HEADERS", Secret: true, Meaning: "Collector request headers for traces only, as key=value pairs; wins over OTEL_EXPORTER_OTLP_HEADERS."},
	{Name: "OTEL_EXPORTER_OTLP_LOGS_HEADERS", Secret: true, Meaning: "Collector request headers for logs only, as key=value pairs; wins over OTEL_EXPORTER_OTLP_HEADERS."},
	{Name: "OTEL_EXPORTER_OTLP_METRICS_HEADERS", Secret: true, Meaning: "Collector request headers for metrics only, as key=value pairs; wins over OTEL_EXPORTER_OTLP_HEADERS."},
	{Name: "OTEL_TRACES_EXPORTER", Meaning: "otlp or none. none turns off trace export."},
	{Name: "OTEL_LOGS_EXPORTER", Meaning: "otlp or none. none turns off log export."},
	{Name: "OTEL_METRICS_EXPORTER", Meaning: "otlp or none. none turns off metrics export."},
	{Name: "OTEL_METRIC_EXPORT_INTERVAL", Default: "60000", Meaning: "Milliseconds between metric exports, a positive integer; anything else fails startup."},
	{Name: "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", Default: "cumulative", Meaning: "cumulative, delta or lowmemory (delta for counters and histograms, cumulative for up-down counters)."},
	{Name: "OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT", Default: "1024", Meaning: "Maximum length of a span attribute value; longer values are truncated. Server spans copy request data such as the user agent and path, so a higher value lets clients grow the spans held in memory. Falls back to OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT. A value that is not a non-negative integer fails startup."},
	{Name: "OTEL_TRACES_SAMPLER", Default: "parentbased_always_on", Meaning: "Trace sampler: always_on, always_off, traceidratio or their parentbased_ forms."},
	{Name: "OTEL_TRACES_SAMPLER_ARG", Default: "1", Meaning: "Sampling ratio from 0 to 1, for the traceidratio samplers."},
	{Name: "OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT", Default: "128", Meaning: "Maximum attributes per span (falls back to OTEL_ATTRIBUTE_COUNT_LIMIT). OTEL_SPAN_EVENT_COUNT_LIMIT, OTEL_SPAN_LINK_COUNT_LIMIT, OTEL_EVENT_ATTRIBUTE_COUNT_LIMIT and OTEL_LINK_ATTRIBUTE_COUNT_LIMIT cap events, links and their attributes, 128 each. A value that is not a non-negative integer fails startup."},
	{Name: "OTEL_SERVICE_NAME", Default: defaultServiceName, Meaning: "Overrides the service.name resource attribute."},
	{Name: "OTEL_RESOURCE_ATTRIBUTES", Meaning: "Extra resource attributes as key=value pairs with percent-encoded values; a malformed value fails startup. goldie always sets service.version, goldie.role (server) and goldie.mode (all), which this cannot override."},
}

// EnvToken is the API token the client commands send. It is a secret, so it
// also has a _FILE form (PC-05).
const EnvToken = "GOLDIE_TOKEN"

// EnvProject names the Project the client commands work for. It overrides
// the detection from git remotes and is overridden by --project.
const EnvProject = "GOLDIE_PROJECT"

// EnvDir is the directory of content the client commands serve from an
// embedded server, so they need no server started by hand. --dir overrides it.
const EnvDir = "GOLDIE_DIR"

// ClientSettings lists the variables the client commands (search, lookup,
// status, version) and goldie mcp --transport stdio read. They are not server configuration: LoadConfig
// ignores them, and the reference renders them in a table of their own.
var ClientSettings = []SettingSpec{
	{Name: EnvAddr, Meaning: "Server URL, http:// or https://, that the client commands and goldie mcp --transport stdio talk to; --addr overrides it. The client does not read GOLDIE_ADDR_FILE. version skips a value that is not a URL, such as the server's listen address."},
	{Name: EnvToken, Secret: true, Meaning: "The bearer token that search, lookup, status and goldie mcp --transport stdio send; --token overrides it. status works without it."},
	{Name: EnvDir, Meaning: "A directory of content (global/articles under it). search, lookup, status and goldie mcp --transport stdio start an embedded server over it on loopback, with its own random token, and stop it when they end; --dir overrides it. Setting it together with --addr, or with GOLDIE_ADDR when --dir is not given, is an error; an explicit --dir wins over GOLDIE_ADDR in the environment, and GOLDIE_TOKEN is ignored. Content that does not load stops the command. In a repository with .goldie/articles, the embedded server also serves the local Scope; with neither --dir nor GOLDIE_DIR it serves the dir from .goldie/config.yaml, or empty content. version ignores it."},
	{Name: EnvProject, Meaning: "The Project as host/org/repo, or a git remote URL (userinfo is dropped). Overrides .goldie/config.yaml and detection from the upstream or origin git remote; --project overrides it. search and lookup send it as project=; status shows it."},
}

// def returns the registered default of a variable. A variable missing from
// the registry is a programming error, so it panics at the first LoadConfig.
func def(name string) string {
	for _, s := range Settings {
		if s.Name == name {
			return s.Default
		}
	}
	panic("platform: setting " + name + " is not in the Settings registry")
}

// ConfigReference renders a settings registry as the Markdown configuration
// reference. The output is deterministic: rows are sorted by name.
func ConfigReference(settings []SettingSpec) []byte {
	rows := append([]SettingSpec(nil), settings...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	var b strings.Builder
	b.WriteString("<!-- Generated from the settings registry in internal/platform/settings.go by `./run regen`. Do not edit. -->\n\n")
	b.WriteString("# Goldie configuration reference\n\n")
	b.WriteString("Configuration is environment only (PC-04). Every variable in the table below also accepts\n")
	b.WriteString("a `NAME_FILE` form that reads the value from a file; setting both is an error (PC-05).\n\n")
	table(&b, rows)
	b.WriteString("\n## Connection timeouts\n\n")
	b.WriteString("These are fixed, not settings. The public listener closes a connection that takes more than\n")
	b.WriteString("10 seconds to send request headers, more than 30 seconds to send the whole request, or that\n")
	b.WriteString("sits idle for 60 seconds between requests. It sets no server-wide write timeout, so long\n")
	b.WriteString("responses keep working; `/mcp` bounds its own requests. The admin listener bounds the whole\n")
	b.WriteString("request and response at 10 seconds. Shutdown does not wait on these timeouts: it follows\n")
	b.WriteString("`GOLDIE_DRAIN_SECONDS`.\n")
	b.WriteString("\n## goldie server --dev\n\n")
	b.WriteString("A first run of plain `goldie server`, with neither a Source (`GOLDIE_CONTENT_DIR` or `GOLDIE_GIT_URL`)\n")
	b.WriteString("nor an API token (`GOLDIE_API_TOKENS` or `GOLDIE_API_TOKEN`) set, stops with the two errors and\n")
	b.WriteString("ends with the line \"To try goldie on this machine: goldie server --dev\". When only one is missing, or\n")
	b.WriteString("a value is invalid, there is no hint.\n\n")
	b.WriteString("`goldie server --dev [dir]` serves `dir`, else `GOLDIE_CONTENT_DIR`, else the working directory.\n")
	b.WriteString("When that directory has no content, it serves the example content built into the binary (the same\n")
	b.WriteString("as `examples/knowledge`) and says so once on stderr, naming the directory. A directory with content\n")
	b.WriteString("is served as it is, never mixed with the examples.\n")
	b.WriteString("In this mode log records go to stderr, so only the export line goes to stdout. The server runs\n")
	b.WriteString("until it is stopped, so run it in the background or redirect stdout to a file and read the\n")
	b.WriteString("export line from there. The file holds the API token, so create it private, and in a subshell so\n")
	b.WriteString("your own umask stays as it is: `(umask 077; goldie server --dev examples/knowledge > dev.env &)`.\n")
	b.WriteString("The file is empty until the server prints its line, so run `. ./dev.env` once the server is up.\n")
	b.WriteString("The subshell leaves the shell no job to bring to the foreground or kill by job number, so stop\n")
	b.WriteString("the server by name: `pkill -f 'goldie server --dev'`. This stops every dev server you\n")
	b.WriteString("run, not just the one in this directory.\n")
	b.WriteString("Both listeners bind to 127.0.0.1 on ports the system picks, and the API takes one random\n")
	b.WriteString("token (32 bytes from the system's secure random source, in hex), made for this run. It prints\n")
	b.WriteString("`export " + EnvAddr + "=http://127.0.0.1:<port> " + EnvToken + "=<token>` once, to stdout, and the\n")
	b.WriteString("token appears nowhere else: not in a log line, the startup report or telemetry. It ignores\n")
	b.WriteString("`" + EnvAddr + "`, `" + EnvAdminAddr + "`, `" + EnvAPIToken + "` and `" + EnvAPITokens + "` and their\n")
	b.WriteString("_FILE forms, so a shell that ran the export line can run it again, and logs one line saying so\n")
	b.WriteString("when any is set. `" + EnvDrainSeconds + "` defaults to 0 here; every other setting applies as for\n")
	b.WriteString("`goldie server`.\n")
	b.WriteString("\n## OpenTelemetry\n\n")
	b.WriteString("The server's traces of the public listener, logs and metrics are exported over OTLP (http/protobuf) when an\n")
	b.WriteString("endpoint is set. These variables keep their standard names and are read as defined by\n")
	b.WriteString("OpenTelemetry, with the validation noted. Admin-port requests are not traced. Request\n")
	b.WriteString("headers and query strings are not recorded, and a lookup's path is recorded as the route\n")
	b.WriteString("template /api/v1/articles/{id}, without the id. goldie reads every variable listed here through\n")
	b.WriteString("its own lookup and passes it to the SDK explicitly. Other OTEL_* variables, such as\n")
	b.WriteString("OTEL_BSP_*, OTEL_BLRP_*, timeouts, TLS and compression, keep the OpenTelemetry SDK's own\n")
	b.WriteString("handling: goldie neither validates nor documents them, but a value the SDK rejects is never\n")
	b.WriteString("printed; goldie logs one fixed warning instead. Only the OTLP headers variables\n")
	b.WriteString("accept a _FILE form; for any other OTEL_* name it is an error.\n\n")
	b.WriteString("The client commands and `goldie mcp --transport stdio` export traces only, with the resource\n")
	b.WriteString("attribute `goldie.role` `cli` or `mcp` (the server's is `server`; `goldie.mode` stays a server\n")
	b.WriteString("mode and is not set on them): one client span per API call, sent to the server as a W3C `traceparent`. The\n")
	b.WriteString("server starts its own trace and links its span to the client span. A client span records no\n")
	b.WriteString("query string or header, and an article lookup's path only as /api/v1/articles/{id}, which\n")
	b.WriteString("appears percent-encoded in `url.full` as /api/v1/articles/%7Bid%7D. The logs and metrics\n")
	b.WriteString("variables do not apply to them, nor does `" + EnvTelemetryClientInfo + "`. An invalid value of a\n")
	b.WriteString("variable they read makes them fail at start, naming the variable (PC-04). They send their\n")
	b.WriteString("spans for at most 2 seconds on exit, and the fixed warning goes to stderr.\n\n")
	table(&b, TelemetrySettings)
	b.WriteString("\n### Metrics goldie emits\n\n")
	b.WriteString("The server exports these counters when metrics export is on (see `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_METRICS_EXPORTER` above). The meter is named `goldie`.\n")
	b.WriteString("Each metric has fixed attributes, so no request value (query text, article id, Project id,\n")
	b.WriteString("Source URL) becomes a label, and each metric keeps at most 2000 attribute combinations. goldie registers no\n")
	b.WriteString("other instruments: there are no HTTP server duration or runtime metrics.\n\n")
	b.WriteString("| Metric | Unit | Attributes | Meaning |\n|---|---|---|---|\n")
	b.WriteString("| `goldie.searches` | `{request}` | `result`: `hit` or `miss` | Searches answered; a miss is a search with no results. |\n")
	b.WriteString("| `goldie.lookups` | `{request}` | `result`: `hit` or `miss` | Article lookups answered; a miss is an id that was not found. |\n")
	b.WriteString("| `goldie.skill.gets` | `{request}` | `name`: the skill; `scope`: `global` or `project`; `via`: `cli`, `mcp` or `other` | Skill gets that found the skill. |\n")
	b.WriteString("| `goldie.syncs` | `{poll}` | `source`: `git` or `dir`; `result`: `ok` or `error` | Finished polls of the configured Source; error when the poll recorded a Source error. |\n")
	b.WriteString("| `goldie.sync.duration` | `s` | `source`: `git` or `dir`; `result`: `ok` or `error` | Histogram of how long each Source poll took, in seconds (buckets 0.05 to 60); a failed poll is recorded with `error`. |\n")
	b.WriteString("| `goldie.mcp.tool.calls` | `{call}` | `tool`: one of the eight tool names or `other`; `result`: `ok` or `error` | MCP tool calls answered by the server; error when the tool result is an error, rate-limited calls included. |\n")
	b.WriteString("\nA skill get that answers 404 is not counted, so a name a caller makes up never becomes a `name` label; `name` is the skill's own spelling, whatever case the caller used.\n")
	b.WriteString("`scope` is `global` for a skill every Project gets and `project` for a skill one Project defines; a skill from the local `.goldie/` Scope counts as `project`.\n")
	b.WriteString("`via` is `cli` for the goldie CLI (dev builds included), `mcp` for an agent through `goldie mcp` or the server's `/mcp`, and `other` for curl, the web UI, any other client or no header.\n")
	b.WriteString("It is read from `X-Goldie-Client` and `X-Goldie-Via`. The client declares it and nothing authenticates it, a courtesy and not a credential: any caller with a token can claim `mcp` or `cli`, so use the metric for trends, not for audit.\n")
	b.WriteString("\n## Client commands\n\n")
	b.WriteString("`goldie search`, `lookup`, `status` and `version` read these variables, and so does\n")
	b.WriteString("`goldie mcp --transport stdio` (GOLDIE_ADDR, GOLDIE_TOKEN and GOLDIE_DIR).\n")
	b.WriteString("Only " + EnvToken + " has a _FILE form (" + EnvToken + "_FILE); the client reads no other _FILE variable.\n\n")
	table(&b, ClientSettings)
	return []byte(b.String())
}

// table renders settings as a Markdown table, in the given order.
func table(b *strings.Builder, rows []SettingSpec) {
	b.WriteString("| Variable | Default | Meaning |\n|---|---|---|\n")
	for _, s := range rows {
		d := "(none)"
		if s.Default != "" {
			d = "`" + cell(s.Default) + "`"
		}
		meaning := s.Meaning
		if s.Secret {
			meaning = "Secret: set it with " + s.Name + "_FILE. " + meaning
		}
		b.WriteString("| `" + cell(s.Name) + "` | " + d + " | " + cell(meaning) + " |\n")
	}
}

// secretSet returns the names of the rows marked Secret.
func secretSet(rows []SettingSpec) map[string]bool {
	set := map[string]bool{}
	for _, s := range rows {
		if s.Secret {
			set[s.Name] = true
		}
	}
	return set
}

// cell makes registry text safe inside a Markdown table cell or code span:
// one line, no pipes, no backticks and no raw HTML.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer("|", `\|`, "<", "&lt;", ">", "&gt;", "`", "'").Replace(s)
}
