package app

import (
	"fmt"
	"strings"

	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/platform"
)

// devGitURL is the repository goldie server --dev --git serves when
// GOLDIE_GIT_URL is not set. It is a variable only so a test binary can link
// another one: -X github.com/thschue/goldie/internal/app.devGitURL=<url>.
// Nothing reads it from the environment, a file or a flag.
var devGitURL = "https://github.com/thschue/goldie.git"

// devGitPath is the demo content in that repository.
const devGitPath = "examples/demo"

// withDevGit fills in the git Source goldie server --dev --git serves when
// GOLDIE_GIT_URL is not set: the demo content of the goldie repository at the
// release tag of this binary, so the content matches it, or at main for a
// build without a release version. A credential set without a URL is refused,
// so it is never sent to the built-in repository; the error names the variable
// and no value.
func withDevGit(cfg platform.Config) (platform.Config, error) {
	if cfg.GitURL != "" {
		return cfg, nil
	}
	for _, c := range []struct{ name, value string }{
		{platform.EnvGitToken, cfg.GitToken},
		{platform.EnvGitSSHKey, cfg.GitSSHKey},
		{platform.EnvGitSSHKnownHosts, cfg.GitSSHKnownHosts},
		{platform.EnvGitCABundle, cfg.GitCABundle},
	} {
		if c.value != "" {
			return cfg, &cli.UsageError{Command: "server", Msg: fmt.Sprintf("%s is set without %s: the built-in demo repository takes no credentials", c.name, platform.EnvGitURL)}
		}
	}
	cfg.GitURL, cfg.GitRef, cfg.GitPath = devGitURL, "main", devGitPath
	if v := platform.Version; v != "dev" {
		cfg.GitRef = "v" + strings.TrimPrefix(v, "v")
	}
	return cfg, nil
}
