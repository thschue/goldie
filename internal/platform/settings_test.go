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
