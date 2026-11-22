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
	EnvAPIToken   = "GOLDIE_API_TOKEN" // secret: never logged
)

// Config is the effective server configuration. It is read once at startup
// by LoadConfig and never changes afterwards.
type Config struct {
	Addr         string        // public (app) listener
	AdminAddr    string        // admin listener: probes and version
	LogLevel     string        // debug | info | warn | error
	DrainTimeout time.Duration // time between failing readiness and shutdown

	SwaggerUIEnabled bool // serve Swagger UI on the public listener

	// The server role validates these at startup; health does not need them.
	ContentDir string // root of the dir Source
	APIToken   string // static bearer token for /api/; secret
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
	cfg.SwaggerUIEnabled = true
	switch strings.ToLower(str(EnvSwaggerUIEnabled, def(EnvSwaggerUIEnabled))) {
	case "true":
	case "false":
		cfg.SwaggerUIEnabled = false
	default:
		errs = append(errs, fmt.Errorf("%s: must be true or false", EnvSwaggerUIEnabled))
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

// Setting resolves one variable that may also be given as NAME_FILE (PC-05).
// Setting both forms is an error. A trailing newline in the file is trimmed.
// Errors name the variable and never include the value or the file path.
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

// Report logs the effective configuration once at startup (PC-06). Secret
// settings are redacted: only whether they are set is logged.
func (c Config) Report(l *slog.Logger) {
	l.Info("config",
		EnvAddr, c.Addr,
		EnvAdminAddr, c.AdminAddr,
		EnvLogLevel, c.LogLevel,
		EnvDrainSeconds, int(c.DrainTimeout/time.Second),
		EnvSwaggerUIEnabled, c.SwaggerUIEnabled,
		EnvContentDir, c.ContentDir,
		EnvAPIToken, redacted(c.APIToken),
	)
}

// redacted stands in for a secret value in logs.
func redacted(secret string) string {
	if secret == "" {
		return "(unset)"
	}
	return "(redacted)"
}
