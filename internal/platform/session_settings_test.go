package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Browser sessions (#488): the cookie's signing key is read from
// GOLDIE_SESSION_KEY or GOLDIE_SESSION_KEY_FILE (PC-05), is a secret the
// startup report never prints (PC-06), and is documented with the session
// cookie as a credential and the decision that made it stateless.

const (
	envSessionKey = "GOLDIE_SESSION_KEY"
	sessionSecret = "session-key-0123456789abcdef0123456789abcdef"
)

func TestSessionKeyIsASecretSettingInTheRegistry(t *testing.T) {
	row := settingRow(t, envSessionKey)
	if !secretOf(t, row) {
		t.Errorf("%s is not marked Secret: the report would print the signing key", envSessionKey)
	}
	if !strings.Contains(row.Meaning, "random") {
		t.Errorf("%s description does not say a random per-process key is used when unset: %q", envSessionKey, row.Meaning)
	}
}

func TestSessionKeyIsReadFromTheVariableOrItsFile(t *testing.T) {
	cfg, err := loadEnv(map[string]string{envSessionKey: sessionSecret})
	if err != nil {
		t.Fatal(err)
	}
	if got := effective(t, cfg, envSessionKey); got != sessionSecret {
		t.Errorf("from the variable: %v, want the key", got)
	}

	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(sessionSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadEnv(map[string]string{envSessionKey + "_FILE": path})
	if err != nil {
		t.Fatal(err)
	}
	if got := effective(t, cfg, envSessionKey); got != sessionSecret {
		t.Errorf("from the file: %v, want the key without its newline", got)
	}

	cfg, err = loadEnv(nil)
	if err != nil {
		t.Fatalf("an unset key is not an error (a random one is used): %v", err)
	}
	if got := effective(t, cfg, envSessionKey); got != "" {
		t.Errorf("unset: %v, want empty", got)
	}
}

func TestSessionKeyBothFormsOrAnUnreadableFileIsAnErrorThatNeverEchoesTheKey(t *testing.T) {
	_, err := loadEnv(map[string]string{envSessionKey: sessionSecret, envSessionKey + "_FILE": "/run/secrets/key"})
	if err == nil || !strings.Contains(err.Error(), envSessionKey) || strings.Contains(err.Error(), sessionSecret) {
		t.Errorf("both set: err = %v, want one naming %s and not the key", err, envSessionKey)
	}
	_, err = loadEnv(map[string]string{envSessionKey + "_FILE": "/nope/key"})
	if err == nil || !strings.Contains(err.Error(), envSessionKey) || strings.Contains(err.Error(), "/nope/key") {
		t.Errorf("unreadable file: err = %v, want one naming %s and not the path", err, envSessionKey)
	}
}

func TestStartupReportNeverPrintsTheSessionKey(t *testing.T) {
	cfg, err := loadEnv(map[string]string{envSessionKey: sessionSecret})
	if err != nil {
		t.Fatal(err)
	}
	if got := reported(t, cfg, envSessionKey); strings.Contains(got, sessionSecret) {
		t.Errorf("the startup report prints the session key: %q", got)
	}
}

func TestConfigurationReferenceNamesTheSessionKeyVariables(t *testing.T) {
	ref := committedReference(t)
	for _, name := range []string{envSessionKey, envSessionKey + "_FILE"} {
		if !strings.Contains(ref, name) {
			t.Errorf("docs/reference/configuration.md does not name %s (run: ./run regen)", name)
		}
	}
	if !strings.Contains(ref, "| `"+envSessionKey+"` |") {
		t.Errorf("docs/reference/configuration.md has no table row for %s", envSessionKey)
	}
}

func TestPlatformContractSaysTheSessionCookieIsACredential(t *testing.T) {
	b, err := os.ReadFile("../../docs/platform-contract.md")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(string(b)), " ")
	if strings.Contains(flat, "no other header, query parameter or cookie is a credential") {
		t.Error("docs/platform-contract.md still says no cookie is a credential")
	}
	if !strings.Contains(strings.ToLower(flat), "session cookie is a credential") {
		t.Error("docs/platform-contract.md does not say the session cookie is a credential")
	}
}

func TestADR0027RecordsStatelessSignedBrowserSessions(t *testing.T) {
	matches, err := filepath.Glob("../../docs/adr/0027-*.md")
	if err != nil || len(matches) != 1 {
		t.Fatalf("want exactly one docs/adr/0027-*.md, got %v (%v)", matches, err)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(b))
	for _, want := range []string{"hmac", "stateless", "session store", "rotat", "token name"} {
		if !strings.Contains(text, want) {
			t.Errorf("%s does not mention %q", filepath.Base(matches[0]), want)
		}
	}
}
