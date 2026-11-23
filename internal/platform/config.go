package platform

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variable names. The GOLDIE_ prefix is mandatory (PC-04).
const (
	EnvAddr         = "GOLDIE_ADDR"
	EnvAdminAddr    = "GOLDIE_ADMIN_ADDR"
	EnvLogLevel     = "GOLDIE_LOG_LEVEL"
	EnvDrainSeconds = "GOLDIE_DRAIN_SECONDS"

	EnvSwaggerUIEnabled = "GOLDIE_SWAGGER_UI_ENABLED"

	EnvContentDir = "GOLDIE_CONTENT_DIR"
	EnvAPIToken   = "GOLDIE_API_TOKEN"  // secret: never logged
	EnvAPITokens  = "GOLDIE_API_TOKENS" // secret: never logged

	EnvRateLimit = "GOLDIE_RATE_LIMIT"

	EnvMinClientVersion = "GOLDIE_MIN_CLIENT_VERSION"

	// The git Source (#96). Its URL is operator-trusted (ADR-0023).
	EnvGitURL           = "GOLDIE_GIT_URL" // logged with userinfo, query and fragment redacted
	EnvGitRef           = "GOLDIE_GIT_REF"
	EnvGitToken         = "GOLDIE_GIT_TOKEN"           // secret: never logged
	EnvGitSSHKey        = "GOLDIE_GIT_SSH_KEY"         // secret: never logged
	EnvGitSSHKnownHosts = "GOLDIE_GIT_SSH_KNOWN_HOSTS" // secret: never logged
	EnvGitCABundle      = "GOLDIE_GIT_CA_BUNDLE"       // logged only as set or unset
	EnvGitAllowFileURL  = "GOLDIE_GIT_ALLOW_FILE_URL"
	EnvPollInterval     = "GOLDIE_POLL_INTERVAL"
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

// Config is the effective server configuration. It is read once at startup
// by LoadConfig and never changes afterwards.
type Config struct {
	Addr         string        // public (app) listener
	AdminAddr    string        // admin listener: probes and version
	LogLevel     string        // debug | info | warn | error
	DrainTimeout time.Duration // time between failing readiness and shutdown

	SwaggerUIEnabled bool // serve Swagger UI on the public listener

	// Every role that loads the configuration (server and health) validates
	// these at startup.
	RateLimit     RateLimit // the per-token request limit, default applied (ParseRateLimit)
	rateLimitText string    // RateLimit as written, for the startup report

	MinClientVersion     Semver // the oldest X-Goldie-Client version served (ParseSemver); zero for no gate
	minClientVersionText string // MinClientVersion as written, for the startup report

	// SetupTelemetry owns GOLDIE_TELEMETRY_CLIENT_INFO and fails startup on a
	// bad value; this copy is only for the startup report.
	telemetryClientInfo bool

	// The server role validates these at startup; health does not need them.
	// LoadConfig checks only that the two token variables are not both set.
	ContentDir string // root of the dir Source
	APIToken   string // the shorthand for one operator token; secret
	APITokens  string // name:role:token lines, as read; secret

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
	cfg.telemetryClientInfo, _ = clientInfoEnabled(lookup) // SetupTelemetry reports the error
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
	if err := CheckOneSource(cfg.ContentDir, cfg.GitURL); err != nil {
		errs = append(errs, err)
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
// after their _FILE forms are read: until several Sources are configurable
// (ADR 0004), a process serves one. Like the token pairing it is part of
// LoadConfig, so goldie health refuses it too; neither set is the server
// role's check. The error names both variables and neither value: the URL may
// hold a password.
func CheckOneSource(contentDir, gitURL string) error {
	if contentDir != "" && gitURL != "" {
		return fmt.Errorf("%s and %s are both set: for now a process serves one Source, so set only one", EnvContentDir, EnvGitURL)
	}
	return nil
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
func (c Config) Report(l *slog.Logger) {
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
