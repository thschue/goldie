package platform

import (
	"strings"
	"testing"
)

// #636 fix round: LoadConfig is the one place that refuses an unsafe or missing
// GOLDIE_UPSTREAM_ADDR, so goldie health and the server agree.
func TestLoadConfigChecksTheUpstreamAddr(t *testing.T) {
	load := func(addr string, set bool) error {
		env := map[string]string{EnvMode: ModeUI}
		if set {
			env[EnvUpstreamAddr] = addr
		}
		_, err := LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
		return err
	}
	for _, ok := range []string{"https://goldie.example.com", "http://127.0.0.1:8080", "http://localhost:8080", "http://[::1]:8080"} {
		if err := load(ok, true); err != nil {
			t.Errorf("%q: %v, want it accepted", ok, err)
		}
	}
	for _, bad := range []string{"http://goldie.example.com", "http://10.0.0.5:8080", "https://user:pw@goldie.example.com", "ftp://goldie.example.com", "goldie.example.com"} {
		err := load(bad, true)
		if err == nil || !strings.Contains(err.Error(), EnvUpstreamAddr) || strings.Contains(err.Error(), "goldie.example.com") || strings.Contains(err.Error(), "pw") {
			t.Errorf("%q: err %v, want a refusal naming the variable and not the value", bad, err)
		}
	}
	err := load("", false)
	if err == nil || !strings.Contains(err.Error(), "for example http://127.0.0.1:8080") {
		t.Errorf("missing upstream: err %v, want it to say what to set", err)
	}
}
