package platform

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// PublicHandler takes Config (#186). LoadConfig owns every check that needs
// only the environment: GOLDIE_API_TOKEN and GOLDIE_API_TOKENS set together
// is an error there, reported with every other problem in one run, so
// goldie health refuses it too. Neither token set is not LoadConfig's
// business: goldie health loads the configuration and needs no token, so
// that check stays with the server role. GOLDIE_MIN_CLIENT_VERSION is parsed
// once, here, into a typed Semver, as GOLDIE_RATE_LIMIT is into a RateLimit.

const (
	envAPIToken  = "GOLDIE_API_TOKEN"
	envAPITokens = "GOLDIE_API_TOKENS"

	// Long enough to pass a token check, so only the pairing can fail.
	cfgOperatorToken = "config-operator-token-0123456789abcdef"
	cfgReaderToken   = "config-reader-token-0123456789abcdef01"
)

func cfgTokenLines() string { return "ci-reader:reader:" + cfgReaderToken }

// Both token variables set, in any mix of the variable and its _FILE form,
// fail LoadConfig naming both variables and never a token.
func TestLoadConfigRejectsBothTokenVariables(t *testing.T) {
	dir := t.TempDir()
	tokenPath := dir + "/token"
	linesPath := dir + "/tokens"
	if err := os.WriteFile(tokenPath, []byte(cfgOperatorToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linesPath, []byte(cfgTokenLines()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string]map[string]string{
		"both variables":         {envAPIToken: cfgOperatorToken, envAPITokens: cfgTokenLines()},
		"token file, lines":      {envAPIToken + "_FILE": tokenPath, envAPITokens: cfgTokenLines()},
		"token, lines file":      {envAPIToken: cfgOperatorToken, envAPITokens + "_FILE": linesPath},
		"both files":             {envAPIToken + "_FILE": tokenPath, envAPITokens + "_FILE": linesPath},
		"malformed values, both": {envAPIToken: "tokenmarker", envAPITokens: "linemarker"},
	} {
		_, err := loadEnv(env)
		if err == nil {
			t.Errorf("%s: LoadConfig accepted %s and %s together", name, envAPIToken, envAPITokens)
			continue
		}
		if !strings.Contains(err.Error(), envAPIToken+" and "+envAPITokens) {
			t.Errorf("%s: err = %v, want it to name %s and %s", name, err, envAPIToken, envAPITokens)
		}
		for _, secret := range []string{cfgOperatorToken, cfgReaderToken, "tokenmarker", "linemarker", tokenPath, linesPath} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: error echoes a token or a path (%q): %v", name, secret, err)
			}
		}
	}
}

// The pairing error is one of LoadConfig's errors, joined with the others:
// an operator with both tokens and a bad log level sees both in one run.
func TestLoadConfigReportsBothTokenVariablesWithEveryOtherProblem(t *testing.T) {
	_, err := loadEnv(map[string]string{
		envAPIToken:        cfgOperatorToken,
		envAPITokens:       cfgTokenLines(),
		"GOLDIE_LOG_LEVEL": "loud",
		envRateLimit:       "fast",
	})
	if err == nil {
		t.Fatal("LoadConfig accepted both token variables, a bad log level and a bad rate limit")
	}
	for _, want := range []string{envAPIToken + " and " + envAPITokens, "GOLDIE_LOG_LEVEL", envRateLimit} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to report %s in the same run", err, want)
		}
	}
}

// Neither token, or exactly one, loads: goldie health calls LoadConfig and
// must not need a token, and either variable alone is a valid server setup.
// The tokens reach Config as read, for the server role to check.
func TestLoadConfigAcceptsNoTokenOrExactlyOne(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"neither":          {},
		"both empty":       {envAPIToken: "", envAPITokens: ""},
		"token only":       {envAPIToken: cfgOperatorToken},
		"lines only":       {envAPITokens: cfgTokenLines()},
		"token, empty set": {envAPIToken: cfgOperatorToken, envAPITokens: ""},
	} {
		cfg, err := loadEnv(env)
		if err != nil {
			t.Errorf("%s: %v, want no error", name, err)
			continue
		}
		if cfg.APIToken != env[envAPIToken] || cfg.APITokens != env[envAPITokens] {
			t.Errorf("%s: Config carries APIToken set=%t, APITokens set=%t; want them as read", name, cfg.APIToken != "", cfg.APITokens != "")
		}
	}
}

// typedMinClientVersion is cfg.MinClientVersion as the typed Semver
// LoadConfig parsed (#186), or a failure naming what Config holds instead.
func typedMinClientVersion(t *testing.T, cfg Config) Semver {
	t.Helper()
	v, ok := any(cfg.MinClientVersion).(Semver)
	if !ok {
		t.Fatalf("Config.MinClientVersion is %T, want the Semver LoadConfig parsed once", cfg.MinClientVersion)
	}
	return v
}

func mustSemver(t *testing.T, s string) Semver {
	t.Helper()
	v, err := ParseSemver(s)
	if err != nil {
		t.Fatalf("ParseSemver(%q): %v", s, err)
	}
	return v
}

// LoadConfig parses GOLDIE_MIN_CLIENT_VERSION once into
// Config.MinClientVersion, a typed Semver: the value the variable or its
// _FILE form names, build metadata dropped. The startup report still logs
// the setting as written.
func TestMinClientVersionIsParsedOnceIntoATypedValue(t *testing.T) {
	for v, want := range map[string]string{
		"1.4.0":          "1.4.0",
		"0.0.1":          "0.0.1",
		"10.20.30":       "10.20.30",
		"1.4.0-rc.1":     "1.4.0-rc.1",
		"1.4.0+build.7":  "1.4.0",
		"2.0.0-a.1+meta": "2.0.0-a.1",
	} {
		cfg, err := loadEnv(map[string]string{envMinClientVersion: v})
		if err != nil {
			t.Errorf("%s=%q: %v", envMinClientVersion, v, err)
			continue
		}
		got := typedMinClientVersion(t, cfg)
		if !reflect.DeepEqual(got, mustSemver(t, want)) {
			t.Errorf("%s=%q: MinClientVersion = %s, want %s", envMinClientVersion, v, got, want)
		}
		if r := reported(t, cfg, envMinClientVersion); r != v {
			t.Errorf("%s=%q: reported %q, want it as written", envMinClientVersion, v, r)
		}
	}
	path := t.TempDir() + "/min"
	if err := os.WriteFile(path, []byte("1.4.0-rc.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadEnv(map[string]string{envMinClientVersion + "_FILE": path})
	if err != nil {
		t.Fatal(err)
	}
	if got := typedMinClientVersion(t, cfg); !reflect.DeepEqual(got, mustSemver(t, "1.4.0-rc.2")) {
		t.Errorf("_FILE form: MinClientVersion = %s, want 1.4.0-rc.2", got)
	}
}

// Unset or empty, MinClientVersion is the zero Semver, which PublicHandler
// takes as no gate, and the report shows the setting unset.
func TestUnsetMinClientVersionIsTheZeroValue(t *testing.T) {
	for _, env := range []map[string]string{{}, {envMinClientVersion: ""}} {
		cfg, err := loadEnv(env)
		if err != nil {
			t.Fatalf("%v: %v", env, err)
		}
		if got := typedMinClientVersion(t, cfg); !reflect.DeepEqual(got, Semver{}) {
			t.Errorf("%v: MinClientVersion = %s, want the zero Semver (no gate)", env, got)
		}
		if r := reported(t, cfg, envMinClientVersion); r != "" {
			t.Errorf("%v: reported %q, want it empty", env, r)
		}
	}
}

// The GOLDIE_RATE_LIMIT text warns that the burst has no upper bound, so a
// very large burst effectively disables the limit (#198). The committed
// configuration reference carries the same text.
func TestRateLimitSettingSaysBurstHasNoUpperBound(t *testing.T) {
	var meaning string
	for _, s := range Settings {
		if s.Name == envRateLimit {
			meaning = s.Meaning
		}
	}
	if meaning == "" {
		t.Fatalf("%s is not in the Settings registry", envRateLimit)
	}
	lower := strings.ToLower(meaning)
	for _, want := range []string{"no upper bound", "disable"} {
		if !strings.Contains(lower, want) {
			t.Errorf("%s description %q does not state %q", envRateLimit, meaning, want)
		}
	}
	committed, err := os.ReadFile("../../docs/reference/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"no upper bound"} {
		if !strings.Contains(strings.ToLower(string(committed)), want) {
			t.Errorf("docs/reference/configuration.md does not state %q (run: ./run regen)", want)
		}
	}
}
