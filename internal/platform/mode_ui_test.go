package platform

import (
	"strings"
	"testing"
)

// #636: GOLDIE_MODE=ui needs GOLDIE_UPSTREAM_ADDR; the Source settings belong
// to the modes that serve content. Every refusal names the variable and never
// its value.
func TestUIModeStartupValidation(t *testing.T) {
	const marker = "s3cr3t-marker"
	upstream := map[string]string{"GOLDIE_UPSTREAM_ADDR": "http://127.0.0.1:9"}
	sourceSettings := map[string]string{
		"GOLDIE_CONTENT_DIR":         "/srv/" + marker,
		"GOLDIE_GIT_URL":             "https://git.example.com/" + marker + ".git",
		"GOLDIE_GIT_REF":             marker,
		"GOLDIE_GIT_TOKEN":           marker,
		"GOLDIE_GIT_SSH_KEY":         marker,
		"GOLDIE_GIT_SSH_KNOWN_HOSTS": marker,
		"GOLDIE_GIT_CA_BUNDLE":       marker,
		"GOLDIE_GIT_ALLOW_FILE_URL":  "true",
		"GOLDIE_SOURCES":             `[{"name":"` + marker + `","type":"dir","dir":"/srv/x"}]`,
		"GOLDIE_POLL_INTERVAL":       "90s",
	}
	merge := func(maps ...map[string]string) map[string]string {
		out := map[string]string{}
		for _, m := range maps {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}

	// ui with an upstream and no Source setting is valid.
	cfg, err := loadEnv(merge(map[string]string{"GOLDIE_MODE": "ui"}, upstream))
	if err != nil {
		t.Fatalf("GOLDIE_MODE=ui with GOLDIE_UPSTREAM_ADDR: %v", err)
	}
	if got := reported(t, cfg, "GOLDIE_MODE"); got != "ui" {
		t.Errorf("startup report GOLDIE_MODE = %q, want ui", got)
	}
	if got := reported(t, cfg, "GOLDIE_UPSTREAM_ADDR"); got != "http://127.0.0.1:9" {
		t.Errorf("startup report GOLDIE_UPSTREAM_ADDR = %q, want the address", got)
	}

	refused := func(name string, env map[string]string, variable string) {
		t.Helper()
		_, err := loadEnv(env)
		if err == nil {
			t.Errorf("%s: want a startup error naming %s", name, variable)
			return
		}
		msg := err.Error()
		if !strings.Contains(msg, variable) {
			t.Errorf("%s: error %q does not name %s", name, msg, variable)
		}
		if strings.Contains(msg, marker) || strings.Contains(msg, "127.0.0.1:9") {
			t.Errorf("%s: error %q contains a value", name, msg)
		}
	}

	// ui needs the upstream.
	refused("ui without an upstream", map[string]string{"GOLDIE_MODE": "ui"}, "GOLDIE_UPSTREAM_ADDR")

	// all, unset and api refuse the upstream.
	for _, mode := range []string{"all", "api", ""} {
		env := merge(upstream)
		if mode != "" {
			env["GOLDIE_MODE"] = mode
		}
		refused("GOLDIE_MODE="+mode+" with an upstream", env, "GOLDIE_UPSTREAM_ADDR")
	}

	// ui refuses each Source setting.
	for name, value := range sourceSettings {
		env := merge(map[string]string{"GOLDIE_MODE": "ui", name: value}, upstream)
		refused("ui with "+name, env, name)
	}

	// The Source settings stay valid where content is served.
	for _, mode := range []string{"all", "api"} {
		env := map[string]string{"GOLDIE_MODE": mode, "GOLDIE_CONTENT_DIR": "/srv/content", "GOLDIE_POLL_INTERVAL": "90s"}
		if _, err := loadEnv(env); err != nil {
			t.Errorf("GOLDIE_MODE=%s with Source settings: %v", mode, err)
		}
	}
}
