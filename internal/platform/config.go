package platform

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// FirstRunHint is the next step for someone who ran a goldie command with
// nothing set up: the server or a client with no server to ask.
const FirstRunHint = "To try goldie on this machine: goldie server --dev"

// Environment variable names. The GOLDIE_ prefix is mandatory (PC-04).
const (
	// The YAML config file the other settings may come from (#846). Not a
	// Settings row: Layers reads it, LoadConfig never sees it.
	EnvConfig = "GOLDIE_CONFIG"

	EnvAddr         = "GOLDIE_ADDR"
	EnvAdminAddr    = "GOLDIE_ADMIN_ADDR"
	EnvLogLevel     = "GOLDIE_LOG_LEVEL"
	EnvDrainSeconds = "GOLDIE_DRAIN_SECONDS"

	EnvSwaggerUIEnabled = "GOLDIE_SWAGGER_UI_ENABLED"
	EnvMode             = "GOLDIE_MODE"

	EnvContentDir = "GOLDIE_CONTENT_DIR"
	EnvAPIToken   = "GOLDIE_API_TOKEN"  // secret: never logged
	EnvAPITokens  = "GOLDIE_API_TOKENS" // secret: never logged

	EnvSessionKey = "GOLDIE_SESSION_KEY" // secret: never logged

	EnvRateLimit = "GOLDIE_RATE_LIMIT"

	EnvMinClientVersion = "GOLDIE_MIN_CLIENT_VERSION"

	// The name instruction blocks give the MCP server (#415).
	EnvMCPName = "GOLDIE_MCP_NAME"

	// Federated activity (ADR 0022).
	EnvActivityPeers         = "GOLDIE_ACTIVITY_PEERS"
	EnvActivityReorderWindow = "GOLDIE_ACTIVITY_REORDER_WINDOW"

	// The links the activity view opens, sent to operator sessions (#807).
	EnvGrafanaURL       = "GOLDIE_GRAFANA_URL"
	EnvTraceURLTemplate = "GOLDIE_TRACE_URL_TEMPLATE"

	// The git Source (#96). Its URL is operator-trusted (ADR-0023).
	EnvGitURL           = "GOLDIE_GIT_URL" // logged with userinfo, query and fragment redacted
	EnvGitRef           = "GOLDIE_GIT_REF"
	EnvGitToken         = "GOLDIE_GIT_TOKEN"           // secret: never logged
	EnvGitSSHKey        = "GOLDIE_GIT_SSH_KEY"         // secret: never logged
	EnvGitSSHKnownHosts = "GOLDIE_GIT_SSH_KNOWN_HOSTS" // secret: never logged
	EnvGitCABundle      = "GOLDIE_GIT_CA_BUNDLE"       // logged only as set or unset
	EnvGitAllowFileURL  = "GOLDIE_GIT_ALLOW_FILE_URL"
	EnvPollInterval     = "GOLDIE_POLL_INTERVAL"

	// The ordered list of Sources (#476): one JSON document.
	EnvSources = "GOLDIE_SOURCES"

	// The server a GOLDIE_MODE=ui process proxies to (#636).
	EnvUpstreamAddr = "GOLDIE_UPSTREAM_ADDR"
)

// MinPollInterval is the shortest GOLDIE_POLL_INTERVAL LoadConfig accepts in
// a release build.
const MinPollInterval = 10 * time.Second

// pollIntervalFloor is a test seam, set only at link time, by the process
// tests' binary: -X github.com/thschue/goldie/internal/platform.pollIntervalFloor=100ms.
// It is a Go duration that lowers the poll interval floor, so those tests can
// poll a git Source within seconds. A release build leaves it empty. Nothing
// reads it from the environment, a file or a flag, so whoever runs the binary
// cannot change it; only whoever links the binary can.
var pollIntervalFloor string

// PollIntervalFloor is the shortest poll interval LoadConfig accepts and the
// server polls at: MinPollInterval, unless the binary was linked with a
// shorter positive pollIntervalFloor. A floor that does not parse, or is not
// shorter, is ignored, so the seam can only lower it.
func PollIntervalFloor() time.Duration {
	if d, err := time.ParseDuration(pollIntervalFloor); err == nil && d > 0 && d < MinPollInterval {
		return d
	}
	return MinPollInterval
}

// MaxActivityReorderWindow is the longest GOLDIE_ACTIVITY_REORDER_WINDOW: no
// live event of a merged activity stream waits longer than this.
const MaxActivityReorderWindow = time.Minute

// The values of GOLDIE_MODE: what the server mounts. ModeAll serves /api, the
// docs, /mcp and the UI; ModeAPI serves /api and the docs only; ModeUI serves
// the UI and proxies /api and the docs to GOLDIE_UPSTREAM_ADDR; ModeMCP
// serves /mcp only, calling the API of GOLDIE_UPSTREAM_ADDR.
const (
	ModeAll = "all"
	ModeAPI = "api"
	ModeUI  = "ui"
	ModeMCP = "mcp"
)

// ParseMode validates a GOLDIE_MODE value; empty is ModeAll. The error names
// the variable and the accepted values, never the value.
func ParseMode(v string) (string, error) {
	switch v {
	case "", ModeAll:
		return ModeAll, nil
	case ModeAPI:
		return ModeAPI, nil
	case ModeUI:
		return ModeUI, nil
	case ModeMCP:
		return ModeMCP, nil
	}
	return ModeAll, fmt.Errorf("%s: must be %s, %s, %s or %s", EnvMode, ModeAll, ModeAPI, ModeUI, ModeMCP)
}

// modeOrAll is mode, or ModeAll when it is empty.
func modeOrAll(mode string) string {
	if mode == "" {
		return ModeAll
	}
	return mode
}

// Config is the effective server configuration. It is read once at startup
// by LoadConfig and never changes afterwards.
type Config struct {
	Addr         string        // public (app) listener
	AdminAddr    string        // admin listener: probes and version
	LogLevel     string        // debug | info | warn | error
	DrainTimeout time.Duration // time between failing readiness and shutdown

	SwaggerUIEnabled bool // serve Swagger UI on the public listener

	// Mode is what the public handler mounts: ModeAll, ModeAPI, ModeUI or ModeMCP. Empty
	// (a Config built by hand) means ModeAll.
	Mode string

	// UpstreamAddr is the base URL of the server ModeUI proxies to and ModeMCP calls; empty in
	// every other mode. LoadConfig has checked it is an http(s) URL with a
	// host and no credentials.
	UpstreamAddr string

	MCPName string // {{mcp_name}} of the instruction blocks; validated by validMCPName

	// Every role that loads the configuration (server and health) validates
	// these at startup.
	RateLimit     RateLimit // the per-token request limit, default applied (ParseRateLimit)
	rateLimitText string    // RateLimit as written, for the startup report

	MinClientVersion     Semver // the oldest X-Goldie-Client version served (ParseSemver); zero for no gate
	minClientVersionText string // MinClientVersion as written, for the startup report

	ActivityPeers             string        // the peers' headless-Service DNS name as written; "" for no peers
	ActivityReorderWindow     time.Duration // the merged activity stream's reorder window, default applied
	activityReorderWindowText string        // ActivityReorderWindow as written, for the startup report

	GrafanaURL       string // the Grafana link of the activity view; "" for none, validated by checkLinkURL
	TraceURLTemplate string // the trace link of the activity view, holding {trace_id}; "" for none

	// SetupTelemetry owns GOLDIE_TELEMETRY_CLIENT_INFO and fails startup on a
	// bad value; this copy is only for the startup report.
	telemetryClientInfo bool

	// The server role validates these at startup; health does not need them.
	// LoadConfig checks only that the two token variables are not both set.
	ContentDir string // root of the dir Source
	APIToken   string // the shorthand for one operator token; secret
	APITokens  string // name:role:token lines, as read; secret

	// SessionKey signs the browser session cookie (ADR 0027); secret. Empty:
	// the server picks a random key per process.
	SessionKey string

	// The GitHub sign-in settings (#642), checked as a whole; none set is sign-in off.
	githubConfig

	// The git Source, as read; LoadConfig checks only that GitURL and
	// ContentDir are not both set. GitURL may carry userinfo: log it only as
	// RedactedGitURL, as the startup report does.
	GitURL           string
	GitRef           string // empty for the remote's HEAD
	GitToken         string // secret
	GitSSHKey        string // secret
	GitSSHKnownHosts string // secret
	GitCABundle      string // PEM; logged only as set or unset
	GitAllowFileURL  bool   // accept a file:// GitURL

	// Sources is GOLDIE_SOURCES, parsed and validated, in listed order (which
	// is preference order); nil when unset. Serving them is a later slice.
	Sources     []SourceSpec
	sourcesText string // the names of Sources, for the startup report

	PollInterval     time.Duration // how often every Source is polled; at least PollIntervalFloor
	pollIntervalText string        // PollInterval as written, for the startup report
}

// Lookup reads one environment variable. os.LookupEnv satisfies it; tests
// pass a map-backed function.
type Lookup func(key string) (string, bool)

// LoadConfig reads and validates the configuration from the environment. All
// problems are reported together, each naming its variable (PC-04) but never
// its value, which may have been read from a _FILE (PC-05).
func LoadConfig(lookup Lookup) (Config, error) {
	var errs []error
	str := func(name, def string) string {
		v, err := Setting(lookup, os.ReadFile, name)
		if err != nil {
			errs = append(errs, err)
			return def
		}
		if v == "" {
			return def
		}
		return v
	}
	cfg := Config{
		Addr:      str(EnvAddr, def(EnvAddr)),
		AdminAddr: str(EnvAdminAddr, def(EnvAdminAddr)),
		LogLevel:  strings.ToLower(str(EnvLogLevel, def(EnvLogLevel))),
	}
	if _, err := ParseLevel(cfg.LogLevel); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", EnvLogLevel, err))
	}
	secs, _ := strconv.Atoi(def(EnvDrainSeconds))
	if raw := str(EnvDrainSeconds, ""); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 3600 {
			errs = append(errs, fmt.Errorf("%s: must be an integer between 0 and 3600", EnvDrainSeconds))
		} else {
			secs = n
		}
	}
	cfg.DrainTimeout = time.Duration(secs) * time.Second
	cfg.ContentDir = str(EnvContentDir, def(EnvContentDir))
	cfg.APIToken = str(EnvAPIToken, def(EnvAPIToken))
	cfg.APITokens = str(EnvAPITokens, def(EnvAPITokens))
	cfg.SessionKey = str(EnvSessionKey, def(EnvSessionKey))
	cfg.githubConfig = loadGitHub(str, &errs)
	if err := CheckAPITokenPairing(cfg.APIToken, cfg.APITokens); err != nil {
		errs = append(errs, err)
	}
	cfg.rateLimitText = str(EnvRateLimit, def(EnvRateLimit))
	if l, err := ParseRateLimit(cfg.rateLimitText); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", EnvRateLimit, err))
	} else {
		cfg.RateLimit = l
	}
	cfg.minClientVersionText = str(EnvMinClientVersion, def(EnvMinClientVersion))
	if cfg.minClientVersionText != "" {
		if v, err := ParseSemver(cfg.minClientVersionText); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", EnvMinClientVersion, err))
		} else {
			cfg.MinClientVersion = v
		}
	}
	cfg.ActivityPeers = str(EnvActivityPeers, def(EnvActivityPeers))
	if cfg.ActivityPeers != "" && !isDNSName(cfg.ActivityPeers) {
		errs = append(errs, fmt.Errorf("%s: must be a DNS name, such as a headless Service's name, without a scheme, port or path", EnvActivityPeers))
		cfg.ActivityPeers = ""
	}
	cfg.activityReorderWindowText = str(EnvActivityReorderWindow, def(EnvActivityReorderWindow))
	if d, err := time.ParseDuration(cfg.activityReorderWindowText); err != nil || d <= 0 || d > MaxActivityReorderWindow {
		// The value is never echoed: time.ParseDuration's error quotes it.
		errs = append(errs, fmt.Errorf("%s: must be a Go duration above 0 and at most %s, such as 2s or 500ms", EnvActivityReorderWindow, MaxActivityReorderWindow))
	} else {
		cfg.ActivityReorderWindow = d
	}
	cfg.GrafanaURL = str(EnvGrafanaURL, def(EnvGrafanaURL))
	if err := checkLinkURL(EnvGrafanaURL, cfg.GrafanaURL, ""); err != nil {
		errs = append(errs, err)
		cfg.GrafanaURL = ""
	}
	cfg.TraceURLTemplate = str(EnvTraceURLTemplate, def(EnvTraceURLTemplate))
	if err := checkLinkURL(EnvTraceURLTemplate, cfg.TraceURLTemplate, TraceIDPlaceholder); err != nil {
		errs = append(errs, err)
		cfg.TraceURLTemplate = ""
	}
	cfg.MCPName = str(EnvMCPName, def(EnvMCPName))
	if !validMCPName(cfg.MCPName) {
		// The value is never echoed: it ends up in text agents read.
		errs = append(errs, fmt.Errorf("%s: must be 1 to 64 letters, digits, hyphens, dots or underscores", EnvMCPName))
		cfg.MCPName = def(EnvMCPName)
	}
	cfg.telemetryClientInfo, _ = clientInfoEnabled(lookup) // SetupTelemetry reports the error
	if m, err := ParseMode(str(EnvMode, def(EnvMode))); err != nil {
		errs = append(errs, err)
		cfg.Mode = ModeAll
	} else {
		cfg.Mode = m
	}
	cfg.SwaggerUIEnabled = true
	switch strings.ToLower(str(EnvSwaggerUIEnabled, def(EnvSwaggerUIEnabled))) {
	case "true":
	case "false":
		cfg.SwaggerUIEnabled = false
	default:
		errs = append(errs, fmt.Errorf("%s: must be true or false", EnvSwaggerUIEnabled))
	}
	cfg.GitURL = str(EnvGitURL, def(EnvGitURL))
	cfg.GitRef = str(EnvGitRef, def(EnvGitRef))
	cfg.GitToken = str(EnvGitToken, def(EnvGitToken))
	cfg.GitSSHKey = str(EnvGitSSHKey, def(EnvGitSSHKey))
	cfg.GitSSHKnownHosts = str(EnvGitSSHKnownHosts, def(EnvGitSSHKnownHosts))
	cfg.GitCABundle = str(EnvGitCABundle, def(EnvGitCABundle))
	sourcesDoc := str(EnvSources, def(EnvSources))
	if err := CheckOneSource(cfg.ContentDir, cfg.GitURL, sourcesDoc != ""); err != nil {
		errs = append(errs, err)
	}
	if sourcesDoc != "" {
		srcs, err := ParseSources(sourcesDoc, lookup, os.ReadFile)
		if err != nil {
			errs = append(errs, err)
		}
		cfg.Sources = srcs
		cfg.sourcesText = sourceNames(srcs)
	}
	switch strings.ToLower(str(EnvGitAllowFileURL, def(EnvGitAllowFileURL))) {
	case "true":
		cfg.GitAllowFileURL = true
	case "false":
	default:
		errs = append(errs, fmt.Errorf("%s: must be true or false", EnvGitAllowFileURL))
	}
	cfg.pollIntervalText = str(EnvPollInterval, def(EnvPollInterval))
	if d, err := time.ParseDuration(cfg.pollIntervalText); err != nil || d < PollIntervalFloor() {
		// The value is never echoed: time.ParseDuration's error quotes it.
		errs = append(errs, fmt.Errorf("%s: must be a Go duration of at least %s, such as 1m or 90s", EnvPollInterval, PollIntervalFloor()))
	} else {
		cfg.PollInterval = d
	}
	errs = append(errs, checkModeSettings(&cfg, str, lookup)...)
	if !strings.Contains(cfg.Addr, ":") {
		errs = append(errs, fmt.Errorf("%s: must be a host:port address", EnvAddr))
	}
	if !strings.Contains(cfg.AdminAddr, ":") {
		errs = append(errs, fmt.Errorf("%s: must be a host:port address", EnvAdminAddr))
	}
	if cfg.Addr == cfg.AdminAddr && !strings.HasSuffix(cfg.Addr, ":0") { // :0 picks distinct ports
		errs = append(errs, fmt.Errorf("%s and %s must differ: the admin port is never the public listener", EnvAddr, EnvAdminAddr))
	}
	return cfg, errors.Join(errs...)
}

// sourceSettings are the variables of the Source: a ModeUI or ModeMCP process serves no
// content, so it refuses them rather than ignoring them.
var sourceSettings = []string{EnvContentDir, EnvGitURL, EnvGitRef, EnvGitToken, EnvGitSSHKey,
	EnvGitSSHKnownHosts, EnvGitCABundle, EnvGitAllowFileURL, EnvSources, EnvPollInterval}

// checkModeSettings reads GOLDIE_UPSTREAM_ADDR into cfg and returns what the
// mode refuses: ModeUI needs the upstream and takes no Source setting, every
// other mode takes no upstream. Errors name the variable and never a value.
func checkModeSettings(cfg *Config, str func(name, def string) string, lookup Lookup) []error {
	var errs []error
	isSet := func(name string) bool {
		v, ok := lookup(name)
		_, file := lookup(name + "_FILE")
		return ok && v != "" || file
	}
	addr := str(EnvUpstreamAddr, def(EnvUpstreamAddr))
	upstream := cfg.Mode == ModeUI || cfg.Mode == ModeMCP
	switch {
	case !upstream && addr != "":
		return append(errs, fmt.Errorf("%s: only %s=%s and %s=%s use an upstream: unset it, or set %s=%s or %s", EnvUpstreamAddr, EnvMode, ModeUI, EnvMode, ModeMCP, EnvMode, ModeUI, ModeMCP))
	case !upstream:
		return nil
	case addr == "":
		errs = append(errs, fmt.Errorf("%s: %s=%s needs it: set it to the base URL of a goldie server, for example http://127.0.0.1:8080", EnvUpstreamAddr, EnvMode, cfg.Mode))
	default:
		if err := CheckServerURL(EnvUpstreamAddr, addr); err != nil {
			errs = append(errs, err)
		} else {
			cfg.UpstreamAddr = addr
		}
	}
	for _, name := range sourceSettings {
		if isSet(name) {
			errs = append(errs, fmt.Errorf("%s: %s=%s serves no content, so it takes no Source setting", name, EnvMode, cfg.Mode))
		}
	}
	return errs
}

// TraceIDPlaceholder is what GOLDIE_TRACE_URL_TEMPLATE must contain: the UI
// replaces it with the trace's ID.
const TraceIDPlaceholder = "{trace_id}"

// checkLinkURL refuses an observability link (#807) that is not an http or
// https URL with a host, that holds userinfo, or that lacks the placeholder
// when one is required. Empty is fine: the link is optional. Errors name env
// and never the value.
func checkLinkURL(env, raw, placeholder string) error {
	if raw == "" {
		return nil
	}
	if _, err := checkHTTPURL(env, raw); err != nil {
		return err
	}
	if placeholder != "" && !strings.Contains(raw, placeholder) {
		return fmt.Errorf("%s: must contain the placeholder %s", env, placeholder)
	}
	return nil
}

// checkHTTPURL is the rule both CheckServerURL and checkLinkURL start from: an
// http or https URL with a host and no userinfo. Errors name env and never raw.
func checkHTTPURL(env, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return nil, fmt.Errorf("%s: must be an http or https URL with a host", env)
	case u.User != nil:
		return nil, fmt.Errorf("%s must not hold credentials", env)
	}
	return u, nil
}

// CheckServerURL is the one rule for the address of another goldie server (the
// upstream of GOLDIE_MODE=ui, and GOLDIE_ADDR as an upstream): an http or https
// URL with a host and no credentials, and https unless the host is loopback,
// so a token or a session never crosses a network in clear. Errors name env,
// the variable that holds addr, and never addr.
func CheckServerURL(env, addr string) error {
	u, err := checkHTTPURL(env, addr)
	if err != nil {
		return err
	}
	if u.Scheme != "https" && !IsLoopbackHost(u.Hostname()) {
		return fmt.Errorf("%s: the upstream must use https unless its host is loopback", env)
	}
	return nil
}

// IsLoopbackHost reports whether host is localhost or a loopback IP.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CheckAPITokenPairing refuses GOLDIE_API_TOKEN and GOLDIE_API_TOKENS set
// together, after their _FILE forms are read: which one counts would be a
// guess. LoadConfig reports it with every other problem, so goldie health
// refuses it too; neither set is the server role's check, because health needs
// no token. The error names both variables and never a token.
func CheckAPITokenPairing(token, tokens string) error {
	if token != "" && tokens != "" {
		return fmt.Errorf("%s and %s are both set: set exactly one", EnvAPIToken, EnvAPITokens)
	}
	return nil
}

// CheckOneSource refuses GOLDIE_CONTENT_DIR and GOLDIE_GIT_URL set together,
// and either of them together with GOLDIE_SOURCES (sources reports that it is
// set), after their _FILE forms are read: a process serves the one Source of
// the single-Source variables, or the list, never a guess between them. Like
// the token pairing it is part of LoadConfig, so goldie health refuses it too;
// none set is the server role's check. The error names the variables and no
// value: the URL may hold a password.
func CheckOneSource(contentDir, gitURL string, sources bool) error {
	if sources {
		for _, v := range []struct{ name, value string }{{EnvContentDir, contentDir}, {EnvGitURL, gitURL}} {
			if v.value != "" {
				return fmt.Errorf("%s and %s are both set: %s declares the Sources, so set only one", EnvSources, v.name, EnvSources)
			}
		}
	}
	if contentDir != "" && gitURL != "" {
		return fmt.Errorf("%s and %s are both set: set only one, or declare the Sources in %s", EnvContentDir, EnvGitURL, EnvSources)
	}
	return nil
}

// validMCPName reports whether s may be {{mcp_name}}: a name an operator
// registers an MCP server under, 1 to 64 ASCII letters, digits, hyphens, dots
// or underscores. Nothing else reaches the text agents are told to follow.
func validMCPName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}

// isDNSName reports whether s is a DNS name as written: dot-separated
// IsDNSLabel labels, at most 253 bytes, without a trailing dot, and not an
// address: neither an IP literal nor a name whose last label is all digits
// (no top-level domain is), such as 10.0.0.1.
func isDNSName(s string) bool {
	if len(s) > 253 || net.ParseIP(s) != nil {
		return false
	}
	last := s[strings.LastIndexByte(s, '.')+1:]
	if strings.Trim(last, "0123456789") == "" {
		return false
	}
	for label := range strings.SplitSeq(s, ".") {
		if !IsDNSLabel(label) {
			return false
		}
	}
	return true
}

// IsDNSLabel reports whether s is one DNS label as written: 1 to 63 ASCII
// letters, digits and hyphens, neither first nor last a hyphen. It is the one
// DNS-label rule: GOLDIE_ACTIVITY_PEERS, a replica's own name and a peer's
// PTR name all use it. Check it before lower-casing: strings.ToLower maps
// some non-ASCII runes, such as the Kelvin sign U+212A, to ASCII letters.
func IsDNSLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// Setting resolves one variable that may also be given as NAME_FILE (PC-05).
// Setting both forms is an error. Every trailing CR and LF in the file is
// dropped, in any order and number; nothing before them is. Errors name the
// variable and never include the value or the file path.
func Setting(lookup Lookup, readFile func(string) ([]byte, error), name string) (string, error) {
	val, hasVal := lookup(name)
	path, hasFile := lookup(name + "_FILE")
	switch {
	case hasVal && hasFile:
		return "", fmt.Errorf("%s and %s_FILE are both set: set exactly one", name, name)
	case hasFile:
		if path == "" {
			return "", fmt.Errorf("%s_FILE is set but empty", name)
		}
		b, err := readFile(path)
		if err != nil {
			return "", fmt.Errorf("%s_FILE: cannot read the file", name)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return val, nil
}

// Report logs the effective configuration once at startup (PC-06): every
// Settings row, in registry order, with the value its Value accessor reads
// from c. A row marked Secret is redacted by that flag alone: only whether it
// is set is logged.
func (c Config) Report(l *slog.Logger) { c.ReportWithSources(l, nil) }

// ReportWithSources is Report with each value beside the layer it came from
// (flag, environment, file or default), as source gives it: Layers.Source, in a
// trailing "source" group keyed by variable name, so every value keeps its
// place and shape. A nil source logs the values alone.
func (c Config) ReportWithSources(l *slog.Logger, source func(name string) string) {
	args := make([]any, 0, 2*len(Settings))
	for _, s := range Settings {
		if s.Value == nil {
			panic("platform: setting " + s.Name + " has no Value accessor in the Settings registry")
		}
		v := s.Value(c)
		if s.Secret {
			v = redacted(v)
		}
		args = append(args, s.Name, v)
	}
	if source != nil {
		from := make([]any, 0, 2*len(Settings))
		for _, s := range Settings {
			from = append(from, s.Name, source(s.Name))
		}
		args = append(args, slog.Group("source", from...))
	}
	l.Info("config", args...)
}

// redacted stands in for a secret value in logs. Anything but a non-empty
// string counts as set, so the value itself never reaches the log.
func redacted(secret any) string {
	if s, ok := secret.(string); ok && s == "" {
		return "(unset)"
	}
	return "(redacted)"
}

// setOrUnset stands in for a value that is logged only as set or unset, such
// as the git CA bundle: not a secret, but bulky and of no use in a log line.
func setOrUnset(v string) string {
	if v == "" {
		return "(unset)"
	}
	return "(set)"
}

// RedactedGitURL is GOLDIE_GIT_URL as it may be logged, the one form the URL
// takes in any log line: redactGitURL of it.
func (c Config) RedactedGitURL() string { return redactGitURL(c.GitURL) }

// redactGitURL is GOLDIE_GIT_URL as the startup report logs it: the userinfo,
// the query and the fragment replaced by (redacted), whether or not the value
// parses. It never trusts url.Parse, which reads usr:pw@host:path as the
// scheme usr with an opaque rest. Everything before the last @ that is not a
// scheme is userinfo, so an @ in a path costs the host, never a password; from
// the first ?, # or character a URL cannot hold (a space, a control or
// non-ASCII character, a quote), the rest is dropped. When such a byte comes
// before the last @, nothing tells a password holding it from a query or
// fragment holding the @, so all but the scheme is dropped.
func redactGitURL(raw string) string {
	if raw == "" {
		return ""
	}
	var b strings.Builder
	rest := raw
	scheme, afterScheme, hasScheme := strings.Cut(raw, "://")
	if hasScheme && isURLScheme(scheme) {
		b.WriteString(scheme + "://")
		rest = afterScheme
	}
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		if end := urlEnd(rest); end < at {
			return b.String() + "(redacted)"
		}
		b.WriteString("(redacted)@")
		rest = rest[at+1:]
	}
	end := urlEnd(rest)
	b.WriteString(rest[:end])
	switch {
	case end == len(rest):
	case rest[end] == '?':
		b.WriteString("?(redacted)")
	case rest[end] == '#':
		b.WriteString("#(redacted)")
	default:
		b.WriteString("(redacted)")
	}
	return b.String()
}

// urlEnd is the index of the first ?, # or byte a URL's authority or path
// cannot hold in s, or len(s) when there is none.
func urlEnd(s string) int {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '?' || c == '#' || !isURLByte(c) {
			return i
		}
	}
	return len(s)
}

// isURLScheme reports whether s is a URL scheme (RFC 3986 3.1): a letter, then
// letters, digits, '+', '-' or '.'.
func isURLScheme(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
		other := '0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'
		if !letter && (i == 0 || !other) {
			return false
		}
	}
	return s != ""
}

// isURLByte reports whether c may appear in the authority or path of a URL
// (RFC 3986: unreserved, sub-delims, ':', '/', '@', '%', '[' and ']').
func isURLByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		strings.IndexByte("-._~!$&'()*+,;=:/@%[]", c) >= 0
}
