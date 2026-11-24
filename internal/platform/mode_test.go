package platform

import (
	"strings"
	"testing"
)

// #635: GOLDIE_MODE is all (default), api, ui (#636) or mcp (#637). Environment only (PC-04).
func TestModeSetting(t *testing.T) {
	load := func(env map[string]string) (Config, error) {
		return LoadConfig(mapLookup(env))
	}
	for value, want := range map[string]string{"": "all", "all": "all", "api": "api"} {
		env := map[string]string{}
		if value != "" {
			env["GOLDIE_MODE"] = value
		}
		cfg, err := load(env)
		if err != nil {
			t.Errorf("GOLDIE_MODE=%q: %v", value, err)
			continue
		}
		if got := reported(t, cfg, "GOLDIE_MODE"); got != want {
			t.Errorf("GOLDIE_MODE=%q: startup report says %q, want %q", value, got, want)
		}
	}
	for _, bad := range []string{"MCP", "UI", "API", "everything"} {
		_, err := load(map[string]string{"GOLDIE_MODE": bad})
		if err == nil {
			t.Errorf("GOLDIE_MODE=%q: want a startup error", bad)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "GOLDIE_MODE") || !strings.Contains(msg, "all") || !strings.Contains(msg, "api") {
			t.Errorf("GOLDIE_MODE=%q: err = %q, want it to name the variable and list all and api", bad, msg)
		}
	}
}

func TestModeIsInTheConfigurationReference(t *testing.T) {
	if !strings.Contains(string(ConfigReference(Settings)), "GOLDIE_MODE") {
		t.Error("the configuration reference does not list GOLDIE_MODE")
	}
}

// The goldie.mode resource attribute carries the real mode on every signal.
func TestTelemetryResourceCarriesTheConfiguredMode(t *testing.T) {
	for mode, want := range map[string]string{"api": "api", "": "all"} {
		env := map[string]string{}
		if mode != "" {
			env["GOLDIE_MODE"] = mode
		}
		for signal, res := range newResourceCollector(t).exportOnAllSignals(t, env) {
			if res["goldie.mode"] != want {
				t.Errorf("GOLDIE_MODE=%q, %s: goldie.mode = %q, want %q", mode, signal, res["goldie.mode"], want)
			}
		}
	}
}
