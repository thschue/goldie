package github_test

import (
	"testing"

	"github.com/thschue/goldie/internal/api/github"
)

func TestNewRefusesPlainHTTPUnlessTheHostIsLoopback(t *testing.T) {
	opts := github.Options{ClientID: "id", ClientSecret: "s", Orgs: []string{"acme"}}
	for base, ok := range map[string]bool{
		"http://ghe.example":      false,
		"http://10.0.0.5":         false,
		"https://ghe.example":     true,
		"http://127.0.0.1:8080":   true,
		"http://localhost:8080":   true,
		"http://[::1]:8080":       true,
		"http://localhost.evil.x": false,
	} {
		opts.BaseURL = base
		_, err := github.New(opts)
		if (err == nil) != ok {
			t.Errorf("New with base %q: err = %v, want success = %v", base, err, ok)
		}
	}
}
