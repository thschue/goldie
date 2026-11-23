package goldie_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Acceptance tests for #503: the first ./run regen on a checkout without
// web/node_modules runs a slow npm ci. It must say so first, as cmd_ui does
// before its install, so the pause is not mistaken for a hang.
//
// ./run is copied into a scratch repository with stand-in go, oapi-codegen,
// npm and npx, so no network or Node is needed and the order of the output
// is observable.

const npmCiMarker = "STUB-NPM-CI-RUNNING"

// regenInScratch runs ./run <task> in a scratch root. With nodeModules the
// scratch checkout already has web/node_modules/openapi-typescript.
func regenInScratch(t *testing.T, task string, nodeModules bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("./run is a POSIX shell script")
	}
	script, err := os.ReadFile("run")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "run"), script, 0o700); err != nil { //nolint:gosec // an executable copy of ./run
		t.Fatal(err)
	}
	for _, d := range []string{"internal/contract", "docs/reference", "web/src/api", "testdata/openapi"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if nodeModules {
		if err := os.MkdirAll(filepath.Join(root, "web/node_modules/openapi-typescript"), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	base := t.TempDir()
	bin, tools := filepath.Join(base, "bin"), filepath.Join(base, "tools")
	writeExec(t, filepath.Join(bin, "go"), "exit 0")
	writeExec(t, filepath.Join(bin, "npx"), "exit 0")
	writeExec(t, filepath.Join(bin, "npm"), "echo "+npmCiMarker+" \"$@\"\nmkdir -p node_modules/openapi-typescript")
	writeExec(t, filepath.Join(tools, "oapi-codegen-v2.9.0", "oapi-codegen"), "exit 0")

	cmd := exec.Command("./run", task) //nolint:gosec // a copy of the repository's own script
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TMPDIR="+t.TempDir(),
		"GOLDIE_TOOLS="+tools,
	)
	out := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = out, out
	_ = cmd.Run() // the exit status is not under test here; the output order is
	return out.String()
}

func TestRunRegenSaysItIsInstallingBeforeNpmCiRuns(t *testing.T) {
	for _, task := range []string{"regen", "regen-ts-check"} {
		t.Run(task, func(t *testing.T) {
			out := regenInScratch(t, task, false)
			at := strings.Index(out, npmCiMarker)
			if at < 0 {
				t.Fatalf("npm ci never ran without web/node_modules:\n%s", out)
			}
			if !strings.Contains(out[at:], "ci --ignore-scripts") {
				t.Errorf("the install is not npm ci --ignore-scripts:\n%s", out)
			}
			var said bool
			for _, line := range strings.Split(out[:at], "\n") {
				if strings.HasPrefix(line, "==> ") && strings.Contains(line, "npm ci") {
					said = true
				}
			}
			if !said {
				t.Errorf("no \"==> ... npm ci ...\" line before npm ci runs, as cmd_ui prints:\n%s", out)
			}
		})
	}
}

func TestRunRegenStaysQuietAboutInstallingWhenDependenciesExist(t *testing.T) {
	out := regenInScratch(t, "regen", true)
	if strings.Contains(out, npmCiMarker) {
		t.Errorf("npm ci ran although web/node_modules/openapi-typescript exists:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "==> ") && strings.Contains(line, "npm ci") {
			t.Errorf("announces an install that does not happen: %q", line)
		}
	}
}

// The announcement names the task that runs, not the spec path.
func TestRunRegenInstallLineNamesTheTask(t *testing.T) {
	for _, task := range []string{"regen", "regen-ts-check"} {
		t.Run(task, func(t *testing.T) {
			out := regenInScratch(t, task, false)
			want := "==> " + task + ": npm ci"
			var found bool
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, want) {
					found = true
				}
			}
			if !found {
				t.Errorf("no line starting with %q:\n%s", want, out)
			}
		})
	}
}
