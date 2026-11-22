package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/platform"
)

// The GOLDIE_API_TOKEN description in the settings registry, and so the
// generated configuration reference, states the minimum CheckToken enforces.
// platform cannot import api, so this is where the two are held together.
func TestAPITokenSettingStatesMinimumLength(t *testing.T) {
	want := fmt.Sprintf("at least %d bytes", api.MinTokenLength)
	for _, s := range platform.Settings {
		if s.Name != platform.EnvAPIToken {
			continue
		}
		if !strings.Contains(s.Meaning, want) {
			t.Errorf("%s description %q does not state %q", s.Name, s.Meaning, want)
		}
		return
	}
	t.Fatalf("%s is not in platform.Settings", platform.EnvAPIToken)
}

// The GOLDIE_API_TOKENS description, and so the generated configuration
// reference, gives the line formats, recommends the hashed form, states the
// minimum for plaintext tokens and the strength required of the token behind
// a hash. GOLDIE_API_TOKEN says it is the shorthand for one operator token.
func TestAPITokensSettingDocumentsFormatsHashingAndStrength(t *testing.T) {
	meaning := map[string]string{}
	for _, s := range platform.Settings {
		meaning[s.Name] = s.Meaning
	}
	tokens, ok := meaning["GOLDIE_API_TOKENS"]
	if !ok {
		t.Fatal("GOLDIE_API_TOKENS is not in platform.Settings")
	}
	for _, want := range []string{
		"name:role:token",
		"name:role:sha256:",
		"reader",
		"operator",
		"recommend",
		fmt.Sprintf("at least %d bytes", api.MinTokenLength),
		"openssl rand -hex 32",
		"GOLDIE_API_TOKENS_FILE",
	} {
		if !strings.Contains(tokens, want) {
			t.Errorf("GOLDIE_API_TOKENS description %q does not state %q", tokens, want)
		}
	}
	if !strings.Contains(meaning[platform.EnvAPIToken], "operator") {
		t.Errorf("%s description %q does not say it is an operator token", platform.EnvAPIToken, meaning[platform.EnvAPIToken])
	}
}
