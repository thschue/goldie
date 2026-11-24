package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/platform"
)

// A credential set without GOLDIE_GIT_URL is refused before anything starts,
// so it is never sent to the built-in demo repository; the error names the
// variable and not its value.
func TestLocalGitRefusesACredentialWithoutAURL(t *testing.T) {
	const secret = "s3cr3t-value"
	for _, c := range []struct {
		name string
		set  func(*platform.Config)
	}{
		{platform.EnvGitToken, func(c *platform.Config) { c.GitToken = secret }},
		{platform.EnvGitSSHKey, func(c *platform.Config) { c.GitSSHKey = secret }},
		{platform.EnvGitSSHKnownHosts, func(c *platform.Config) { c.GitSSHKnownHosts = secret }},
		{platform.EnvGitCABundle, func(c *platform.Config) { c.GitCABundle = secret }},
	} {
		t.Run(c.name, func(t *testing.T) {
			var cfg platform.Config
			c.set(&cfg)
			_, _, _, err := Local{Config: cfg, Git: true}.Start(context.Background())
			var ue *cli.UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v, want a usage error", err)
			}
			if want := c.name + " is set without GOLDIE_GIT_URL"; !strings.Contains(err.Error(), want) {
				t.Errorf("err = %q, want it to contain %q", err, want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("err = %q holds the value", err)
			}
		})
	}
}
