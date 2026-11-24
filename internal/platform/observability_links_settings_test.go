package platform

import (
	"strings"
	"testing"
)

// Acceptance tests for #807: GOLDIE_GRAFANA_URL and GOLDIE_TRACE_URL_TEMPLATE,
// the two links the activity view opens ("Trends in Grafana", "Open trace").
// Both are optional, listed in the settings registry, and refuse at startup a
// value that is not http or https, that carries userinfo, or (the template)
// that lacks {trace_id}. The error names the variable, never the value (PC-04).
const (
	envGrafanaURL        = "GOLDIE_GRAFANA_URL"
	envTraceURLTemplate  = "GOLDIE_TRACE_URL_TEMPLATE"
	validGrafanaURL      = "https://grafana.example.com/d/goldie?orgId=1"
	validTraceURLPattern = "https://tempo.example.com/trace/{trace_id}"
)

// Both are server settings in the registry, optional (no default), not
// secrets, documented in the committed configuration reference.
func TestObservabilityLinkSettingsAreRegisteredOptionalAndDocumented(t *testing.T) {
	for _, name := range []string{envGrafanaURL, envTraceURLTemplate} {
		row := settingRow(t, name)
		if row.Default != "" {
			t.Errorf("%s default = %q, want unset: the link is optional", name, row.Default)
		}
		if row.Secret {
			t.Errorf("%s is marked Secret; it is a link, not a credential", name)
		}
		if row.Meaning == "" {
			t.Errorf("%s has no description", name)
		}
		for _, s := range ClientSettings {
			if s.Name == name {
				t.Errorf("%s is in ClientSettings; it configures the server", name)
			}
		}
		if !strings.Contains(committedReference(t), "\n| `"+name+"` | ") {
			t.Errorf("committed docs/reference/configuration.md has no row for %s (run: ./run regen)", name)
		}
	}
	if m := settingRow(t, envTraceURLTemplate).Meaning; !strings.Contains(m, "{trace_id}") {
		t.Errorf("%s description %q does not name the {trace_id} placeholder", envTraceURLTemplate, m)
	}
}

// Unset or empty, startup succeeds and nothing is reported.
func TestObservabilityLinkSettingsAreOptional(t *testing.T) {
	for _, env := range []map[string]string{{}, {envGrafanaURL: "", envTraceURLTemplate: ""}} {
		cfg, err := loadEnv(env)
		if err != nil {
			t.Fatalf("%v: %v, want startup to succeed", env, err)
		}
		for _, name := range []string{envGrafanaURL, envTraceURLTemplate} {
			if got := reported(t, cfg, name); got != "" {
				t.Errorf("%v: %s reported as %q, want empty", env, name, got)
			}
		}
	}
}

// An http or https URL is accepted and reported as written.
func TestObservabilityLinkSettingsAcceptHTTPAndHTTPS(t *testing.T) {
	for _, g := range []string{validGrafanaURL, "http://grafana.internal:3000/"} {
		cfg, err := loadEnv(map[string]string{envGrafanaURL: g, envTraceURLTemplate: validTraceURLPattern})
		if err != nil {
			t.Errorf("%s=%q: %v, want it accepted", envGrafanaURL, g, err)
			continue
		}
		if got := reported(t, cfg, envGrafanaURL); got != g {
			t.Errorf("%s=%q: reported %q", envGrafanaURL, g, got)
		}
		if got := reported(t, cfg, envTraceURLTemplate); got != validTraceURLPattern {
			t.Errorf("%s: reported %q", envTraceURLTemplate, got)
		}
	}
	for _, v := range []string{validTraceURLPattern, "http://jaeger.internal:16686/trace/{trace_id}?uiFind=x"} {
		if _, err := loadEnv(map[string]string{envTraceURLTemplate: v}); err != nil {
			t.Errorf("%s=%q: %v, want it accepted", envTraceURLTemplate, v, err)
		}
	}
}

// A value that is not an http(s) URL with a host, or that carries userinfo,
// fails startup naming the variable and never echoing the value.
func TestObservabilityLinkSettingsRejectBadURLsWithoutEchoing(t *testing.T) {
	bad := []string{
		"ftp://marker.example.com/x",
		"javascript:alert(1)//marker",
		"data:text/html,marker",
		"file:///etc/marker",
		"//marker.example.com/x",
		"marker.example.com/x",
		"marker",
		"https://",
		"https:///marker",
		"https://user@marker.example.com/x",
		"https://user:pw@marker.example.com/x",
		"https://:pw@marker.example.com/x",
		"http://marker.example.com\\@evil.example.com/",
		"https://marker.example.com/\nx",
	}
	for _, v := range bad {
		for _, c := range []struct{ name, value string }{
			{envGrafanaURL, v},
			{envTraceURLTemplate, v + "{trace_id}"},
		} {
			_, err := loadEnv(map[string]string{c.name: c.value})
			if err == nil || !strings.Contains(err.Error(), c.name) {
				t.Errorf("%s=%q: err = %v, want a failure naming %s", c.name, c.value, err, c.name)
				continue
			}
			if strings.Contains(err.Error(), "marker") || strings.Contains(err.Error(), "pw@") {
				t.Errorf("%s=%q: error echoes the value: %v", c.name, c.value, err)
			}
		}
	}
}

// The template must contain {trace_id}; without it the link would open the
// same page for every trace.
func TestTraceURLTemplateWithoutPlaceholderFailsStartup(t *testing.T) {
	for _, v := range []string{
		"https://marker.example.com/trace",
		"https://marker.example.com/trace/trace_id",
		"https://marker.example.com/trace/{traceid}",
		"https://marker.example.com/trace/{TRACE_ID}",
	} {
		_, err := loadEnv(map[string]string{envTraceURLTemplate: v})
		if err == nil || !strings.Contains(err.Error(), envTraceURLTemplate) {
			t.Errorf("%s=%q: err = %v, want a failure naming %s", envTraceURLTemplate, v, err, envTraceURLTemplate)
			continue
		}
		if strings.Contains(err.Error(), "marker") {
			t.Errorf("%s=%q: error echoes the value: %v", envTraceURLTemplate, v, err)
		}
	}
	// The Grafana URL has no placeholder to require.
	if _, err := loadEnv(map[string]string{envGrafanaURL: "https://grafana.example.com/"}); err != nil {
		t.Errorf("%s without a placeholder: %v, want it accepted", envGrafanaURL, err)
	}
}
