package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #258: the git Source settings live in the settings registry, so LoadConfig
// reads them, the startup report logs them (secrets redacted, the URL with its
// userinfo, query and fragment redacted) and ./run regen documents them. One
// GOLDIE_POLL_INTERVAL paces every Source, dir included (#276). The git Source
// and GOLDIE_CONTENT_DIR exclude each other. Git Source URLs are
// operator-trusted (maintainer decision, 2026-11-23), and the GOLDIE_GIT_URL
// text and an ADR say so.
//
// The names are test-local so this file compiles before the implementation
// adds its Env constants.
const (
	envGitURL            = "GOLDIE_GIT_URL"
	envGitRef            = "GOLDIE_GIT_REF"
	envGitToken          = "GOLDIE_GIT_TOKEN"
	envGitSSHKey         = "GOLDIE_GIT_SSH_KEY"
	envGitSSHKnownHosts  = "GOLDIE_GIT_SSH_KNOWN_HOSTS"
	envGitCABundle       = "GOLDIE_GIT_CA_BUNDLE"
	envGitAllowFileURL   = "GOLDIE_GIT_ALLOW_FILE_URL"
	envPollInterval      = "GOLDIE_POLL_INTERVAL"
	gitSampleURL         = "https://git.example.com/org/content.git"
	gitSampleCABundle    = "-----BEGIN CERTIFICATE-----\nMIIBsampleCAbundle0123456789\n-----END CERTIFICATE-----"
	gitSampleCABundleTag = "MIIBsampleCAbundle"
)

// gitSettingNames are every setting #258 adds to the Settings registry.
var gitSettingNames = []string{envGitURL, envGitRef, envGitToken, envGitSSHKey, envGitSSHKnownHosts, envGitCABundle, envGitAllowFileURL, envPollInterval}

// settingRow returns the Settings row of name, failing the test without one.
func settingRow(t *testing.T, name string) SettingSpec {
	t.Helper()
	for _, s := range Settings {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("%s is not in the Settings registry (#258)", name)
	return SettingSpec{}
}

// effective is the value LoadConfig produced for name, read through the
// registry row's Value accessor (the value the report logs before redaction).
func effective(t *testing.T, cfg Config, name string) any {
	t.Helper()
	s := settingRow(t, name)
	if s.Value == nil {
		t.Fatalf("%s has no Value accessor", name)
	}
	return s.Value(cfg)
}

// Every git Source setting and GOLDIE_POLL_INTERVAL is a registry row with its
// documented default, and the committed configuration reference has its row
// (./run regen, PC-10).
func TestGitSourceSettingsAreRegisteredWithTheirDefaults(t *testing.T) {
	defaults := map[string]string{
		envGitURL:           "",
		envGitToken:         "",
		envGitSSHKey:        "",
		envGitSSHKnownHosts: "",
		envGitCABundle:      "",
		envGitAllowFileURL:  "false",
		envPollInterval:     "1m",
	}
	ref := committedReference(t)
	for _, name := range gitSettingNames {
		s := settingRow(t, name)
		if want, ok := defaults[name]; ok && s.Default != want {
			t.Errorf("%s default = %q, want %q", name, s.Default, want)
		}
		if !strings.Contains(ref, "\n| `"+name+"` | ") {
			t.Errorf("committed docs/reference/configuration.md has no row for %s (run: ./run regen)", name)
		}
	}
}

// GOLDIE_GIT_URL and GOLDIE_GIT_REF reach the configuration as written and the
// startup report logs them; a URL without userinfo, query or fragment has
// nothing to redact and is logged unchanged.
func TestGitURLAndRefAreLoadedAndReported(t *testing.T) {
	cfg, err := loadEnv(map[string]string{envGitURL: gitSampleURL, envGitRef: "release/v2"})
	if err != nil {
		t.Fatalf("LoadConfig with a git URL and ref: %v", err)
	}
	if got := reported(t, cfg, envGitURL); got != gitSampleURL {
		t.Errorf("report shows %s=%q, want %q", envGitURL, got, gitSampleURL)
	}
	if got := reported(t, cfg, envGitRef); got != "release/v2" {
		t.Errorf("report shows %s=%q, want release/v2", envGitRef, got)
	}
	_, rec, out := reportKeys(t, cfg)
	if rec[envGitURL] == nil || rec[envGitRef] == nil {
		t.Errorf("report lacks %s or %s:\n%s", envGitURL, envGitRef, out)
	}
}

// GOLDIE_POLL_INTERVAL is a Go duration of at least 10s, 1m when unset or
// empty. The report logs the effective value as written.
func TestPollIntervalAcceptsDurationsOfAtLeastTenSeconds(t *testing.T) {
	for value, want := range map[string]string{
		"":        "1m",
		"10s":     "10s",
		"10000ms": "10000ms",
		"90s":     "90s",
		"1m":      "1m",
		"2h30m":   "2h30m",
	} {
		cfg, err := loadEnv(map[string]string{envPollInterval: value})
		if err != nil {
			t.Errorf("%s=%q: LoadConfig: %v", envPollInterval, value, err)
			continue
		}
		if got := reported(t, cfg, envPollInterval); got != want {
			t.Errorf("%s=%q: report shows %q, want %q", envPollInterval, value, got, want)
		}
	}
	cfg, err := loadEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reported(t, cfg, envPollInterval); got != "1m" {
		t.Errorf("%s unset: report shows %q, want the default 1m", envPollInterval, got)
	}
}

// A value under 10s, or one that is not a Go duration, fails startup naming
// GOLDIE_POLL_INTERVAL and never echoing the value (PC-04).
func TestPollIntervalRejectsShortOrMalformedValues(t *testing.T) {
	for _, value := range []string{
		"9s", "9999ms", "9.999s", "1s", "1ns", "0", "0s", "-30s", "-1m",
		"1", "60", "abc-canary-7f3a", "1 m", "1m\x00", "9999999999999h",
		"1m;rm -rf /", "<script>1m</script>", "1\u202em",
	} {
		_, err := loadEnv(map[string]string{envPollInterval: value})
		if err == nil {
			t.Errorf("%s=%q: LoadConfig accepted it, want a failure", envPollInterval, value)
			continue
		}
		if !strings.Contains(err.Error(), envPollInterval) {
			t.Errorf("%s=%q: err = %v, want it to name %s", envPollInterval, value, err, envPollInterval)
		}
		for _, echo := range []string{"abc-canary-7f3a", "9999ms", "9999999999999h", "rm -rf", "<script>", "\u202e"} {
			if strings.Contains(value, echo) && strings.Contains(err.Error(), echo) {
				t.Errorf("%s=%q: err = %v echoes the value", envPollInterval, value, err)
			}
		}
	}
}

// The poll interval applies to every Source, dir included, and the
// reference says so and gives the 10s floor.
func TestPollIntervalTextCoversEverySource(t *testing.T) {
	m := settingRow(t, envPollInterval).Meaning
	for _, want := range []string{"dir", "git", "10s"} {
		if !strings.Contains(m, want) {
			t.Errorf("%s description %q does not state %q", envPollInterval, m, want)
		}
	}
}

// GOLDIE_GIT_ALLOW_FILE_URL is true or false, false by default; anything else
// fails startup naming it and never echoing the value.
func TestGitAllowFileURLIsABooleanDefaultingToFalse(t *testing.T) {
	for value, want := range map[string]bool{"": false, "false": false, "true": true} {
		cfg, err := loadEnv(map[string]string{envGitAllowFileURL: value})
		if err != nil {
			t.Errorf("%s=%q: LoadConfig: %v", envGitAllowFileURL, value, err)
			continue
		}
		_, rec, out := reportKeys(t, cfg)
		if got, ok := rec[envGitAllowFileURL].(bool); !ok || got != want {
			t.Errorf("%s=%q: report shows %v, want %v:\n%s", envGitAllowFileURL, value, rec[envGitAllowFileURL], want, out)
		}
	}
	for _, value := range []string{"maybe-canary", "yes", "1", "file://"} {
		_, err := loadEnv(map[string]string{envGitAllowFileURL: value})
		if err == nil || !strings.Contains(err.Error(), envGitAllowFileURL) {
			t.Errorf("%s=%q: err = %v, want a failure naming the variable", envGitAllowFileURL, value, err)
			continue
		}
		if strings.Contains(err.Error(), "maybe-canary") || strings.Contains(err.Error(), "file://") {
			t.Errorf("%s=%q: err = %v echoes the value", envGitAllowFileURL, value, err)
		}
	}
}

// The token, the SSH key and the known hosts are read from their _FILE form
// with the trailing newline dropped, CRLF included: a key file saved on
// Windows must not leave a carriage return at its end. The key's lines are
// kept (CRLF may stay or become LF).
func TestGitSecretsAreReadFromTheirFileForm(t *testing.T) {
	const keyLF = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAACmFlczI1Ni1jdHIAAAAGYmNyeXB0\nAAAAGAAAABBsamplekeybody0123456789\n-----END OPENSSH PRIVATE KEY-----"
	const hostsLF = "git.example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIsample0\n[git.example.com]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIsample1"
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	cases := []struct {
		name, file, want string
	}{
		{envGitToken, "glpat-file-token-0123456789\n", "glpat-file-token-0123456789"},
		{envGitToken, "glpat-file-token-0123456789\r\n", "glpat-file-token-0123456789"},
		{envGitToken, "glpat-file-token-0123456789", "glpat-file-token-0123456789"},
		{envGitSSHKey, keyLF + "\n", keyLF},
		{envGitSSHKey, crlf(keyLF) + "\r\n", keyLF},
		{envGitSSHKnownHosts, hostsLF + "\n", hostsLF},
		{envGitSSHKnownHosts, crlf(hostsLF) + "\r\n", hostsLF},
	}
	for i, c := range cases {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("secret-%d", i))
		if err := os.WriteFile(path, []byte(c.file), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := loadEnv(map[string]string{c.name + "_FILE": path})
		if err != nil {
			t.Errorf("%s_FILE (case %d): LoadConfig: %v", c.name, i, err)
			continue
		}
		got, ok := effective(t, cfg, c.name).(string)
		if !ok {
			t.Errorf("%s_FILE (case %d): effective value is %T, want the file's text", c.name, i, effective(t, cfg, c.name))
			continue
		}
		if strings.HasSuffix(got, "\r") || strings.HasSuffix(got, "\n") {
			t.Errorf("%s_FILE (case %d): value %q keeps a trailing CR or LF", c.name, i, got)
		}
		if strings.ReplaceAll(got, "\r\n", "\n") != c.want {
			t.Errorf("%s_FILE (case %d): value %q, want the file's lines %q", c.name, i, got, c.want)
		}
		if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\r") {
			t.Errorf("%s_FILE (case %d): value %q has a lone carriage return", c.name, i, got)
		}
		if rep := reported(t, cfg, c.name); rep != "(redacted)" {
			t.Errorf("%s_FILE (case %d): report shows %q, want (redacted)", c.name, i, rep)
		}
	}
}

// A secret set both as a value and as _FILE, an empty _FILE, or a _FILE that
// cannot be read fails startup naming the variable, never the value or the
// path (PC-05). GOLDIE_GIT_CA_BUNDLE follows the same _FILE rules.
func TestGitSecretFileFormEdgeCases(t *testing.T) {
	const canary = "canary-gitfile-" + canaryCore
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	if err := os.WriteFile(good, []byte(canary+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing-"+canaryCore)
	for _, name := range []string{envGitToken, envGitSSHKey, envGitSSHKnownHosts, envGitCABundle} {
		for form, c := range map[string]struct {
			env  map[string]string
			want string
		}{
			"value and _FILE":  {map[string]string{name: canary, name + "_FILE": good}, name + " and " + name + "_FILE"},
			"empty value too":  {map[string]string{name: "", name + "_FILE": good}, name + " and " + name + "_FILE"},
			"empty _FILE":      {map[string]string{name + "_FILE": ""}, name + "_FILE"},
			"unreadable _FILE": {map[string]string{name + "_FILE": missing}, name + "_FILE"},
			"directory _FILE":  {map[string]string{name + "_FILE": dir}, name + "_FILE"},
		} {
			_, err := loadEnv(c.env)
			if err == nil {
				t.Errorf("%s, %s: LoadConfig accepted it, want a failure", name, form)
				continue
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s, %s: err = %v, want it to name %q", name, form, err, c.want)
			}
			if strings.Contains(err.Error(), canaryCore) || strings.Contains(err.Error(), dir) {
				t.Errorf("%s, %s: err = %v echoes the value or the path", name, form, err)
			}
		}
	}
}

// GOLDIE_GIT_CA_BUNDLE, set directly or through _FILE, is reported only as
// set or unset: never a line of the bundle, the same for any bundle, and
// (unset) when it is not set. It is not a Secret row (only the token, the key
// and the known hosts are).
func TestGitCABundleIsReportedOnlyAsSetOrUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.pem")
	other := strings.ReplaceAll(gitSampleCABundle, "sample", "second")
	if err := os.WriteFile(path, []byte(other+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsetCfg, err := loadEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reported(t, unsetCfg, envGitCABundle); got != "(unset)" {
		t.Errorf("%s unset: report shows %q, want (unset)", envGitCABundle, got)
	}
	var seen []string
	for form, env := range map[string]map[string]string{
		"value": {envGitCABundle: gitSampleCABundle},
		"file":  {envGitCABundle + "_FILE": path},
	} {
		cfg, err := loadEnv(env)
		if err != nil {
			t.Errorf("%s: LoadConfig: %v", form, err)
			continue
		}
		_, rec, out := reportKeys(t, cfg)
		got, ok := rec[envGitCABundle].(string)
		if !ok {
			t.Errorf("%s: report shows %s=%v, want a string saying it is set:\n%s", form, envGitCABundle, rec[envGitCABundle], out)
			continue
		}
		if got == "" || got == "(unset)" {
			t.Errorf("%s: report shows %s=%q, want it reported as set", form, envGitCABundle, got)
		}
		for _, part := range []string{"BEGIN", "CERTIFICATE", gitSampleCABundleTag, "MIIBsecond"} {
			if strings.Contains(out, part) {
				t.Errorf("%s: report contains %q of the CA bundle:\n%s", form, part, out)
			}
		}
		seen = append(seen, got)
	}
	for _, v := range seen[1:] {
		if v != seen[0] {
			t.Errorf("CA bundle reported differently for different bundles (%q, %q): it must only say set", seen[0], v)
		}
	}
	if secretOf(t, settingRow(t, envGitCABundle)) {
		t.Errorf("%s is marked Secret; the issue reports it as set or unset, and only the token, key and known hosts are Secret", envGitCABundle)
	}
}

// GOLDIE_CONTENT_DIR and GOLDIE_GIT_URL together fail in LoadConfig, after the
// _FILE forms are read, naming both and echoing neither, with every other
// problem in the same error; so goldie health refuses them too.
func TestContentDirAndGitURLExcludeEachOther(t *testing.T) {
	const pw = "pw-" + canaryCore
	dirValue := "/srv/content-" + canaryCore[:8]
	url := "https://deploy:" + pw + "@git.example.com/org/content.git"
	urlFile := filepath.Join(t.TempDir(), "url")
	if err := os.WriteFile(urlFile, []byte(url+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for form, env := range map[string]map[string]string{
		"both":           {EnvContentDir: dirValue, envGitURL: url},
		"url from file":  {EnvContentDir: dirValue, envGitURL + "_FILE": urlFile},
		"dir from file":  {EnvContentDir + "_FILE": urlFile, envGitURL: gitSampleURL},
		"plain git url":  {EnvContentDir: dirValue, envGitURL: gitSampleURL},
		"unparsable url": {EnvContentDir: dirValue, envGitURL: "https://deploy:" + pw + "@git.example.com/%zz"},
	} {
		_, err := loadEnv(env)
		if err == nil {
			t.Errorf("%s: LoadConfig accepted %s and %s together", form, EnvContentDir, envGitURL)
			continue
		}
		for _, name := range []string{EnvContentDir, envGitURL} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("%s: err = %v, want it to name %s", form, err, name)
			}
		}
		for _, hidden := range []string{canaryCore, canaryCore[:8], "deploy", "git.example.com", urlFile} {
			if strings.Contains(err.Error(), hidden) {
				t.Errorf("%s: err = %v echoes a value or path (%q)", form, err, hidden)
			}
		}
	}
	_, err := loadEnv(map[string]string{EnvContentDir: dirValue, envGitURL: gitSampleURL, EnvLogLevel: "loud"})
	if err == nil || !strings.Contains(err.Error(), EnvLogLevel) || !strings.Contains(err.Error(), envGitURL) {
		t.Errorf("err = %v, want the Source conflict and the bad log level in one error", err)
	}
	for form, env := range map[string]map[string]string{
		"dir only":           {EnvContentDir: dirValue},
		"git only":           {envGitURL: gitSampleURL},
		"neither":            {},
		"git url empty":      {EnvContentDir: dirValue, envGitURL: ""},
		"content dir empty":  {EnvContentDir: "", envGitURL: gitSampleURL},
		"poll with dir only": {EnvContentDir: dirValue, envPollInterval: "30s"},
		"poll with git only": {envGitURL: gitSampleURL, envPollInterval: "30s"},
	} {
		if _, err := loadEnv(env); err != nil {
			t.Errorf("%s: LoadConfig: %v, want it accepted", form, err)
		}
	}
}

// The startup report logs GOLDIE_GIT_URL with its userinfo, query and
// fragment redacted, whether or not the URL parses: a password, a token in
// the user name, a query or a fragment planted in it never reaches the
// report, nor the LoadConfig error. A parsable URL keeps its host and path.
func TestStartupReportRedactsTheGitURL(t *testing.T) {
	const (
		user = "usr" + canaryCore
		pw   = "pwd" + canaryCore
		qkey = "qky" + canaryCore
		qval = "qvl" + canaryCore
		frag = "frg" + canaryCore
	)
	parsable := map[string]string{
		"user and password": "https://" + user + ":" + pw + "@git.example.com/org/content.git",
		"token as user":     "https://" + pw + "@git.example.com/org/content.git",
		"empty user":        "https://:" + pw + "@git.example.com/org/content.git",
		"at in password":    "https://" + user + ":p@" + pw + "@git.example.com/org/content.git",
		"percent-encoded":   "https://" + user + ":%70%77%64" + canaryCore + "@git.example.com/org/content.git",
		"query":             "https://git.example.com/org/content.git?" + qkey + "=" + qval,
		"query and frag":    "https://git.example.com/org/content.git?private_token=" + qval + "#" + frag,
		"fragment":          "https://git.example.com/org/content.git#" + frag,
		"ssh with password": "ssh://" + user + ":" + pw + "@git.example.com:2222/org/content.git",
		"ssh user":          "ssh://" + user + "@git.example.com/org/content.git",
		"file with user":    "file://" + user + ":" + pw + "@git.example.com/org/content.git",
		"uppercase scheme":  "HTTPS://" + user + ":" + pw + "@git.example.com/org/content.git",
	}
	unparsable := map[string]string{
		"bad escape": "https://" + user + ":" + pw + "@git.example.com/org/%zz?" + qkey + "#" + frag,
		"bad port":   "https://" + user + ":" + pw + "@git.example.com:port/org/content.git",
		// url.Parse takes "usr…" for a scheme and the rest as opaque, so a
		// redactor that trusts a successful parse leaks the password.
		"scp-like":           user + ":" + pw + "@git.example.com:org/content.git",
		"scp-like ssh":       "git@git.example.com:org/content.git?" + qval,
		"newline injection":  "https://" + user + ":" + pw + "@git.example.com/r.git\n\"}{\"level\":\"ERROR\",\"x\":\"" + pw + "\"}",
		"NUL byte":           "https://" + user + ":" + pw + "\x00@git.example.com/org/content.git",
		"bidi in password":   "https://" + user + ":" + pw + "\u202e@git.example.com/org/content.git",
		"zero-width in user": "https://" + user + "\u200b:" + pw + "@git.example.com/org/content.git",
		"space in host":      "https://" + user + ":" + pw + "@git example.com/org/content.git",
		"open bracket":       "https://" + user + ":" + pw + "@[::1/org/content.git",
		"no scheme":          "://" + user + ":" + pw + "@git.example.com/org/content.git?" + qval,
		"percent in user":    "https://" + user + "%:" + pw + "@git.example.com/org/content.git#" + frag,
	}
	secrets := []string{canaryCore, canaryCore[:12], canaryCore[len(canaryCore)-12:], "%70%77%64"}
	check := func(t *testing.T, raw string, keepsHost bool) {
		t.Helper()
		cfg, err := loadEnv(map[string]string{envGitURL: raw})
		if err != nil {
			if !strings.Contains(err.Error(), envGitURL) {
				t.Errorf("LoadConfig failed without naming %s: %v", envGitURL, err)
			}
			for _, s := range secrets {
				if strings.Contains(err.Error(), s) {
					t.Errorf("LoadConfig error echoes the URL's secret %q: %v", s, err)
				}
			}
		}
		keys, rec, out := reportKeys(t, cfg)
		if !contains(keys, envGitURL) {
			t.Fatalf("report lacks %s:\n%s", envGitURL, out)
		}
		for _, s := range secrets {
			if strings.Contains(out, s) {
				t.Errorf("report contains %q of the URL's userinfo, query or fragment:\n%s", s, out)
			}
		}
		got, _ := rec[envGitURL].(string)
		if got == "" {
			t.Errorf("report shows %s=%v for a set URL, want it redacted, not dropped:\n%s", envGitURL, rec[envGitURL], out)
		}
		if keepsHost && (!strings.Contains(got, "git.example.com") || !strings.Contains(got, "/org/content.git")) {
			t.Errorf("report shows %s=%q, want the host and path kept", envGitURL, got)
		}
	}
	for name, raw := range parsable {
		t.Run(name, func(t *testing.T) { check(t, raw, true) })
	}
	for name, raw := range unparsable {
		t.Run(name, func(t *testing.T) { check(t, raw, false) })
	}
}

// The GOLDIE_GIT_URL text states the maintainer's SSRF stance (option (a),
// 2026-11-23): the URL is operator-trusted, any https or ssh host is accepted,
// private ranges included, redirects are followed but drop credentials
// across origins and never go to http, and file:// needs
// GOLDIE_GIT_ALLOW_FILE_URL. The committed reference carries it (./run regen).
func TestGitURLTextStatesTheSSRFStance(t *testing.T) {
	wants := []string{"operator", "https", "ssh", "private", "redirect", "file://", envGitAllowFileURL}
	m := strings.ToLower(settingRow(t, envGitURL).Meaning)
	for _, want := range wants {
		if !strings.Contains(m, strings.ToLower(want)) {
			t.Errorf("%s description does not state %q: %q", envGitURL, want, settingRow(t, envGitURL).Meaning)
		}
	}
	var row string
	for _, line := range strings.Split(committedReference(t), "\n") {
		if strings.HasPrefix(line, "| `"+envGitURL+"` |") {
			row = line
		}
	}
	for _, want := range wants {
		if !strings.Contains(strings.ToLower(row), strings.ToLower(want)) {
			t.Errorf("committed reference row for %s does not state %q (run: ./run regen): %q", envGitURL, want, row)
		}
	}
}

// The PR adds the ADR recording the SSRF stance, listed in the ADR index. It
// says git Source URLs are operator-trusted, private ranges are accepted,
// redirects drop credentials when they leave the origin and never go to http,
// file:// needs GOLDIE_GIT_ALLOW_FILE_URL, and URLs from anyone else, such as
// Declarative Sources, need a new decision.
func TestADRRecordsTheGitSourceSSRFStance(t *testing.T) {
	const dir = "../../docs/adr"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var file, text string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "README.md" || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), envGitAllowFileURL) {
			file, text = e.Name(), string(b)
		}
	}
	if file == "" {
		t.Fatalf("no ADR in %s mentions %s: the PR must add the ADR recording the git Source SSRF stance", dir, envGitAllowFileURL)
	}
	lower := strings.ToLower(text)
	for _, want := range []string{"operator", "private", "redirect", "credential", "http", "file://", "declarative source"} {
		if !strings.Contains(lower, want) {
			t.Errorf("ADR %s does not state %q", file, want)
		}
	}
	index, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "("+file+")") {
		t.Errorf("docs/adr/README.md does not list %s", file)
	}
}
