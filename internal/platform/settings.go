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
	return []byte(b.String())
}

// cell makes registry text safe inside a Markdown table cell or code span:
// one line, no pipes, no backticks and no raw HTML.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer("|", `\|`, "<", "&lt;", ">", "&gt;", "`", "'").Replace(s)
}
