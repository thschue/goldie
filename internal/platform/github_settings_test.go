package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitHub sign-in settings (#642, slice 1 of #25). The operator tells goldie
// which OAuth app, which GitHub, which orgs and which operator teams to trust:
// GOLDIE_GITHUB_CLIENT_ID, GOLDIE_GITHUB_CLIENT_SECRET (and _FILE),
// GOLDIE_GITHUB_URL, GOLDIE_GITHUB_ORGS (comma-separated logins),
// GOLDIE_GITHUB_OPERATOR_TEAMS (comma-separated org/slug) and
// GOLDIE_PUBLIC_URL. The client secret is a secret (PC-05, PC-06); a partial
// configuration is refused naming the variable, never a value.

const (
	envGitHubClientID     = "GOLDIE_GITHUB_CLIENT_ID"
	envGitHubClientSecret = "GOLDIE_GITHUB_CLIENT_SECRET"
	envGitHubURL          = "GOLDIE_GITHUB_URL"
	envGitHubOrgs         = "GOLDIE_GITHUB_ORGS"
	envGitHubTeams        = "GOLDIE_GITHUB_OPERATOR_TEAMS"
	envPublicURL          = "GOLDIE_PUBLIC_URL"

	githubSecret = "gh-oauth-secret-0123456789abcdef0123456789abcdef"
)

var githubSettingNames = []string{envGitHubClientID, envGitHubClientSecret, envGitHubURL, envGitHubOrgs, envGitHubTeams, envPublicURL}

// githubEnv is a complete, valid sign-in configuration with overrides applied;
// an override of "-" removes the variable.
func githubEnv(overrides map[string]string) map[string]string {
	env := map[string]string{
		envGitHubClientID:     "Iv1.0123456789abcdef",
		envGitHubClientSecret: githubSecret,
		envGitHubURL:          "https://github.example.com",
		envGitHubOrgs:         "acme,other",
		envGitHubTeams:        "acme/platform,other/ops",
		envPublicURL:          "https://goldie.example.com",
	}
	for k, v := range overrides {
		if v == "-" {
			delete(env, k)
		} else {
			env[k] = v
		}
	}
	return env
}

func TestGitHubSettingsAreInTheRegistryAndOnlyTheClientSecretIsSecret(t *testing.T) {
	for _, name := range githubSettingNames {
		row := settingRow(t, name)
		if row.Meaning == "" {
			t.Errorf("%s has no Meaning", name)
		}
		if got, want := secretOf(t, row), name == envGitHubClientSecret; got != want {
			t.Errorf("%s Secret = %v, want %v", name, got, want)
		}
	}
}

func TestGitHubSettingsAreReadAtStartup(t *testing.T) {
	cfg, err := loadEnv(githubEnv(nil))
	if err != nil {
		t.Fatalf("a complete configuration: %v", err)
	}
	for name, want := range map[string]string{
		envGitHubClientID: "Iv1.0123456789abcdef",
		envGitHubURL:      "https://github.example.com",
		envPublicURL:      "https://goldie.example.com",
	} {
		if got := reported(t, cfg, name); got != want {
			t.Errorf("%s reported %q, want %q", name, got, want)
		}
	}
	if got := effective(t, cfg, envGitHubClientSecret); got != githubSecret {
		t.Errorf("%s effective %v, want the secret", envGitHubClientSecret, got)
	}
	for name, parts := range map[string][]string{
		envGitHubOrgs:  {"acme", "other"},
		envGitHubTeams: {"acme/platform", "other/ops"},
	} {
		got := reported(t, cfg, name)
		for _, p := range parts {
			if !strings.Contains(got, p) {
				t.Errorf("%s reported %q, want it to hold %q", name, got, p)
			}
		}
	}
}

func TestGitHubClientSecretIsReadFromTheFileFormWithoutItsNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(githubSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadEnv(githubEnv(map[string]string{envGitHubClientSecret: "-", envGitHubClientSecret + "_FILE": path}))
	if err != nil {
		t.Fatal(err)
	}
	if got := effective(t, cfg, envGitHubClientSecret); got != githubSecret {
		t.Errorf("from the file: %v, want the secret without its newline", got)
	}
}

func TestGitHubClientSecretBothFormsOrAnUnreadableFileNamesTheVariableNeverTheValue(t *testing.T) {
	_, err := loadEnv(githubEnv(map[string]string{envGitHubClientSecret + "_FILE": "/run/secrets/gh"}))
	if err == nil || !strings.Contains(err.Error(), envGitHubClientSecret) || strings.Contains(err.Error(), githubSecret) {
		t.Errorf("both set: err = %v, want one naming %s and not the secret", err, envGitHubClientSecret)
	}
	_, err = loadEnv(githubEnv(map[string]string{envGitHubClientSecret: "-", envGitHubClientSecret + "_FILE": "/nope/gh"}))
	if err == nil || !strings.Contains(err.Error(), envGitHubClientSecret) || strings.Contains(err.Error(), "/nope/gh") {
		t.Errorf("unreadable file: err = %v, want one naming %s and not the path", err, envGitHubClientSecret)
	}
}

func TestStartupReportShowsTheGitHubClientSecretRedactedOrUnset(t *testing.T) {
	cfg, err := loadEnv(githubEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := reported(t, cfg, envGitHubClientSecret); got != "(redacted)" {
		t.Errorf("set: reported %q, want (redacted)", got)
	}
	var buf syncBuffer
	cfg.Report(NewLogger(&buf, 0, "server"))
	if strings.Contains(buf.String(), githubSecret) {
		t.Errorf("the startup report prints the client secret: %s", buf.String())
	}

	cfg, err = loadEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reported(t, cfg, envGitHubClientSecret); got != "(unset)" {
		t.Errorf("unset: reported %q, want (unset)", got)
	}
}

// A partial configuration stops startup, naming the variable that is wrong or
// missing, and never echoing the secret.
func TestPartialGitHubConfigurationIsRefusedNamingTheVariable(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"client id without orgs":               {githubEnv(map[string]string{envGitHubOrgs: "-", envGitHubTeams: "-"}), envGitHubOrgs},
		"client id without secret":             {githubEnv(map[string]string{envGitHubClientSecret: "-"}), envGitHubClientSecret},
		"secret without client id":             {githubEnv(map[string]string{envGitHubClientID: "-"}), envGitHubClientID},
		"orgs without client id":               {githubEnv(map[string]string{envGitHubClientID: "-", envGitHubClientSecret: "-"}), envGitHubClientID},
		"operator team whose org isn't listed": {githubEnv(map[string]string{envGitHubTeams: "acme/platform,stranger/ops"}), envGitHubTeams},
		"operator team without a slug":         {githubEnv(map[string]string{envGitHubTeams: "acme"}), envGitHubTeams},
		"operator team without an org":         {githubEnv(map[string]string{envGitHubTeams: "/platform"}), envGitHubTeams},
		"operator team with an empty slug":     {githubEnv(map[string]string{envGitHubTeams: "acme/"}), envGitHubTeams},
		"operator team with extra segments":    {githubEnv(map[string]string{envGitHubTeams: "acme/platform/ops"}), envGitHubTeams},
		"operator teams without any orgs":      {githubEnv(map[string]string{envGitHubClientID: "-", envGitHubClientSecret: "-", envGitHubOrgs: "-"}), envGitHubTeams},
	} {
		_, err := loadEnv(tc.env)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one naming %s", name, err, tc.want)
			continue
		}
		if strings.Contains(err.Error(), githubSecret) {
			t.Errorf("%s: the error echoes the client secret: %v", name, err)
		}
	}
}

func TestGitHubPublicURLMustBeHTTPSUnlessLoopback(t *testing.T) {
	for _, ok := range []string{
		"https://goldie.example.com",
		"https://goldie.example.com:8443",
		"http://localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
	} {
		if _, err := loadEnv(githubEnv(map[string]string{envPublicURL: ok})); err != nil {
			t.Errorf("%s = %q: %v, want it accepted", envPublicURL, ok, err)
		}
	}
	for _, bad := range []string{
		"http://goldie.example.com",
		"http://localhost.evil.example.com",
		"http://127.0.0.1.evil.example.com",
		"http://10.0.0.5:8080",
		"ftp://goldie.example.com",
		"javascript:alert(1)",
		"goldie.example.com",
		"https://",
		"https://goldie.example.com\r\nX-Injected: 1",
	} {
		_, err := loadEnv(githubEnv(map[string]string{envPublicURL: bad}))
		if err == nil || !strings.Contains(err.Error(), envPublicURL) {
			t.Errorf("%s = %q: err = %v, want one naming %s", envPublicURL, bad, err, envPublicURL)
			continue
		}
		if strings.Contains(err.Error(), "X-Injected") {
			t.Errorf("%s = %q: the error echoes the value: %v", envPublicURL, bad, err)
		}
	}
}

// With none of them set, LoadConfig is as before: no error, and empty values
// count as unset, as for every other setting.
func TestWithoutGitHubSettingsStartupIsUnchanged(t *testing.T) {
	if _, err := loadEnv(nil); err != nil {
		t.Fatalf("no GitHub settings is not an error: %v", err)
	}
	empty := map[string]string{}
	for _, name := range githubSettingNames {
		empty[name] = ""
	}
	if _, err := loadEnv(empty); err != nil {
		t.Errorf("empty GitHub settings: %v, want them treated as unset", err)
	}
}

func TestConfigurationReferenceDocumentsTheGitHubSettings(t *testing.T) {
	ref := committedReference(t)
	for _, name := range githubSettingNames {
		if !strings.Contains(ref, "| `"+name+"` |") {
			t.Errorf("docs/reference/configuration.md has no table row for %s (run: ./run regen)", name)
		}
	}
	if !strings.Contains(ref, envGitHubClientSecret+"_FILE") {
		t.Errorf("docs/reference/configuration.md does not name %s_FILE", envGitHubClientSecret)
	}
}
