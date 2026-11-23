package platform

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestSettingsRegistryMatchesLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{
		EnvAddr: cfg.Addr, EnvAdminAddr: cfg.AdminAddr, EnvLogLevel: cfg.LogLevel,
		EnvDrainSeconds: "25",
		EnvContentDir:   cfg.ContentDir, EnvAPIToken: cfg.APIToken,
		"GOLDIE_API_TOKENS": cfg.APITokens,
		envRateLimit:        reported(t, cfg, envRateLimit),
	}
	if !cfg.SwaggerUIEnabled {
		t.Error("Swagger UI must be enabled by default")
	}
	got[EnvSwaggerUIEnabled] = "true"
	got[EnvTelemetryClientInfo] = "false"                            // read by SetupTelemetry, not part of Config
	got[EnvMinClientVersion] = reported(t, cfg, EnvMinClientVersion) // Config holds the parsed Semver (#186)
	// The git Source settings and the poll interval (#258).
	_, rec, _ := reportKeys(t, cfg)
	got[envGitURL] = reported(t, cfg, envGitURL)
	got[envGitRef] = reported(t, cfg, envGitRef)
	got[envGitAllowFileURL] = fmt.Sprint(rec[envGitAllowFileURL])
	got[envPollInterval] = reported(t, cfg, envPollInterval)
	for _, name := range []string{envGitToken, envGitSSHKey, envGitSSHKnownHosts} {
		got[name] = fmt.Sprint(effective(t, cfg, name))
	}
	got[envGitCABundle] = "" // reported only as set or unset; it has no default
	if int(cfg.DrainTimeout.Seconds()) != 25 {
		t.Errorf("drain default = %v", cfg.DrainTimeout)
	}
	for _, s := range Settings {
		if !strings.HasPrefix(s.Name, "GOLDIE_") {
			t.Errorf("%s lacks the GOLDIE_ prefix", s.Name)
		}
		if want, ok := got[s.Name]; !ok || want != s.Default {
			t.Errorf("%s: registry default %q, LoadConfig %q", s.Name, s.Default, want)
		}
	}
}

func TestConfigReferenceEscapesAndIsDeterministic(t *testing.T) {
	in := []SettingSpec{
		{Name: "GOLDIE_B", Meaning: "a | b <script>alert(1)</script>\nsecond `line`"},
		{Name: "GOLDIE_A", Default: "x|y", Meaning: "plain"},
	}
	a := string(ConfigReference(in))
	if a != string(ConfigReference(in)) {
		t.Fatal("two renders differ")
	}
	if strings.Index(a, "GOLDIE_A") > strings.Index(a, "GOLDIE_B") {
		t.Error("rows are not sorted by name")
	}
	for _, want := range []string{
		"| `GOLDIE_B` | (none) | a \\| b &lt;script&gt;alert(1)&lt;/script&gt; second 'line' |",
		"| `GOLDIE_A` | `x\\|y` | plain |",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("reference lacks %q:\n%s", want, a)
		}
	}
	if strings.Contains(a, "<script>") {
		t.Error("raw HTML in output")
	}
}

func TestCommittedConfigReferenceIsFresh(t *testing.T) {
	committed, err := os.ReadFile("../../docs/reference/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != string(ConfigReference(Settings)) {
		t.Error("docs/reference/configuration.md is out of date (run: ./run regen)")
	}
	added := append([]SettingSpec{{Name: "GOLDIE_NEW", Meaning: "new"}}, Settings...)
	if string(committed) == string(ConfigReference(added)) {
		t.Error("drift not detected when a setting is added")
	}
}

func TestSwaggerUIEnabledSetting(t *testing.T) {
	load := func(env map[string]string) (Config, error) {
		return LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	}
	for value, want := range map[string]bool{"true": true, "false": false, "TRUE": true, "False": false, "": true} {
		cfg, err := load(map[string]string{EnvSwaggerUIEnabled: value})
		if err != nil || cfg.SwaggerUIEnabled != want {
			t.Errorf("%s=%q: enabled=%v err=%v, want %v", EnvSwaggerUIEnabled, value, cfg.SwaggerUIEnabled, err, want)
		}
	}
	_, err := load(map[string]string{EnvSwaggerUIEnabled: "maybe"})
	if err == nil || !strings.Contains(err.Error(), EnvSwaggerUIEnabled) || strings.Contains(err.Error(), "maybe") {
		t.Errorf("invalid value: err = %v, want it to name the variable and not echo the value", err)
	}
}

// Redaction in the startup report is covered for every Secret row by
// TestStartupReportRedactsEverySecretSetting (#185).
func TestAPITokenIsReadFromFile(t *testing.T) {
	const token = "file-token-0123456789abcdef0123456789"
	path := t.TempDir() + "/token"
	if err := os.WriteFile(path, []byte(token+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{EnvAPIToken + "_FILE": path, EnvContentDir: "/srv/content"}
	cfg, err := LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIToken != token || cfg.ContentDir != "/srv/content" {
		t.Fatalf("token %q, content dir %q: want the file's token without its newline", cfg.APIToken, cfg.ContentDir)
	}
}

// The client commands' variables are documented in the configuration
// reference, in their own table.
func TestConfigReferenceListsClientSettings(t *testing.T) {
	ref := string(ConfigReference(nil))
	// The intro's _FILE rule is limited to the server table, and the client
	// section states its own exception, so the two cannot contradict.
	if strings.Contains(ref, "Every variable also accepts") {
		t.Errorf("the intro claims a _FILE form for every variable, which the client and OpenTelemetry sections contradict:\n%s", ref)
	}
	for _, want := range []string{"Every variable in the table below also accepts", "Only " + EnvToken + " has a _FILE form", "## Client commands", "| `" + EnvAddr + "` | (none) | Server URL", "| `" + EnvToken + "` | (none) | Secret: set it with GOLDIE_TOKEN_FILE."} {
		if !strings.Contains(ref, want) {
			t.Errorf("reference lacks %q:\n%s", want, ref)
		}
	}
}

// GOLDIE_API_TOKENS is read from its _FILE form with every line kept and only
// the trailing newline dropped. Its redaction is covered by
// TestStartupReportRedactsEverySecretSetting (#185).
func TestAPITokensAreReadFromFile(t *testing.T) {
	const lines = "ci-reader:reader:reader-token-0123456789abcdef0123456789\n" +
		"ci-hashed:operator:sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	path := t.TempDir() + "/tokens"
	if err := os.WriteFile(path, []byte(lines+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"GOLDIE_API_TOKENS_FILE": path}
	cfg, err := LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APITokens != lines {
		t.Fatalf("APITokens = %q, want the file's lines without the trailing newline", cfg.APITokens)
	}
}

// The OpenTelemetry section's intro says what PC-12 says: the server's traces
// of the public listener, its logs and its metrics are exported, and the
// client roles' traces are described apart from it (#327).
func TestConfigReferenceOpenTelemetryIntroMatchesPC12(t *testing.T) {
	ref := string(ConfigReference(Settings))
	_, section, ok := strings.Cut(ref, "## OpenTelemetry")
	if !ok {
		t.Fatal("the reference has no OpenTelemetry section")
	}
	const want = "The server's traces of the public listener, logs and metrics are exported over OTLP (http/protobuf)"
	if !strings.HasPrefix(strings.TrimSpace(section), want) {
		t.Errorf("the OpenTelemetry intro does not start with %q:\n%.300s", want, section)
	}
	if strings.Contains(section, "Traces of the public listener and logs are exported") {
		t.Error("the old intro, which leaves out metrics, is still there")
	}
}
