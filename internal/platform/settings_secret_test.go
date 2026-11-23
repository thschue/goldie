package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// #185: whether a setting is secret is a flag on its registry row
// (SettingSpec.Secret), not prose in its Meaning. The startup report takes its
// keys and order from the Settings registry and redacts every Secret row; the
// OpenTelemetry variables with a _FILE form are the TelemetrySettings rows
// marked Secret; and the configuration reference prints the secret note from
// the flag (PC-05, PC-06).

// secretOf reads SettingSpec.Secret. It goes through reflection so this file
// compiles before the field exists and each test fails on its own.
func secretOf(t *testing.T, s SettingSpec) bool {
	t.Helper()
	f := reflect.ValueOf(s).FieldByName("Secret")
	if !f.IsValid() || f.Kind() != reflect.Bool {
		t.Fatalf("SettingSpec has no Secret bool field (#185)")
	}
	return f.Bool()
}

// withSecret returns s with SettingSpec.Secret set to v.
func withSecret(t *testing.T, s SettingSpec, v bool) SettingSpec {
	t.Helper()
	secretOf(t, s)
	reflect.ValueOf(&s).Elem().FieldByName("Secret").SetBool(v)
	return s
}

// secretNames lists the names of the rows marked Secret, in registry order.
func secretNames(t *testing.T, rows []SettingSpec) []string {
	t.Helper()
	var names []string
	for _, s := range rows {
		if secretOf(t, s) {
			names = append(names, s.Name)
		}
	}
	return names
}

// The registry marks exactly the secret variables of the record: the API
// tokens of the server, the git Source's token, SSH key and known hosts
// (#258; its CA bundle is reported as set or unset, not Secret), the client's
// token, and the OTLP headers (ADR-0017). A plain setting marked Secret would
// vanish from the startup report (PC-06). Registry order is not part of this
// check; the golden report pins it.
func TestRegistryMarksExactlyTheSecretSettings(t *testing.T) {
	server := secretNames(t, Settings)
	sort.Strings(server)
	want := []string{EnvAPIToken, EnvAPITokens, "GOLDIE_SESSION_KEY", envGitSSHKey, envGitSSHKnownHosts, envGitToken}
	sort.Strings(want)
	if !reflect.DeepEqual(server, want) {
		t.Errorf("Settings rows marked Secret = %v, want %v", server, want)
	}
	if got, want := secretNames(t, ClientSettings), []string{EnvToken}; !reflect.DeepEqual(got, want) {
		t.Errorf("ClientSettings rows marked Secret = %v, want %v", got, want)
	}
	headers := map[string]bool{
		"OTEL_EXPORTER_OTLP_HEADERS":         true,
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS":  true,
		"OTEL_EXPORTER_OTLP_LOGS_HEADERS":    true,
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS": true,
	}
	got := secretNames(t, TelemetrySettings)
	if !contains(got, "OTEL_EXPORTER_OTLP_HEADERS") {
		t.Errorf("TelemetrySettings rows marked Secret = %v, want OTEL_EXPORTER_OTLP_HEADERS among them", got)
	}
	for _, name := range got {
		if !headers[name] {
			t.Errorf("TelemetrySettings marks %s Secret; only the OTLP headers variables are secret", name)
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// reportKeys returns the GOLDIE_ keys of the one-line startup report in the
// order they were written, and the decoded record.
func reportKeys(t *testing.T, cfg Config) ([]string, map[string]any, string) {
	t.Helper()
	var buf syncBuffer
	cfg.Report(NewLogger(&buf, 0, "server"))
	out := buf.String()
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("startup report is not one line:\n%s", out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("startup report is not a JSON object: %v\n%s", err, out)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("startup report: %v\n%s", err, out)
		}
		if k, _ := tok.(string); strings.HasPrefix(k, "GOLDIE_") {
			keys = append(keys, k)
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("startup report: %v\n%s", err, out)
		}
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("startup report is not JSON: %v\n%s", err, out)
	}
	return keys, rec, out
}

// The startup report lists every Settings row, in registry order, and nothing
// else: a setting added to the registry is reported without a hand-written
// call. Each plain row shows its effective value. The dir and git Sources
// exclude each other (#258), so the samples are loaded once per Source, the
// other left unset; GOLDIE_GIT_CA_BUNDLE is reported only as set.
func TestStartupReportTakesKeysAndOrderFromTheRegistry(t *testing.T) {
	// A valid, non-default value for every plain setting. A row added to the
	// registry without a sample here fails, so the test stays exhaustive.
	samples := map[string]string{
		EnvAddr:                ":9000",
		EnvAdminAddr:           ":9001",
		EnvLogLevel:            "debug",
		EnvDrainSeconds:        "7",
		EnvMode:                "api",
		EnvSwaggerUIEnabled:    "false",
		EnvMCPName:             "acme-golden",
		EnvContentDir:          "/srv/content-sample",
		EnvRateLimit:           "5/m",
		EnvMinClientVersion:    "1.4.0",
		EnvTelemetryClientInfo: "true",
		envGitURL:              gitSampleURL,
		envGitRef:              "release/v2",
		envGitCABundle:         gitSampleCABundle,
		envGitAllowFileURL:     "true",
		envPollInterval:        "90s",

		// The federated activity settings (#398).
		envActivityPeers:         "goldie-headless.goldie.svc",
		envActivityReorderWindow: "750ms",
	}
	for source, other := range map[string]string{EnvContentDir: envGitURL, envGitURL: EnvContentDir} {
		t.Run(source, func(t *testing.T) {
			env := map[string]string{}
			var want []string
			for _, s := range Settings {
				want = append(want, s.Name)
				// GOLDIE_SOURCES conflicts with both single-Source variables
				// this test loads; it reports as unset here and is covered by
				// the Sources tests.
				if secretOf(t, s) || s.Name == other || s.Name == EnvSources {
					continue
				}
				v, ok := samples[s.Name]
				if !ok {
					t.Fatalf("no sample value for %s: add one to this test", s.Name)
				}
				if v == s.Default {
					t.Fatalf("sample for %s equals its default; pick another", s.Name)
				}
				env[s.Name] = v
			}
			cfg, err := loadEnv(env)
			if err != nil {
				t.Fatal(err)
			}
			keys, rec, out := reportKeys(t, cfg)
			if !reflect.DeepEqual(keys, want) {
				t.Errorf("startup report keys = %v\nwant the Settings registry in order: %v\n%s", keys, want, out)
			}
			for _, s := range Settings {
				switch {
				case secretOf(t, s):
					if rec[s.Name] != "(unset)" {
						t.Errorf("%s is unset: report shows %v, want (unset)", s.Name, rec[s.Name])
					}
				case s.Name == EnvSources:
					if got := fmt.Sprint(rec[s.Name]); got != "" {
						t.Errorf("%s is unset: report shows %q, want empty", s.Name, got)
					}
				case s.Name == envGitCABundle:
					if got := fmt.Sprint(rec[s.Name]); got == "" || got == "(unset)" || strings.Contains(got, gitSampleCABundleTag) {
						t.Errorf("%s is set: report shows %q, want it reported only as set", s.Name, got)
					}
				default:
					if got := fmt.Sprint(rec[s.Name]); got != env[s.Name] {
						t.Errorf("%s=%q: report shows %q, want the effective value", s.Name, env[s.Name], got)
					}
				}
			}
		})
	}
}

// The core of every canary; no part of it may reach the report.
const canaryCore = "7f3a9c1e5b2d4086a1c3e5f7b9d0e2f4"

// reportLeaks sets the Settings row name to a canary, in its plain and its
// _FILE form and with a log-injection attempt, loads the configuration, and
// returns every way the startup report fails to redact it.
func reportLeaks(t *testing.T, name string) []string {
	t.Helper()
	canary := "canary-" + strings.ToLower(name) + "-" + canaryCore
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(canary+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]map[string]string{
		"plain":     {name: canary},
		"file":      {name + "_FILE": path},
		"injection": {name: canary + "\n\"}{\"level\":\"ERROR\",\"" + name + "\":\"" + canaryCore + "\"}"},
	}
	var leaks []string
	for form, env := range cases {
		cfg, err := loadEnv(env)
		if err != nil {
			if strings.Contains(err.Error(), canaryCore) {
				leaks = append(leaks, form+": LoadConfig error echoes the value: "+err.Error())
			}
			leaks = append(leaks, form+": LoadConfig: "+err.Error())
			continue
		}
		var buf syncBuffer
		cfg.Report(NewLogger(&buf, 0, "server"))
		out := buf.String()
		for _, part := range []string{canary, canaryCore, canaryCore[:12], canaryCore[len(canaryCore)-12:]} {
			if strings.Contains(out, part) {
				leaks = append(leaks, fmt.Sprintf("%s: report contains %q:\n%s", form, part, out))
				break
			}
		}
		if strings.Count(strings.TrimSpace(out), "\n") != 0 {
			leaks = append(leaks, form+": report is not one line:\n"+out)
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(out), &rec); err != nil {
			leaks = append(leaks, form+": report is not JSON: "+out)
			continue
		}
		if rec[name] != "(redacted)" {
			leaks = append(leaks, fmt.Sprintf("%s: report shows %s=%v, want (redacted)", form, name, rec[name]))
		}
	}
	cfg, err := loadEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf syncBuffer
	cfg.Report(NewLogger(&buf, 0, "server"))
	var rec map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &rec); err != nil || rec[name] != "(unset)" {
		leaks = append(leaks, fmt.Sprintf("unset: report shows %s=%v, want (unset): %s", name, rec[name], buf.String()))
	}
	return leaks
}

// One test for every Secret row of the registry: a canary set in it never
// reaches the startup report, which shows (redacted), or (unset) when it is
// not set. It replaces the per-secret redaction tests. Marking a row
// non-secret makes it fail, so the flag, not a hand-written call, redacts.
func TestStartupReportRedactsEverySecretSetting(t *testing.T) {
	names := secretNames(t, Settings)
	if len(names) == 0 {
		t.Fatal("no Settings row is marked Secret")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			for _, leak := range reportLeaks(t, name) {
				t.Error(leak)
			}
		})
	}
	t.Run("unmarking a row exposes it", func(t *testing.T) {
		for i, s := range Settings {
			if !secretOf(t, s) {
				continue
			}
			func() {
				saved := Settings
				defer func() { Settings = saved }()
				unmarked := append([]SettingSpec(nil), saved...)
				unmarked[i] = withSecret(t, s, false)
				Settings = unmarked
				if len(reportLeaks(t, s.Name)) == 0 {
					t.Errorf("%s marked non-secret is still redacted: the report must redact by the flag", s.Name)
				}
			}()
		}
	})
}

// The OTel variables that accept a _FILE form are the TelemetrySettings rows
// marked Secret; every other row refuses it, naming the variable and never the
// path (PC-05, ADR-0017).
func TestTelemetryFileFormFollowsTheSecretFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "value")
	if err := os.WriteFile(path, []byte("x-api-token=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, s := range TelemetrySettings {
		secret := secretOf(t, s)
		t.Run(s.Name, func(t *testing.T) {
			tel, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{s.Name + "_FILE": path}))
			if tel != nil {
				t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
			}
			if err != nil && strings.Contains(err.Error(), path) {
				t.Errorf("error names the file path: %v", err)
			}
			named := err != nil && strings.Contains(err.Error(), s.Name+"_FILE")
			if secret && err != nil {
				t.Errorf("%s is marked Secret, so %s_FILE must be read: err = %v", s.Name, s.Name, err)
			}
			if !secret && !named {
				t.Errorf("%s is not Secret, so %s_FILE must be refused naming it: err = %v", s.Name, s.Name, err)
			}
		})
	}
}

// The signal forms of the OTLP headers keep their _FILE form (ADR-0017), and
// the value is read from the file, not echoed.
func TestTelemetrySignalHeadersAcceptFileForm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "headers")
	if err := os.WriteFile(path, []byte("x-api-token=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_LOGS_HEADERS"} {
		tel, err := SetupTelemetry(context.Background(), mapLookup(map[string]string{name + "_FILE": path}))
		if err != nil {
			t.Errorf("%s_FILE: %v", name, err)
		}
		if tel != nil {
			_ = tel.Shutdown(context.Background())
		}
	}
}

// The configuration reference prints the secret note from the flag, at the
// start of the Meaning cell, with the row's own _FILE name, escaped like any
// other registry text. A row not marked Secret gets no note.
func TestConfigReferencePrintsSecretNoteFromTheFlag(t *testing.T) {
	secret := withSecret(t, SettingSpec{Name: "GOLDIE_S", Meaning: "Holds a key."}, true)
	plain := SettingSpec{Name: "GOLDIE_P", Meaning: "Plain."}
	hostile := withSecret(t, SettingSpec{Name: "GOLDIE_X|<b>`", Meaning: "h"}, true)
	ref := string(ConfigReference([]SettingSpec{secret, plain, hostile}))
	for _, want := range []string{
		"| `GOLDIE_S` | (none) | Secret: set it with GOLDIE_S_FILE. Holds a key. |\n",
		"| `GOLDIE_P` | (none) | Plain. |\n",
		"| `GOLDIE_X\\|&lt;b&gt;'` | (none) | Secret: set it with GOLDIE_X\\|&lt;b&gt;'_FILE. h |\n",
	} {
		if !strings.Contains(ref, want) {
			t.Errorf("reference lacks %q:\n%s", want, ref)
		}
	}
	if strings.Contains(ref, "<b>") {
		t.Error("raw HTML in output")
	}
	// The fixed TelemetrySettings and ClientSettings tables always follow and
	// carry their own Secret rows' notes, so the check is on the unmarked
	// row's own line: exactly its Meaning, and its _FILE name nowhere.
	unmarked := string(ConfigReference([]SettingSpec{withSecret(t, secret, false)}))
	if !strings.Contains(unmarked, "\n| `GOLDIE_S` | (none) | Holds a key. |\n") {
		t.Errorf("a row not marked Secret must render its Meaning with no note:\n%s", unmarked)
	}
	if strings.Contains(unmarked, "GOLDIE_S_FILE") {
		t.Errorf("a row not marked Secret carries the note:\n%s", unmarked)
	}
}

// The registry rows no longer repeat the secret note in their Meaning; the
// rendered reference prints it once for every Secret row in all three tables.
func TestRegistryRowsDoNotRepeatTheSecretNote(t *testing.T) {
	ref := string(ConfigReference(Settings))
	for table, rows := range map[string][]SettingSpec{"Settings": Settings, "TelemetrySettings": TelemetrySettings, "ClientSettings": ClientSettings} {
		for _, s := range rows {
			if strings.Contains(s.Meaning, "Secret:") {
				t.Errorf("%s row %s repeats the secret note in its Meaning; the flag prints it", table, s.Name)
			}
			if !secretOf(t, s) {
				continue
			}
			prefix := "| `" + s.Name + "` | (none) | "
			var row string
			for _, line := range strings.Split(ref, "\n") {
				if strings.HasPrefix(line, prefix) {
					row = line
				}
			}
			note := "Secret: set it with " + s.Name + "_FILE."
			if !strings.HasPrefix(row, prefix+note+" ") || strings.Count(row, "Secret:") != 1 {
				t.Errorf("%s row %s: want its Meaning to start with %q once, got %q", table, s.Name, note, row)
			}
		}
	}
}
