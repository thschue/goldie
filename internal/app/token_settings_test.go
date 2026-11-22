package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// GOLDIE_API_TOKEN and GOLDIE_API_TOKENS together are ambiguous, as a variable
// and its _FILE form are: startup fails naming both, without the tokens.
func TestBothTokenVariablesFailStartup(t *testing.T) {
	_, err := PublicHandler(Options{ContentDir: testContent, APIToken: testToken, APITokens: namedTokenLines()})
	var cfgErr *ConfigError
	if err == nil || !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v, want a *ConfigError", err)
	}
	if !strings.Contains(err.Error(), platform.EnvAPIToken+" and "+platform.EnvAPITokens) {
		t.Errorf("err = %v, want it to name both variables", err)
	}
	for _, secret := range []string{testToken, readerToken, operatorToken} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error echoes a token: %v", err)
		}
	}
}

// With neither variable set, the error names both.
func TestNoTokenVariableNamesBoth(t *testing.T) {
	_, err := PublicHandler(Options{ContentDir: testContent})
	if err == nil || !strings.Contains(err.Error(), platform.EnvAPITokens) || !strings.Contains(err.Error(), platform.EnvAPIToken+" ") {
		t.Errorf("err = %v, want it to name %s and %s", err, platform.EnvAPITokens, platform.EnvAPIToken)
	}
}
