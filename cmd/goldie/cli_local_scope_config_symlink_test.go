package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A .goldie/config.yaml that is a symlink is refused, wherever it points: the
// CLI reads its dir and the engine its suppress list, and neither may follow
// the link (the engine refuses it, #389). The command fails on lookup, search
// and status, prints nothing, names .goldie/config.yaml relative to the root,
// and leaks no absolute path, symlink target or content from the target. The
// CLI and the engine agree: an in-root link is refused too, so the user gets
// the engine's error, not a silent difference.
func TestCLILocalScopeRefusesAConfigYamlThatIsASymlink(t *testing.T) {
	outside := lsOutside(t)
	lsWrite(t, outside, map[string]string{"config.yaml": "dir: content\n# " + lsOutsideSecret + "\n"})
	outsideLink := func(string) string { return filepath.Join(outside, "config.yaml") }
	insideRoot := func(root string) string { return filepath.Join(root, "shared", "config.yaml") }
	for name, c := range map[string]struct {
		files map[string]string
		link  func(root string) (target string)
		args  []string // added to every command
	}{
		"to a file outside the root, with a dir": {nil, outsideLink, nil},
		"to a file outside the root, with --dir": {nil, outsideLink, []string{"--dir", lsContentDir}},
		"to a file inside the root, with a dir": {
			map[string]string{"shared/config.yaml": "dir: content\n"}, insideRoot, nil},
		"to a file inside .goldie, with a dir": {
			map[string]string{".goldie/shared.yaml": "dir: content\n"}, func(string) string { return "shared.yaml" }, nil},
		"to a file inside the root, with --dir": {
			map[string]string{"shared/config.yaml": "suppress: []\n"}, insideRoot, []string{"--dir", lsContentDir}},
		"dangling": {nil, func(string) string { return "missing.yaml" }, nil},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{".goldie/articles/team/only-local.md": lsOnlyLocalItem}
			for k, v := range c.files {
				files[k] = v
			}
			root := lsRepo(t, files)
			lsCopyContent(t, root, "content")
			target := c.link(root)
			if err := os.Symlink(target, filepath.Join(root, ".goldie", "config.yaml")); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
			for _, cmd := range [][]string{{"lookup", "team/only-local"}, {"search", "onlylocal"}, {"status"}} {
				out, err := lsRun(t, root, nil, append(append([]string{}, cmd...), c.args...)...)
				if err == nil {
					t.Fatalf("goldie %s succeeded with a symlinked config.yaml:\n%s", strings.Join(cmd, " "), out)
				}
				if out != "" {
					t.Errorf("output on failure: %q", out)
				}
				msg := err.Error()
				if !strings.Contains(msg, ".goldie/config.yaml") {
					t.Errorf("error %q does not name .goldie/config.yaml", msg)
				}
				lsNoLeak(t, msg, append(lsRealPaths(t, root), outside, target)...)
				if strings.Contains(msg, lsOutsideSecret) {
					t.Errorf("error carries content from the link target: %q", msg)
				}
			}
		})
	}
}
