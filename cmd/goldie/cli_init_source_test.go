package main

import (
	"path/filepath"
	"testing"
)

// TestCLIInitRefusesSourceRepositoryWithoutProject checks that a Source
// repository is refused as one even when nothing names a Project: no remote,
// no --project, no GOLDIE_PROJECT. The Source refusal comes before detection,
// so the user is not told to configure a Project for a repository that must
// not get one.
func TestCLIInitRefusesSourceRepositoryWithoutProject(t *testing.T) {
	for name, marker := range map[string]string{
		"global/articles": filepath.Join("global", "articles", "deploy.md"),
		"projects/":       filepath.Join("projects", "github.com", "acme", "shop", "articles", "deploy.md"),
	} {
		t.Run(name, func(t *testing.T) {
			root := gitRepo(t, "[core]\n\tbare = false\n")
			writeFile(t, filepath.Join(root, marker), "---\nname: x\n---\n")
			for _, dir := range []string{root, subdir(t, root, "docs")} {
				out, err := runInit(t, dir, nil)
				if err == nil || err.Error() != initErrSource {
					t.Errorf("from %s: err = %v, want %q", dir, err, initErrSource)
				}
				if out != "" {
					t.Errorf("printed %q, want nothing", out)
				}
				noGoldie(t, root)
				noGoldie(t, dir)
			}
		})
	}
}
