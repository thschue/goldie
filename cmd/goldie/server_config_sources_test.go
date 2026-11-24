package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// #846: goldie server is configured by flags, a config file or the
// environment. Precedence is flag, environment variable, config file, default.
// A secret is never a flag or inline in the file; every check still applies,
// naming the setting and where the value came from, never the value.

// serverRun is one `goldie server` run observed at the process boundary.
type serverRun struct {
	report map[string]any // the "config" startup log line; nil when the server stopped first
	out    string         // everything the process wrote, stdout and stderr
	failed bool           // the process stopped before the report
}

// runServerConfig starts goldie server with args and env (added to a base
// that has only the listen addresses), reads the startup report, and stops the
// server. A server that stops before reporting has failed: out is its message.
func runServerConfig(t *testing.T, args []string, env ...string) serverRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goldieBinary(t), append([]string{"server"}, args...)...)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "GOLDIE_ADDR=127.0.0.1:0", "GOLDIE_ADMIN_ADDR=127.0.0.1:0", "GOLDIE_DRAIN_SECONDS=0"}, env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var run serverRun
	var lines []string
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		var m map[string]any
		if json.Unmarshal([]byte(sc.Text()), &m) == nil && m["msg"] == "config" {
			run.report = m
			break
		}
	}
	if err := sc.Err(); err != nil {
		lines = append(lines, "log capture stopped: "+err.Error())
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	run.out = strings.Join(lines, "\n") + "\n" + stderr.String()
	run.failed = run.report == nil
	return run
}

// configFile writes a YAML config file and returns its path.
func configFile(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "goldie.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// baseFile is the settings a server needs, as a config file holds them: the
// token by its file path, never inline.
func baseFile(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("content_dir: %s\napi_token_file: %s\n", contentDir(t), tokenFile(t, testToken))
}

// baseEnv is the same settings as environment variables.
func baseEnv(t *testing.T) []string {
	t.Helper()
	return []string{"GOLDIE_CONTENT_DIR=" + contentDir(t), "GOLDIE_API_TOKEN_FILE=" + tokenFile(t, testToken)}
}

// failure is the text of a stopped server's message: after "invalid
// configuration", before cobra's usage text.
func failure(out string) string {
	_, f, _ := strings.Cut(out, "invalid configuration")
	f, _, _ = strings.Cut(f, "Usage:")
	return f
}

// leaves flattens a decoded JSON value into path -> text pairs.
func leaves(prefix string, v any, into map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			leaves(prefix+"."+k, e, into)
		}
	case []any:
		for i, e := range x {
			leaves(fmt.Sprintf("%s.%d", prefix, i), e, into)
		}
	default:
		into[prefix] = fmt.Sprint(x)
	}
}

// reportAbout is the text the startup report carries about one setting,
// whatever its shape: every leaf whose path names the setting, lower-cased.
func reportAbout(report map[string]any, setting string) string {
	all := map[string]string{}
	leaves("", report, all)
	var parts []string
	for path, text := range all {
		if strings.Contains(strings.ToLower(path), setting) {
			parts = append(parts, text)
		}
	}
	return strings.ToLower(strings.Join(parts, " "))
}

// reportValue is the effective value the report logs for a GOLDIE_ variable.
func reportValue(t *testing.T, run serverRun, name string) string {
	t.Helper()
	if run.report == nil {
		t.Fatalf("the server stopped instead of starting: %s", run.out)
	}
	v, ok := run.report[name]
	if !ok {
		t.Fatalf("the startup report lacks %s: %v", name, run.report)
	}
	if m, isMap := v.(map[string]any); isMap { // a {value, source} entry
		v = m["value"]
	}
	return fmt.Sprint(v)
}

// Acceptance 1: --config <file> reads the settings from a YAML file, keyed by
// the setting's name in lower case without GOLDIE_; GOLDIE_CONFIG names the
// file too.
func TestServerReadsItsSettingsFromAConfigFile(t *testing.T) {
	file := configFile(t, baseFile(t)+"poll_interval: 45s\nmcp_name: cfg-mcp\n")
	for name, tc := range map[string]struct {
		args []string
		env  []string
	}{
		"flag":     {args: []string{"--config", file}},
		"variable": {env: []string{"GOLDIE_CONFIG=" + file}},
	} {
		run := runServerConfig(t, tc.args, tc.env...)
		if got := reportValue(t, run, "GOLDIE_POLL_INTERVAL"); got != "45s" {
			t.Errorf("%s: poll interval = %q, want the file's 45s", name, got)
		}
		if got := reportValue(t, run, "GOLDIE_MCP_NAME"); got != "cfg-mcp" {
			t.Errorf("%s: mcp name = %q, want the file's cfg-mcp", name, got)
		}
		if got := reportValue(t, run, "GOLDIE_CONTENT_DIR"); got != contentDir(t) {
			t.Errorf("%s: content dir = %q, want the file's", name, got)
		}
	}
}

// Acceptance 1: sources is a YAML list with the fields of GOLDIE_SOURCES.
func TestServerReadsSourcesAsAYAMLList(t *testing.T) {
	file := configFile(t, fmt.Sprintf(`api_token_file: %s
sources:
  - name: platform
    type: dir
    dir: %s
    claims:
      global: true
  - name: team-a
    type: dir
    dir: %s
    claims:
      projects: ["git.example.com/team-a/*"]
`, tokenFile(t, testToken), contentDir(t), contentDir(t)))
	run := runServerConfig(t, []string{"--config", file})
	got := reportValue(t, run, "GOLDIE_SOURCES")
	if pi, ti := strings.Index(got, "platform"), strings.Index(got, "team-a"); pi < 0 || ti < pi {
		t.Errorf("sources in the report = %q, want platform then team-a, in listed order", got)
	}
}

// Acceptance 2: every non-secret setting is a goldie server flag listed in
// --help; no secret is.
func TestServerHelpListsAFlagForEverySettingButTheSecrets(t *testing.T) {
	out, err := exec.Command(goldieBinary(t), "server", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("server --help: %v\n%s", err, out)
	}
	help := string(out)
	for _, want := range []string{"--git-url", "--poll-interval", "--config"} {
		if !strings.Contains(help, want) {
			t.Errorf("--help does not list %s:\n%s", want, help)
		}
	}
	for _, s := range platform.Settings {
		flag := "--" + strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(s.Name, "GOLDIE_")), "_", "-")
		listed := regexp.MustCompile(regexp.QuoteMeta(flag) + `(\s|=|$)`).MatchString(help)
		if s.Secret && listed {
			t.Errorf("--help lists %s: a secret must not be a flag (it shows in the process list)", flag)
		}
		if !s.Secret && !listed {
			t.Errorf("--help does not list %s for %s", flag, s.Name)
		}
	}
}

// Acceptance 2: a flag sets the setting.
func TestServerFlagSetsASetting(t *testing.T) {
	run := runServerConfig(t, []string{"--poll-interval", "33s", "--mcp-name", "flag-mcp"}, baseEnv(t)...)
	if got := reportValue(t, run, "GOLDIE_POLL_INTERVAL"); got != "33s" {
		t.Errorf("poll interval = %q, want 33s", got)
	}
	if got := reportValue(t, run, "GOLDIE_MCP_NAME"); got != "flag-mcp" {
		t.Errorf("mcp name = %q, want flag-mcp", got)
	}
}

// Acceptance 3: flag, then environment variable, then config file, then default.
func TestServerPrecedenceIsFlagThenEnvironmentThenFileThenDefault(t *testing.T) {
	file := configFile(t, baseFile(t)+"poll_interval: 10s\n")
	for _, tc := range []struct {
		name string
		args []string
		env  []string
		want string
	}{
		{"flag, environment and file", []string{"--poll-interval", "30s"}, []string{"GOLDIE_POLL_INTERVAL=20s"}, "30s"},
		{"environment and file", nil, []string{"GOLDIE_POLL_INTERVAL=20s"}, "20s"},
		{"file alone", nil, nil, "10s"},
	} {
		run := runServerConfig(t, append([]string{"--config", file}, tc.args...), tc.env...)
		if got := reportValue(t, run, "GOLDIE_POLL_INTERVAL"); got != tc.want {
			t.Errorf("%s: poll interval = %q, want %q", tc.name, got, tc.want)
		}
	}
	// Nothing set: the default.
	run := runServerConfig(t, nil, baseEnv(t)...)
	if got := reportValue(t, run, "GOLDIE_POLL_INTERVAL"); got != "1m" {
		t.Errorf("default poll interval = %q, want 1m", got)
	}
}

// Acceptance 4: a secret given as a flag stops the server, which never echoes
// the value; the flag does not exist.
func TestServerRefusesASecretAsAFlag(t *testing.T) {
	const secret = "flag-secret-0123456789abcdef0123456789"
	// A control: the server takes a non-secret flag, so a refusal below is
	// about the secret and not about flags in general.
	if run := runServerConfig(t, []string{"--poll-interval", "30s"}, baseEnv(t)...); run.failed {
		t.Fatalf("the server refused a non-secret flag: %s", run.out)
	}
	for _, flag := range []string{"--api-token", "--git-token", "--session-key"} {
		for name, args := range map[string][]string{"separate": {flag, secret}, "equals": {flag + "=" + secret}} {
			run := runServerConfig(t, args, baseEnv(t)...)
			if !run.failed {
				t.Errorf("%s %s: the server started", flag, name)
			}
			if strings.Contains(run.out, secret) {
				t.Errorf("%s %s: output echoes the secret: %s", flag, name, run.out)
			}
		}
	}
}

// Acceptance 4: a secret inline in the config file stops the server naming the
// key, never the value; the *_file form is accepted.
func TestServerRefusesASecretInlineInTheConfigFile(t *testing.T) {
	const secret = "inline-secret-0123456789abcdef0123456789"
	for _, key := range []string{"api_token", "git_token", "session_key"} {
		file := configFile(t, baseFile(t)+key+": "+secret+"\n")
		run := runServerConfig(t, []string{"--config", file})
		if !run.failed {
			t.Errorf("%s inline: the server started", key)
		}
		if !strings.Contains(failure(run.out), key) {
			t.Errorf("%s inline: the failure does not name the key: %s", key, run.out)
		}
		if strings.Contains(run.out, secret) {
			t.Errorf("%s inline: output echoes the secret: %s", key, run.out)
		}
	}
	// The file path of a secret is accepted (baseFile names api_token_file).
	run := runServerConfig(t, []string{"--config", configFile(t, baseFile(t))})
	if run.failed {
		t.Errorf("api_token_file in the file stopped the server: %s", run.out)
	}
}

// Acceptance 5: a bad value stops the server naming the setting and where it
// came from, never the value, from each source.
func TestServerBadValueNamesTheSettingAndItsSourceNeverTheValue(t *testing.T) {
	const bad = "zz-bad-marker-17"
	for _, tc := range []struct {
		source string // the word the failure must carry
		others []string
		args   []string
		env    []string
	}{
		{"flag", []string{"environment", "file"}, []string{"--poll-interval", bad}, baseEnv(t)},
		{"environment", []string{"flag", "file"}, nil, append([]string{"GOLDIE_POLL_INTERVAL=" + bad}, baseEnv(t)...)},
		{"file", []string{"flag", "environment"}, []string{"--config", configFile(t, baseFile(t)+"poll_interval: "+bad+"\n")}, nil},
	} {
		run := runServerConfig(t, tc.args, tc.env...)
		f := strings.ToLower(failure(run.out))
		if !run.failed {
			t.Errorf("%s: the server started on a bad poll interval", tc.source)
			continue
		}
		if !strings.Contains(f, "poll_interval") && !strings.Contains(f, "poll-interval") {
			t.Errorf("%s: the failure does not name the setting: %s", tc.source, run.out)
		}
		if !strings.Contains(f, tc.source) {
			t.Errorf("%s: the failure does not name its source: %s", tc.source, run.out)
		}
		for _, other := range tc.others {
			if strings.Contains(f, other) {
				t.Errorf("%s: the failure names the wrong source %q: %s", tc.source, other, run.out)
			}
		}
		if strings.Contains(run.out, bad) {
			t.Errorf("%s: output echoes the value: %s", tc.source, run.out)
		}
	}
}

// Acceptance 5: every existing check still applies to values from the file (a
// content dir and a git URL together), naming both settings.
func TestServerChecksStillApplyToValuesFromTheFile(t *testing.T) {
	file := configFile(t, baseFile(t)+"git_url: https://git.example.com/org/content.git\n")
	run := runServerConfig(t, []string{"--config", file})
	if !run.failed {
		t.Fatalf("the server started with content_dir and git_url both set: %s", run.out)
	}
	f := strings.ToLower(failure(run.out))
	for _, want := range []string{"content_dir", "git_url"} {
		if !strings.Contains(f, want) && !strings.Contains(f, strings.ReplaceAll(want, "_", "-")) {
			t.Errorf("the failure does not name %s: %s", want, run.out)
		}
	}
}

// Acceptance 5: an unknown key in the file stops the server, naming the key.
func TestServerRefusesAnUnknownKeyInTheConfigFile(t *testing.T) {
	file := configFile(t, baseFile(t)+"poll_intervall: 30s\n")
	run := runServerConfig(t, []string{"--config", file})
	if !run.failed {
		t.Fatalf("the server started with an unknown key: %s", run.out)
	}
	if !strings.Contains(failure(run.out), "poll_intervall") {
		t.Errorf("the failure does not name the unknown key: %s", run.out)
	}
}

// Acceptance 6: the startup report shows the source of each value and keeps
// secrets redacted.
func TestServerStartupReportShowsEachValuesSource(t *testing.T) {
	file := configFile(t, baseFile(t)+"mcp_name: cfg-mcp\n")
	run := runServerConfig(t, []string{"--config", file, "--poll-interval", "30s"}, "GOLDIE_ACTIVITY_REORDER_WINDOW=3s")
	if run.failed {
		t.Fatalf("the server stopped: %s", run.out)
	}
	for _, tc := range []struct{ setting, value, source string }{
		{"poll_interval", "30s", "flag"},
		{"activity_reorder_window", "3s", "env"},
		{"mcp_name", "cfg-mcp", "file"},
		{"swagger_ui_enabled", "true", "default"},
	} {
		about := reportAbout(run.report, tc.setting)
		if !strings.Contains(about, tc.value) {
			t.Errorf("%s: the report lacks the value %q: %v", tc.setting, tc.value, run.report)
		}
		if !strings.Contains(about, tc.source) {
			t.Errorf("%s: the report does not show its source (%s): %q", tc.setting, tc.source, about)
		}
	}
	if strings.Contains(run.out, testToken) {
		t.Errorf("the startup report echoes the API token: %s", run.out)
	}
	if !strings.Contains(reportAbout(run.report, "api_token"), "redacted") {
		t.Errorf("the API token is not shown as redacted: %v", run.report)
	}
}

// Acceptance 7: the reference documents the three ways and the precedence, and
// PC-04 no longer says environment-only.
func TestConfigurationDocsDescribeTheThreeWaysAndThePrecedence(t *testing.T) {
	ref, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	intro, _, _ := strings.Cut(string(ref), "| Variable |")
	for _, want := range []string{"--config", "GOLDIE_CONFIG", "flag", "environment", "config file", "default"} {
		if !strings.Contains(intro, want) {
			t.Errorf("configuration.md does not mention %q before the table:\n%s", want, intro)
		}
	}
	if !regexp.MustCompile(`(?is)flag.*environment.*config file.*default`).MatchString(intro) {
		t.Errorf("configuration.md does not state the precedence flag, environment, config file, default:\n%s", intro)
	}
	if strings.Contains(intro, "Configuration is environment only") {
		t.Errorf("configuration.md still says configuration is environment only:\n%s", intro)
	}
	pc, err := os.ReadFile(filepath.Join("..", "..", "docs", "platform-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	var pc04 string
	for _, l := range strings.Split(string(pc), "\n") {
		if strings.HasPrefix(l, "| PC-04 ") {
			pc04 = l
		}
	}
	if strings.Contains(pc04, "Environment-only") || !strings.Contains(pc04, "config file") || !strings.Contains(pc04, "flag") {
		t.Errorf("PC-04 is not amended for flags and a config file: %s", pc04)
	}
}
