package cli

import (
	"strings"

	"github.com/thschue/goldie/internal/platform"
)

// devGitURL is the repository goldie server --dev --git serves when
// GOLDIE_GIT_URL is not set. It is a variable only so a test binary can link
// another one: -X github.com/thschue/goldie/internal/cli.devGitURL=<url>.
// Nothing reads it from the environment, a file or a flag.
var devGitURL = "https://github.com/thschue/goldie.git"

// devGitPath is the demo content in that repository.
const devGitPath = "examples/demo"

// DevGit is the git Source goldie server --dev --git serves when
// GOLDIE_GIT_URL is not set: the demo content of the goldie repository at the
// release tag of this binary, so the content matches it, or at main for a
// build without a release version.
func DevGit() (url, ref, path string) {
	ref = "main"
	if v := platform.Version; v != "dev" {
		ref = "v" + strings.TrimPrefix(v, "v")
	}
	return devGitURL, ref, devGitPath
}
