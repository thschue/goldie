package main

import (
	"strings"
	"testing"
)

// TestCLIStatusProjectProblemGolden pins the Project line and the additive JSON
// fields for a bad Project and for a rejected remote, next to status_project.
func TestCLIStatusProjectProblemGolden(t *testing.T) {
	srv := cliServer(t)
	base := []string{"--addr", srv.URL, "--token", cliToken}
	rejected := func(t *testing.T) string {
		return gitRepo(t, remoteCfg("origin", "https://git.internal.example/grp/sub/repo.git"))
	}
	plain := func(t *testing.T) string { return t.TempDir() }
	cases := []struct {
		name string
		dir  func(t *testing.T) string
		args []string
	}{
		{"status_project_problem", plain, []string{"status", "--project", "just-a-name"}},
		{"status_project_problem_json", plain, []string{"status", "--json", "--project", "just-a-name"}},
		{"status_project_rejected_remote", rejected, []string{"status"}},
		{"status_project_rejected_remote_json", rejected, []string{"status", "--json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := runCLIIn(t, c.dir(t), append(c.args, base...)...)
			if err != nil {
				t.Fatalf("run %v: %v", c.args, err)
			}
			for _, leak := range []string{"git.internal.example", "grp/sub", "just-a-name"} {
				if strings.Contains(out, leak) {
					t.Errorf("output leaks %q:\n%s", leak, out)
				}
			}
			golden(t, c.name, out, srv.URL)
		})
	}
}
