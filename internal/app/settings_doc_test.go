package app

import (
	"fmt"
	"os"
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

// The GOLDIE_API_TOKENS description says, in one sentence, that a plaintext
// token must not start with sha256: (#159), and the committed configuration
// reference carries it (regenerated with ./run regen).
func TestAPITokensSettingSaysPlaintextMustNotStartWithSHA256(t *testing.T) {
	const rule = "must not start with sha256:"
	var tokens string
	for _, s := range platform.Settings {
		if s.Name == platform.EnvAPITokens {
			tokens = s.Meaning
		}
	}
	if tokens == "" {
		t.Fatalf("%s is not in platform.Settings", platform.EnvAPITokens)
	}
	var sentences []string
	for _, s := range strings.Split(tokens, ". ") {
		if strings.Contains(strings.ToLower(s), rule) {
			sentences = append(sentences, s)
		}
	}
	if len(sentences) != 1 {
		t.Fatalf("%s description %q: %d sentences say %q, want exactly one", platform.EnvAPITokens, tokens, len(sentences), rule)
	}
	if !strings.Contains(strings.ToLower(sentences[0]), "plaintext token") {
		t.Errorf("%s description: the sentence %q does not say it is about a plaintext token", platform.EnvAPITokens, sentences[0])
	}
	ref, err := os.ReadFile("../../docs/reference/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(ref), "\n") {
		if strings.HasPrefix(line, "| `"+platform.EnvAPITokens+"` |") {
			if !strings.Contains(strings.ToLower(line), rule) {
				t.Errorf("docs/reference/configuration.md does not say a plaintext token %s (run: ./run regen): %s", rule, line)
			}
			return
		}
	}
	t.Errorf("docs/reference/configuration.md has no %s row", platform.EnvAPITokens)
}
