package platform

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Per-token rate limiting (#100). GOLDIE_RATE_LIMIT is "<n>/s" or "<n>/m",
// n requests per second or per minute for each token, optionally followed by
// ",burst=<b>", the most requests a token may send at once (b defaults to n).
// n and b are positive ASCII decimal integers without sign or spaces, and
// the unit is lower case; nothing else is accepted. Unset or empty, the default is
// 20/s,burst=40. LoadConfig validates it and the startup report logs the
// effective value as written.
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
