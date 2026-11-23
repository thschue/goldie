package main

import (
	"strings"
	"testing"
)

// An address next to .goldie/articles/ is an error that names the setting, as
// an address next to a directory is; an explicit --dir still wins over the
// GOLDIE_ADDR of the environment, as before. GOLDIE_DIR next to config.yaml's
// dir takes precedence over it.
func TestCLILocalScopeWithAnAddressIsAConflict(t *testing.T) {
	root := lsRepo(t, map[string]string{".goldie/articles/team/only-local.md": lsOnlyLocalItem})
	for name, c := range map[string]struct {
		env  map[string]string
		args []string
		want string
	}{
		"env":  {map[string]string{"GOLDIE_ADDR": "http://127.0.0.1:1"}, nil, "GOLDIE_ADDR"},
		"flag": {nil, []string{"--addr", "http://127.0.0.1:1"}, "--addr"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := lsRun(t, root, c.env, append([]string{"lookup", "team/only-local"}, c.args...)...)
			if err == nil || !strings.Contains(err.Error(), c.want) || out != "" {
				t.Errorf("err = %v, out = %q, want an error naming %s", err, out, c.want)
			}
		})
	}
}

func TestCLILocalScopeEnvDirReplacesConfigDir(t *testing.T) {
	root := lsRepo(t, map[string]string{
		".goldie/articles/team/only-local.md": lsOnlyLocalItem,
		".goldie/config.yaml":                 "dir: nowhere\n",
	})
	out := lsMustRun(t, root, map[string]string{"GOLDIE_DIR": lsContentDir}, "lookup", "agent-loops")
	if !strings.Contains(out, "agent-loops") {
		t.Errorf("lookup of a global Item over GOLDIE_DIR:\n%s", out)
	}
}
