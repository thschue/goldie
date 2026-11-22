package platform

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Per-token rate limiting (#100). GOLDIE_RATE_LIMIT is "<n>/s" or "<n>/m",
// n requests per second or per minute for each token, optionally followed by
// ",burst=<b>", the most requests a token may send at once (b defaults to n).
// n and b are positive ASCII decimal integers without sign or spaces, and
// the unit is lower case; nothing else is accepted, and neither is a rate
// above 1e9 per second (#178). Unset or empty, the default is 20/s,burst=40.
// LoadConfig parses it once into Config.RateLimit, a typed RateLimit with the
// default applied, and the startup report logs the effective value as
// written.
const envRateLimit = "GOLDIE_RATE_LIMIT"

// reported is the value the startup report logs for name.
func reported(t *testing.T, cfg Config, name string) string {
	t.Helper()
	var buf syncBuffer
	cfg.Report(NewLogger(&buf, 0, "server"))
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &line); err != nil {
		t.Fatalf("startup report is not one JSON line: %v\n%s", err, buf.String())
	}
	v, ok := line[name].(string)
	if !ok {
		t.Fatalf("startup report has no string %s: %s", name, buf.String())
	}
	return v
}

func loadEnv(env map[string]string) (Config, error) {
	return LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
}

// GOLDIE_RATE_LIMIT is in the settings registry with a generous default, and
// its description, rendered into the configuration reference, gives the
// syntax, that the limit is per token and per process, and what a rejected
// request gets.
func TestRateLimitSettingIsRegisteredWithAGenerousDefault(t *testing.T) {
	for _, s := range Settings {
		if s.Name != envRateLimit {
			continue
		}
		if s.Default != "20/s,burst=40" {
			t.Errorf("%s default = %q, want 20/s,burst=40", envRateLimit, s.Default)
		}
		for _, want := range []string{"per token", "per process", "/s", "/m", ",burst=", "429", "Retry-After"} {
			if !strings.Contains(s.Meaning, want) {
				t.Errorf("%s description %q does not state %q", envRateLimit, s.Meaning, want)
			}
		}
		return
	}
	t.Fatalf("%s is not in the Settings registry", envRateLimit)
}

// Unset or empty, the default is in effect; a valid value, from the variable
// or its _FILE form, is reported as written.
func TestRateLimitSettingAcceptsValidValues(t *testing.T) {
	for _, env := range []map[string]string{{}, {envRateLimit: ""}} {
		cfg, err := loadEnv(env)
		if err != nil {
			t.Fatalf("%v: %v", env, err)
		}
		if got := reported(t, cfg, envRateLimit); got != "20/s,burst=40" {
			t.Errorf("%v: reported %q, want the default 20/s,burst=40", env, got)
		}
	}
	for _, v := range []string{
		"20/s",
		"20/s,burst=40",
		"1/s",
		"1/m",
		"600/m,burst=10",
		"1/s,burst=1",
		"5/s,burst=1",
		"1000/s,burst=2000",
	} {
		cfg, err := loadEnv(map[string]string{envRateLimit: v})
		if err != nil {
			t.Errorf("%s=%q: %v, want it accepted", envRateLimit, v, err)
			continue
		}
		if got := reported(t, cfg, envRateLimit); got != v {
			t.Errorf("%s=%q: reported %q", envRateLimit, v, got)
		}
	}
	path := t.TempDir() + "/rate"
	if err := os.WriteFile(path, []byte("3/m,burst=6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadEnv(map[string]string{envRateLimit + "_FILE": path})
	if err != nil {
		t.Fatal(err)
	}
	if got := reported(t, cfg, envRateLimit); got != "3/m,burst=6" {
		t.Errorf("_FILE form: reported %q, want 3/m,burst=6", got)
	}
}

// Anything outside the syntax fails startup naming GOLDIE_RATE_LIMIT: no
// unit or another unit, zero, negative, fractional or signed numbers, a burst
// that is zero, empty or not a number, unknown or repeated options, stray
// separators and whitespace, control bytes and numbers too large to hold.
func TestRateLimitSettingRejectsInvalidValues(t *testing.T) {
	for _, v := range []string{
		"fast",
		"20",
		"20/",
		"/s",
		"s",
		"0/s",
		"0/m",
		"-1/s",
		"+5/s",
		"1.5/s",
		"1e3/s",
		"0x10/s",
		"20/h",
		"20/sec",
		"20/S",
		"20 /s",
		" 20/s",
		"20/s ",
		"20/s\n",
		"20/s\x00",
		"20/s\t,burst=40",
		"20/s,",
		"20/s,burst",
		"20/s,burst=",
		"20/s,burst=0",
		"20/s,burst=-1",
		"20/s,burst=x",
		"20/s,burst=1.5",
		"20/s,bursts=40",
		"20/s,burst=40,burst=50",
		"20/s;burst=40",
		"20/s,burst=40,",
		"burst=40",
		"99999999999999999999/s",
		"20/s,burst=99999999999999999999",
		"20/s/m",
		"\u0662\u0660/s", // Arabic-Indic digits
	} {
		_, err := loadEnv(map[string]string{envRateLimit: v})
		if err == nil || !strings.Contains(err.Error(), envRateLimit) {
			t.Errorf("%s=%q: err = %v, want a failure naming %s", envRateLimit, v, err, envRateLimit)
		}
	}
}

// typedRateLimit is cfg.RateLimit as the typed value LoadConfig parsed (#178),
// or a failure naming what Config holds instead.
func typedRateLimit(t *testing.T, cfg Config) RateLimit {
	t.Helper()
	l, ok := any(cfg.RateLimit).(RateLimit)
	if !ok {
		t.Fatalf("Config.RateLimit is %T, want the RateLimit LoadConfig parsed once", cfg.RateLimit)
	}
	return l
}

// LoadConfig parses GOLDIE_RATE_LIMIT once into Config.RateLimit, a typed
// RateLimit, with the default applied there: unset or empty is 20/s,burst=40,
// a missing burst is the rate, and the _FILE form parses the same.
func TestRateLimitIsParsedOnceIntoATypedValueWithTheDefault(t *testing.T) {
	def := RateLimit{Requests: 20, Per: time.Second, Burst: 40}
	for _, env := range []map[string]string{{}, {envRateLimit: ""}} {
		cfg, err := loadEnv(env)
		if err != nil {
			t.Fatalf("%v: %v", env, err)
		}
		if got := typedRateLimit(t, cfg); got != def {
			t.Errorf("%v: RateLimit = %+v, want the default %+v", env, got, def)
		}
	}
	for v, want := range map[string]RateLimit{
		"20/s":           {Requests: 20, Per: time.Second, Burst: 20},
		"1/m":            {Requests: 1, Per: time.Minute, Burst: 1},
		"600/m,burst=10": {Requests: 600, Per: time.Minute, Burst: 10},
		"5/s,burst=1":    {Requests: 5, Per: time.Second, Burst: 1},
	} {
		cfg, err := loadEnv(map[string]string{envRateLimit: v})
		if err != nil {
			t.Errorf("%s=%q: %v", envRateLimit, v, err)
			continue
		}
		if got := typedRateLimit(t, cfg); got != want {
			t.Errorf("%s=%q: RateLimit = %+v, want %+v", envRateLimit, v, got, want)
		}
	}
	path := t.TempDir() + "/rate"
	if err := os.WriteFile(path, []byte("3/m,burst=6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadEnv(map[string]string{envRateLimit + "_FILE": path})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := typedRateLimit(t, cfg), (RateLimit{Requests: 3, Per: time.Minute, Burst: 6}); got != want {
		t.Errorf("_FILE form: RateLimit = %+v, want %+v", got, want)
	}
}

// Above 1e9 requests per second the interval between requests rounds to 0 ns
// and the limit would silently switch off, so such a value stops startup
// naming GOLDIE_RATE_LIMIT, never echoing the value. 1e9/s and its per-minute
// equal 6e10/m are the highest accepted rates; the burst does not move the
// bound.
func TestRateLimitAboveOneBillionPerSecondIsRejected(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
		want  RateLimit
	}{
		{"999999999/s", true, RateLimit{Requests: 999_999_999, Per: time.Second, Burst: 999_999_999}},
		{"1000000000/s", true, RateLimit{Requests: 1_000_000_000, Per: time.Second, Burst: 1_000_000_000}},
		{"1000000000/s,burst=1", true, RateLimit{Requests: 1_000_000_000, Per: time.Second, Burst: 1}},
		{"60000000000/m", true, RateLimit{Requests: 60_000_000_000, Per: time.Minute, Burst: 60_000_000_000}},
		{"60000000000/m,burst=40", true, RateLimit{Requests: 60_000_000_000, Per: time.Minute, Burst: 40}},
		{"1000000001/s", false, RateLimit{}},
		{"1000000001/s,burst=1", false, RateLimit{}},
		{"2000000000/s", false, RateLimit{}},
		{"60000000001/m", false, RateLimit{}},
		{"60000000001/m,burst=1", false, RateLimit{}},
		{"9223372036854775807/s", false, RateLimit{}},
		{"9223372036854775807/m", false, RateLimit{}},
		{"00000001000000001/s", false, RateLimit{}},
	} {
		cfg, err := loadEnv(map[string]string{envRateLimit: tc.value})
		if !tc.ok {
			if err == nil || !strings.Contains(err.Error(), envRateLimit) {
				t.Errorf("%s=%q: err = %v, want a failure naming %s", envRateLimit, tc.value, err, envRateLimit)
			} else if strings.Contains(err.Error(), tc.value) {
				t.Errorf("%s=%q: error echoes the value: %v", envRateLimit, tc.value, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s=%q: %v, want it accepted", envRateLimit, tc.value, err)
			continue
		}
		if got := typedRateLimit(t, cfg); got != tc.want {
			t.Errorf("%s=%q: RateLimit = %+v, want %+v", envRateLimit, tc.value, got, tc.want)
		}
	}
	// The _FILE form is bound the same way.
	path := t.TempDir() + "/rate"
	if err := os.WriteFile(path, []byte("1000000001/s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadEnv(map[string]string{envRateLimit + "_FILE": path}); err == nil || !strings.Contains(err.Error(), envRateLimit) {
		t.Errorf("_FILE form over 1e9/s: err = %v, want a failure naming %s", err, envRateLimit)
	}
}

// The setting text says what counts: an MCP tool call counts twice, once for
// the /mcp request and once for the API request it makes, and every client
// using the GOLDIE_API_TOKEN shorthand shares one bucket, api-token. The
// committed configuration reference carries the same text.
func TestRateLimitSettingSaysWhatCounts(t *testing.T) {
	var meaning string
	for _, s := range Settings {
		if s.Name == envRateLimit {
			meaning = s.Meaning
		}
	}
	if meaning == "" {
		t.Fatalf("%s is not in the Settings registry", envRateLimit)
	}
	for _, want := range []string{"twice", "/mcp", "API request", "GOLDIE_API_TOKEN", "share", "api-token"} {
		if !strings.Contains(meaning, want) {
			t.Errorf("%s description %q does not state %q", envRateLimit, meaning, want)
		}
	}
	committed, err := os.ReadFile("../../docs/reference/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"twice", "api-token"} {
		if !strings.Contains(string(committed), want) {
			t.Errorf("docs/reference/configuration.md does not state %q (run: ./run regen)", want)
		}
	}
}
