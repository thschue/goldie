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
	// redacts a Secret row's value after calling it, so an accessor returns
	// the plain field and never redacts on its own.
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
	{Name: EnvContentDir, Value: func(c Config) any { return c.ContentDir }, Meaning: "Required by goldie server. Directory of the dir Source; articles are read from global/articles under it at startup, and invalid content stops the server."},
	{Name: EnvAPIToken, Value: func(c Config) any { return c.APIToken }, Secret: true, Meaning: "goldie server needs this or GOLDIE_API_TOKENS, not both; both set stops the server and goldie health. The shorthand for one operator token, named api-token: the bearer token an /api/ or /mcp request may send. Printable ASCII without spaces, at least 32 bytes (generate one with openssl rand -hex 32); a trailing newline in the file is ignored and not counted."},
	{Name: EnvAPITokens, Value: func(c Config) any { return c.APITokens }, Secret: true, Meaning: "goldie server needs this or GOLDIE_API_TOKEN, not both; both set stops the server and goldie health. The named API tokens, one per line: name:role:sha256:<hex>, the recommended form, so the server never holds the token itself, or name:role:token. The name (1 to 64 letters, digits, '.', '_' or '-') identifies the client in activity and telemetry; each name and token is used once. The role is reader (search, look up, list, fetch bundles) or operator (also trigger syncs and read activity). <hex> is the SHA-256 of the token in hex (printf %s \"$TOKEN\" | sha256sum); the token behind a hash must be as strong as openssl rand -hex 32 makes it. A plaintext token is printable ASCII without spaces, at least 32 bytes, and must not start with sha256:, which marks a hash. Blank lines and lines starting with # are ignored. GOLDIE_API_TOKENS_FILE keeps every line and drops only the trailing newline. A bad line stops the server, naming the line number, never the token."},
	{Name: EnvRateLimit, Value: func(c Config) any { return c.rateLimitText }, Default: "20/s,burst=40", Meaning: "The request limit per token on every /api/ path and on /mcp: <n>/s or <n>/m, n requests per second or per minute for each token name, optionally followed by ,burst=<b>, the most requests a token may send at once (b defaults to n). n and b are positive integers, and the rate is at most 1e9 per second (60000000000/m). The burst has no upper bound, so a very large burst effectively disables the limit. The limit is held in memory per process, so each replica and each restart starts with full buckets. A request over the limit is answered 429 in the error envelope, with a Retry-After header giving the whole seconds to wait. Requests without a valid token are rejected with 401 before the limit and do not count. An MCP tool call counts twice: once for the /mcp request and once for the API request it makes. Every client using the GOLDIE_API_TOKEN shorthand shares one bucket, under the token name api-token. A malformed value stops the server and goldie health."},
	{Name: EnvMinClientVersion, Value: func(c Config) any { return c.minClientVersionText }, Meaning: "The oldest goldie client the server serves, as a semantic version such as 1.4.0 (MAJOR.MINOR.PATCH, optionally -<prerelease>, without a leading v). The goldie CLI sends X-Goldie-Client: goldie/<version> on every request; on every /api/ path and on /mcp, a client whose version is older (semver precedence, so a prerelease is older than its release) is answered 426 in the error envelope, code upgrade_required, with a hint naming this minimum. Requests without the header, or whose header is not goldie/<semver> (curl, other tools, a dev build), are let through: the header is a courtesy, not a credential. Requests without a valid token are rejected with 401 first, and a refused request does not count against the rate limit. Unset, there is no gate. A value that is not a semantic version stops the server and goldie health."},
	{Name: EnvTelemetryClientInfo, Value: func(c Config) any { return c.telemetryClientInfo }, Default: "false", Meaning: "true or false. true adds the caller's IP address (client.address, from the connection, never from X-Forwarded-For, which any caller can forge) and user agent (user_agent.original) to server spans. Off, spans carry no client address, port or user agent. Takes effect only when trace export is on."},
}

// TelemetrySettings lists the standard OpenTelemetry variables goldie honours.
// They keep their standard names, so they are not GOLDIE_ prefixed and are not
// part of Config; SetupTelemetry validates them. Unset, telemetry is off.
var TelemetrySettings = []SettingSpec{
	{Name: "OTEL_SDK_DISABLED", Meaning: "true turns telemetry off even when an endpoint is set."},
	{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Meaning: "OTLP collector base URL (http or https, without surrounding space); signals go to /v1/traces, /v1/logs and /v1/metrics below it. Setting it, or a signal endpoint, turns on trace, log and metrics export. With an exporter selected and no endpoint, signals go to https://localhost:4318. The scheme decides between plain HTTP and TLS, so OTEL_EXPORTER_OTLP_INSECURE and its signal forms have no effect."},
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
	{Name: "OTEL_RESOURCE_ATTRIBUTES", Meaning: "Extra resource attributes as key=value pairs with percent-encoded values; a malformed value fails startup. goldie always sets service.version and goldie.mode (all), which this cannot override."},
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
	{Name: EnvDir, Meaning: "A directory of content (global/articles under it). search, lookup, status and goldie mcp --transport stdio start an embedded server over it on loopback, with its own random token, and stop it when they end; --dir overrides it. Setting it together with --addr or GOLDIE_ADDR is an error, and GOLDIE_TOKEN is ignored. Content that does not load stops the command. version ignores it."},
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
	b.WriteString("\n## goldie server -dev\n\n")
	b.WriteString("`goldie server -dev [dir]` serves `dir`, else `GOLDIE_CONTENT_DIR`, else the working directory.\n")
	b.WriteString("Both listeners bind to 127.0.0.1 on ports the system picks, and the API takes one random\n")
	b.WriteString("token (32 bytes from the system's secure random source, in hex), made for this run. It prints\n")
	b.WriteString("`export " + EnvAddr + "=http://127.0.0.1:<port> " + EnvToken + "=<token>` once, to stdout, and the\n")
	b.WriteString("token appears nowhere else: not in a log line, the startup report or telemetry. It ignores\n")
	b.WriteString("`" + EnvAddr + "`, `" + EnvAdminAddr + "`, `" + EnvAPIToken + "` and `" + EnvAPITokens + "` and their\n")
	b.WriteString("_FILE forms, so a shell that ran the export line can run it again, and logs one line saying so\n")
	b.WriteString("when any is set. `" + EnvDrainSeconds + "` defaults to 0 here; every other setting applies as for\n")
	b.WriteString("`goldie server`.\n")
	b.WriteString("\n## OpenTelemetry\n\n")
	b.WriteString("Traces of the public listener and logs are exported over OTLP (http/protobuf) when an\n")
	b.WriteString("endpoint is set. These variables keep their standard names and are read as defined by\n")
	b.WriteString("OpenTelemetry, with the validation noted. Admin-port requests are not traced. Request\n")
	b.WriteString("headers and query strings are not recorded. goldie reads every variable listed here through\n")
	b.WriteString("its own lookup and passes it to the SDK explicitly. Other OTEL_* variables, such as\n")
	b.WriteString("OTEL_BSP_*, OTEL_BLRP_*, timeouts, TLS and compression, keep the OpenTelemetry SDK's own\n")
	b.WriteString("handling: goldie neither validates nor documents them, but a value the SDK rejects is never\n")
	b.WriteString("printed; goldie logs one fixed warning instead. Only the OTLP headers variables\n")
	b.WriteString("accept a _FILE form; for any other OTEL_* name it is an error.\n\n")
	table(&b, TelemetrySettings)
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
