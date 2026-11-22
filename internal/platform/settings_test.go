package platform

import (
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
	}
	if !cfg.SwaggerUIEnabled {
		t.Error("Swagger UI must be enabled by default")
	}
	got[EnvSwaggerUIEnabled] = "true"
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
		{"GOLDIE_B", "", "a | b <script>alert(1)</script>\nsecond `line`"},
		{"GOLDIE_A", "x|y", "plain"},
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
	added := append([]SettingSpec{{"GOLDIE_NEW", "", "new"}}, Settings...)
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
