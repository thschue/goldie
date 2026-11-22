package app

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// PublicHandler takes platform.Config (#186): the Config LoadConfig returns,
// with the typed RateLimit (#178) and the typed MinClientVersion, used as is,
// and the server's component logger. There is no second options struct to
// copy settings into. Every setting problem PublicHandler finds is reported
// in one *ConfigError, joined with errors.Join, one problem per line, each
// naming its variable and none echoing a value, so the operator fixes them
// all in one restart. A RateLimit of zero requests is a setting problem, not
// "unlimited": the handler fails closed.

// loadedConfig is the Config LoadConfig reads from env, the way the server
// hands it to PublicHandler.
func loadedConfig(t *testing.T, env map[string]string) platform.Config {
	t.Helper()
	cfg, err := platform.LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// configHandler is PublicHandler over cfg with the test content and the
// default rate limit, as LoadConfig would fill them in.
func configHandler(t *testing.T, cfg platform.Config) http.Handler {
	t.Helper()
	cfg.ContentDir = testContent
	cfg.RateLimit = platform.DefaultRateLimit()
	h, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	return h
}

// semver is s parsed as LoadConfig parses GOLDIE_MIN_CLIENT_VERSION.
func semver(t *testing.T, s string) platform.Semver {
	t.Helper()
	v, err := platform.ParseSemver(s)
	if err != nil {
		t.Fatalf("ParseSemver(%q): %v", s, err)
	}
	return v
}

// The Config LoadConfig returns is what PublicHandler serves from, every
// field as read: the content dir and named tokens, Swagger UI switched off,
// the rate limit and the minimum client version.
func TestPublicHandlerServesTheConfigLoadConfigRead(t *testing.T) {
	cfg := loadedConfig(t, map[string]string{
		platform.EnvContentDir:       testContent,
		platform.EnvAPITokens:        namedTokenLines(),
		platform.EnvSwaggerUIEnabled: "false",
		platform.EnvRateLimit:        "1/m,burst=1",
		platform.EnvMinClientVersion: minVersion,
	})
	h, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatalf("PublicHandler(LoadConfig()): %v", err)
	}
	current := func(r *http.Request) { r.Header.Set(clientHeader, "goldie/"+minVersion) }
	old := func(r *http.Request) { r.Header.Set(clientHeader, "goldie/1.3.9") }
	if rec := serveAs(t, h, lookupPath, current); rec.Code != http.StatusOK {
		t.Fatalf("reader token from %s: status %d, want 200: %s", platform.EnvAPITokens, rec.Code, rec.Body)
	}
	if rec := serveAs(t, h, lookupPath, old); rec.Code != http.StatusUpgradeRequired {
		t.Errorf("client older than %s=%s: status %d, want 426", platform.EnvMinClientVersion, minVersion, rec.Code)
	}
	if rec := serveAs(t, h, lookupPath, current); rec.Code != http.StatusTooManyRequests {
		t.Errorf("second request under %s=1/m,burst=1: status %d, want 429", platform.EnvRateLimit, rec.Code)
	}
	if rec := serveAs(t, h, "/swagger-ui", func(*http.Request) {}); rec.Code != http.StatusNotFound {
		t.Errorf("/swagger-ui with %s=false: status %d, want 404", platform.EnvSwaggerUIEnabled, rec.Code)
	}
}

// PublicHandler takes the typed minimum client version as is: a Semver in
// Config gates older clients, and the zero Semver is no gate.
func TestPublicHandlerTakesTheTypedMinClientVersionAsIs(t *testing.T) {
	gated := configHandler(t, platform.Config{APITokens: namedTokenLines(), MinClientVersion: semver(t, minVersion)})
	if rec := serveAs(t, gated, lookupPath, func(r *http.Request) { r.Header.Set(clientHeader, "goldie/1.3.9") }); rec.Code != http.StatusUpgradeRequired {
		t.Errorf("minimum %s, goldie/1.3.9: status %d, want 426", minVersion, rec.Code)
	}
	if rec := serveAs(t, gated, lookupPath, func(r *http.Request) { r.Header.Set(clientHeader, "goldie/"+minVersion) }); rec.Code != http.StatusOK {
		t.Errorf("minimum %s, goldie/%s: status %d, want 200", minVersion, minVersion, rec.Code)
	}
	open := configHandler(t, platform.Config{APITokens: namedTokenLines(), MinClientVersion: platform.Semver{}})
	if rec := serveAs(t, open, lookupPath, func(r *http.Request) { r.Header.Set(clientHeader, "goldie/0.0.1") }); rec.Code != http.StatusOK {
		t.Errorf("zero minimum, goldie/0.0.1: status %d, want 200 (no gate)", rec.Code)
	}
}

// configErrors calls PublicHandler with cfg and returns its error, which must
// be a *ConfigError, and the error's lines.
func configErrors(t *testing.T, label string, cfg platform.Config) (error, []string) {
	t.Helper()
	h, err := PublicHandler(cfg, nil)
	if err == nil {
		t.Fatalf("%s: PublicHandler returned a handler, want a *ConfigError", label)
	}
	if h != nil {
		t.Errorf("%s: PublicHandler returned a handler next to its error", label)
	}
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Errorf("%s: err = %v, want a *ConfigError so the server labels it invalid configuration", label, err)
	}
	return err, strings.Split(err.Error(), "\n")
}

// A bad token, a bad rate limit and a missing content dir are all reported in
// one error: each problem on its own line naming its variable, the three
// joined with errors.Join, and no line echoing the token or the path.
func TestPublicHandlerReportsEveryConfigErrorInOneRun(t *testing.T) {
	const marker = "s3cret marker 0123456789abcdef0123" // long enough that the space is what fails
	missing := filepath.Join(t.TempDir(), "missing-marker-dir")
	for name, cfg := range map[string]platform.Config{
		"bad shorthand token": {APIToken: marker, RateLimit: platform.RateLimit{}, ContentDir: missing},
		"bad token line":      {APITokens: "ci:reader:" + marker, RateLimit: platform.RateLimit{Per: time.Second, Burst: 40}, ContentDir: missing},
		"no token, no dir":    {RateLimit: platform.RateLimit{}},
	} {
		err, lines := configErrors(t, name, cfg)
		for _, v := range []string{platform.EnvAPIToken, platform.EnvRateLimit, platform.EnvContentDir} {
			n := 0
			for _, l := range lines {
				if strings.Contains(l, v) {
					n++
				}
			}
			if n == 0 {
				t.Errorf("%s: err = %q, want a line naming %s", name, err, v)
			}
		}
		if len(lines) < 3 {
			t.Errorf("%s: err = %q, want each of the three problems on its own line", name, err)
		}
		var joined interface{ Unwrap() []error }
		if !errors.As(err, &joined) || len(joined.Unwrap()) < 3 {
			t.Errorf("%s: err = %q, want the problems joined with errors.Join", name, err)
		}
		for _, hidden := range []string{marker, "s3cret", missing, "missing-marker-dir"} {
			if strings.Contains(err.Error(), hidden) {
				t.Errorf("%s: error echoes a value (%q): %v", name, hidden, err)
			}
		}
	}
}

// Only the problems that are there are reported: one bad setting gives one
// line naming that variable and not the others.
func TestPublicHandlerReportsOnlyTheProblemsThereAre(t *testing.T) {
	const marker = "s3cret marker 0123456789abcdef0123"
	good := platform.Config{ContentDir: testContent, APIToken: testToken, RateLimit: platform.DefaultRateLimit()}
	bad := func(mutate func(*platform.Config)) platform.Config { c := good; mutate(&c); return c }
	for want, cfg := range map[string]platform.Config{
		platform.EnvAPIToken:   bad(func(c *platform.Config) { c.APIToken = marker }),
		platform.EnvRateLimit:  bad(func(c *platform.Config) { c.RateLimit = platform.RateLimit{} }),
		platform.EnvContentDir: bad(func(c *platform.Config) { c.ContentDir = filepath.Join(t.TempDir(), "missing") }),
	} {
		err, lines := configErrors(t, want, cfg)
		if len(lines) != 1 || !strings.Contains(err.Error(), want) {
			t.Errorf("only %s bad: err = %q, want one line naming it", want, err)
		}
		for _, other := range []string{platform.EnvAPIToken, platform.EnvRateLimit, platform.EnvContentDir} {
			if other != want && strings.Contains(err.Error(), other) {
				t.Errorf("only %s bad: err = %q also names %s", want, err, other)
			}
		}
	}
}

// A RateLimit of zero requests, the zero value a caller gets by leaving the
// field out, fails closed: PublicHandler returns a *ConfigError naming
// GOLDIE_RATE_LIMIT and no handler, rather than serving without a limit.
func TestZeroValueRateLimitFailsClosed(t *testing.T) {
	for _, l := range []platform.RateLimit{
		{},
		{Requests: 0, Per: time.Second, Burst: 40},
		{Requests: 0, Per: time.Minute, Burst: 1},
		{Requests: 0, Per: time.Second},
	} {
		label := fmt.Sprintf("RateLimit %+v", l)
		err, _ := configErrors(t, label, platform.Config{ContentDir: testContent, APITokens: namedTokenLines(), RateLimit: l})
		if !strings.Contains(err.Error(), platform.EnvRateLimit) {
			t.Errorf("%s: err = %v, want it to name %s", label, err, platform.EnvRateLimit)
		}
	}
	// A caller that leaves RateLimit out of an otherwise complete Config is
	// refused too.
	if _, err := PublicHandler(platform.Config{ContentDir: testContent, APIToken: testToken}, nil); err == nil || !strings.Contains(err.Error(), platform.EnvRateLimit) {
		t.Errorf("Config without a RateLimit: err = %v, want a failure naming %s", err, platform.EnvRateLimit)
	}
}

// GOLDIE_API_TOKEN and GOLDIE_API_TOKENS together are refused by LoadConfig,
// before PublicHandler is reached (#186); with neither set LoadConfig loads,
// and PublicHandler is what refuses to serve without a token.
func TestTokenPairingIsLoadConfigsAndNoTokenIsTheHandlers(t *testing.T) {
	_, err := platform.LoadConfig(func(k string) (string, bool) {
		v, ok := map[string]string{platform.EnvAPIToken: testToken, platform.EnvAPITokens: namedTokenLines(), platform.EnvContentDir: testContent}[k]
		return v, ok
	})
	if err == nil || !strings.Contains(err.Error(), platform.EnvAPIToken+" and "+platform.EnvAPITokens) {
		t.Errorf("LoadConfig with both token variables: err = %v, want it to name both", err)
	}
	cfg := loadedConfig(t, map[string]string{platform.EnvContentDir: testContent})
	if _, err := PublicHandler(cfg, nil); err == nil || !strings.Contains(err.Error(), platform.EnvAPITokens) || !strings.Contains(err.Error(), platform.EnvAPIToken+" ") {
		t.Errorf("PublicHandler with neither token: err = %v, want it to name %s and %s", err, platform.EnvAPITokens, platform.EnvAPIToken)
	}
}

// The settings copy and the test-only constructors are gone (#186): package
// app declares no Options type, no newPublicMux that takes a token string and
// swaps a bad one for an empty set, and no publicRoutes. Tests build the
// router with the one builder PublicHandler uses, from a parsed token set and
// limiter.
func TestAppHasNoOptionsCopyOrTestOnlyConstructors(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	gone := map[string]bool{"Options": true, "newPublicMux": true, "publicRoutes": true}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && gone[d.Name.Name] {
					t.Errorf("%s declares func %s", fset.Position(d.Pos()), d.Name.Name)
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					if ts, ok := s.(*ast.TypeSpec); ok && gone[ts.Name.Name] {
						t.Errorf("%s declares type %s", fset.Position(ts.Pos()), ts.Name.Name)
					}
				}
			}
		}
	}
}
