package platform

import (
	"os"
	"strings"
	"testing"
)

// Minimum client version (#102). GOLDIE_MIN_CLIENT_VERSION is a semantic
// version (semver 2.0.0: MAJOR.MINOR.PATCH, optionally -prerelease). Clients
// that send X-Goldie-Client: goldie/<version> with an older version are
// refused with 426 and an upgrade hint; requests without the header are let
// through. Unset or empty, there is no gate. LoadConfig validates it (PC-04),
// it has a _FILE form (PC-05), and the startup report logs it as written.
const envMinClientVersion = "GOLDIE_MIN_CLIENT_VERSION"

// GOLDIE_MIN_CLIENT_VERSION is in the settings registry, unset by default (no
// gate), and its description, rendered into the configuration reference,
// names the header it reads, the status an older client gets, and that
// requests without the header are let through.
func TestMinClientVersionSettingIsRegisteredWithNoGateByDefault(t *testing.T) {
	for _, s := range Settings {
		if s.Name != envMinClientVersion {
			continue
		}
		if s.Default != "" {
			t.Errorf("%s default = %q, want unset: no gate unless the operator sets one", envMinClientVersion, s.Default)
		}
		for _, want := range []string{"X-Goldie-Client", "426", "without"} {
			if !strings.Contains(s.Meaning, want) {
				t.Errorf("%s description %q does not state %q", envMinClientVersion, s.Meaning, want)
			}
		}
		return
	}
	t.Fatalf("%s is not in the Settings registry", envMinClientVersion)
}

// The gate is server configuration, not a client setting.
func TestMinClientVersionIsNotAClientSetting(t *testing.T) {
	for _, s := range ClientSettings {
		if s.Name == envMinClientVersion {
			t.Errorf("%s is in ClientSettings; it configures the server", envMinClientVersion)
		}
	}
}

// Unset or empty starts the server with no gate. A valid semantic version,
// with or without a prerelease, from the variable or its _FILE form, starts
// it and is reported as written.
func TestMinClientVersionSettingAcceptsValidValues(t *testing.T) {
	for _, env := range []map[string]string{{}, {envMinClientVersion: ""}} {
		if _, err := loadEnv(env); err != nil {
			t.Errorf("%v: %v, want no gate and no error", env, err)
		}
	}
	for _, v := range []string{
		"0.0.0",
		"0.1.0",
		"1.2.3",
		"10.20.30",
		"1.0.0-rc.1",
		"2.0.0-alpha.beta.1",
		"1.0.0-0.3.7",
		"1.0.0-x-y-z.--",
	} {
		cfg, err := loadEnv(map[string]string{envMinClientVersion: v})
		if err != nil {
			t.Errorf("%s=%q: %v, want it accepted", envMinClientVersion, v, err)
			continue
		}
		if got := reported(t, cfg, envMinClientVersion); got != v {
			t.Errorf("%s=%q: reported %q", envMinClientVersion, v, got)
		}
	}
	path := t.TempDir() + "/min"
	if err := os.WriteFile(path, []byte("1.4.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadEnv(map[string]string{envMinClientVersion + "_FILE": path})
	if err != nil {
		t.Fatal(err)
	}
	if got := reported(t, cfg, envMinClientVersion); got != "1.4.0" {
		t.Errorf("_FILE form: reported %q, want 1.4.0", got)
	}
}

// Anything that is not a semantic version fails startup naming
// GOLDIE_MIN_CLIENT_VERSION, never echoing the value: missing or extra
// parts, leading zeros, empty or invalid prerelease and build parts, ranges
// and operators, the header form, names such as dev or latest, whitespace,
// control bytes, non-ASCII digits and numbers too large to hold.
func TestMinClientVersionSettingRejectsInvalidValues(t *testing.T) {
	for _, v := range []string{
		"marker",
		"latest",
		"dev",
		"v",
		"1",
		"1.2",
		"1.2.3.4",
		"1..3",
		"1.2.",
		".1.2",
		"01.2.3",
		"1.02.3",
		"1.2.03",
		"-1.2.3",
		"+1.2.3",
		"1.2.-3",
		"1.2.3-",
		"1.2.3+",
		"1.2.3-01",
		"1.2.3-rc..1",
		"1.2.3-rc_1",
		"1.2.3-marker!",
		"1.2.x",
		"1.2.*",
		">=1.2.3",
		"^1.2.3",
		"~1.2.3",
		"1.2.3,1.3.0",
		"1.2.3 - 1.3.0",
		"goldie/1.2.3",
		" 1.2.3",
		"1.2.3 ",
		"1.2.3\n",
		"1.2.3\t",
		"1.2.3\x00",
		"1.2.3\u200b",
		"\u0661.2.3", // Arabic-Indic digit one
		"\uff11.2.3", // fullwidth digit one
		"99999999999999999999.0.0",
		"1.99999999999999999999.0",
		"1.2.99999999999999999999",
	} {
		_, err := loadEnv(map[string]string{envMinClientVersion: v})
		if err == nil || !strings.Contains(err.Error(), envMinClientVersion) {
			t.Errorf("%s=%q: err = %v, want a failure naming %s", envMinClientVersion, v, err, envMinClientVersion)
			continue
		}
		if strings.Contains(err.Error(), "marker") {
			t.Errorf("%s=%q: error echoes the value: %v", envMinClientVersion, v, err)
		}
	}
}
