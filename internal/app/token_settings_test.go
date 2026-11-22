package app

import (
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// GOLDIE_API_TOKEN and GOLDIE_API_TOKENS together are ambiguous, as a variable
// and its _FILE form are: startup fails naming both, without the tokens. The
// check is LoadConfig's (#186), whose errors the server reports as invalid
// configuration before it builds the public handler.
func TestBothTokenVariablesFailStartup(t *testing.T) {
	env := map[string]string{platform.EnvContentDir: testContent, platform.EnvAPIToken: testToken, platform.EnvAPITokens: namedTokenLines()}
	_, err := platform.LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err == nil {
		t.Fatal("LoadConfig accepted both token variables")
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
	_, err := PublicHandler(platform.Config{ContentDir: testContent, RateLimit: platform.DefaultRateLimit()}, nil)
	if err == nil || !strings.Contains(err.Error(), platform.EnvAPITokens) || !strings.Contains(err.Error(), platform.EnvAPIToken+" ") {
		t.Errorf("err = %v, want it to name %s and %s", err, platform.EnvAPITokens, platform.EnvAPIToken)
	}
}
