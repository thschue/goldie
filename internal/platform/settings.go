package platform

import (
	"sort"
	"strings"
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
}

// Settings lists every configuration variable. To add one, add its Env
// constant and a row here, then run `./run regen`.
var Settings = []SettingSpec{
	{EnvAddr, ":8080", "Public (app) listener."},
	{EnvAdminAddr, ":8081", "Admin listener: /livez, /readyz, /version. Must differ from GOLDIE_ADDR."},
	{EnvLogLevel, "info", "Log level: debug, info, warn or error."},
	{EnvDrainSeconds, "25", "Time in seconds between failing readiness on SIGTERM and shutting the listeners down. 0 to 3600."},
	{EnvSwaggerUIEnabled, "true", "Serve Swagger UI at /swagger-ui on the public listener, from assets embedded in the binary. true or false; false answers 404."},
	{EnvContentDir, "", "Required by goldie server. Directory of the dir Source; articles are read from global/articles under it at startup, and invalid content stops the server."},
	{EnvAPIToken, "", "Required by goldie server. Secret: set it with GOLDIE_API_TOKEN_FILE. The static bearer token that every /api/ request must send. Printable ASCII without spaces; a trailing newline in the file is ignored."},
}

// TelemetrySettings lists the standard OpenTelemetry variables goldie honours.
// They keep their standard names, so they are not GOLDIE_ prefixed and are not
// part of Config; SetupTelemetry validates them. Unset, telemetry is off.
var TelemetrySettings = []SettingSpec{
	{"OTEL_SDK_DISABLED", "", "true turns telemetry off even when an endpoint is set."},
	{"OTEL_EXPORTER_OTLP_ENDPOINT", "", "OTLP collector base URL (http or https). Setting it, or a signal endpoint, turns on trace and log export."},
	{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "", "Collector URL for traces only."},
	{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "", "Collector URL for logs only."},
	{"OTEL_EXPORTER_OTLP_PROTOCOL", "", "Only http/protobuf is supported, and it is the default. Also _TRACES_PROTOCOL and _LOGS_PROTOCOL."},
	{"OTEL_EXPORTER_OTLP_HEADERS", "", "Collector request headers as key=value pairs, for example an API token. Secret: also accepts OTEL_EXPORTER_OTLP_HEADERS_FILE (and _TRACES_ and _LOGS_ forms)."},
	{"OTEL_TRACES_EXPORTER", "", "otlp or none. none turns off trace export."},
	{"OTEL_LOGS_EXPORTER", "", "otlp or none. none turns off log export."},
	{"OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT", "1024", "Maximum length of a span attribute value; longer values are truncated. Server spans copy request data such as the user agent and path."},
	{"OTEL_SERVICE_NAME", "goldie", "Overrides the service.name resource attribute."},
	{"OTEL_RESOURCE_ATTRIBUTES", "", "Extra resource attributes. goldie always sets service.version and goldie.mode (all)."},
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
	b.WriteString("Configuration is environment only (PC-04). Every variable also accepts a `NAME_FILE` form\n")
	b.WriteString("that reads the value from a file; setting both is an error (PC-05).\n\n")
	b.WriteString("| Variable | Default | Meaning |\n|---|---|---|\n")
	for _, s := range rows {
		d := "(none)"
		if s.Default != "" {
			d = "`" + cell(s.Default) + "`"
		}
		b.WriteString("| `" + cell(s.Name) + "` | " + d + " | " + cell(s.Meaning) + " |\n")
	}
	b.WriteString("\n## OpenTelemetry\n\n")
	b.WriteString("Traces of the public listener and logs are exported over OTLP (http/protobuf) when an\n")
	b.WriteString("endpoint is set. These variables keep their standard names and are read as defined by\n")
	b.WriteString("OpenTelemetry, with the validation noted. Admin-port requests are not traced. Request\n")
	b.WriteString("headers and query strings are not recorded.\n\n")
	b.WriteString("| Variable | Default | Meaning |\n|---|---|---|\n")
	for _, s := range TelemetrySettings {
		d := "(none)"
		if s.Default != "" {
			d = "`" + cell(s.Default) + "`"
		}
		b.WriteString("| `" + cell(s.Name) + "` | " + d + " | " + cell(s.Meaning) + " |\n")
	}
	return []byte(b.String())
}

// cell makes registry text safe inside a Markdown table cell or code span:
// one line, no pipes, no backticks and no raw HTML.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer("|", `\|`, "<", "&lt;", ">", "&gt;", "`", "'").Replace(s)
}
