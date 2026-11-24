package main

import (
	"strings"
	"testing"
)

// Fix round for #739: what the user sees when the decisions folder is not
// quite right.

// A malformed ADR beside a good one is not served, and the command says so on
// stderr, naming the file.
func TestCLIMalformedADRIsNamedOnStderr(t *testing.T) {
	files := ldADRs()
	files["docs/adr/0002-bad.md"] = "# 2. Bad one\n\n## Status\n\nAccepted\n\n## Decision\n\nNo date here.\n"
	root := lsRepo(t, files)
	stdout, stderr, code := runValidate(t, root, validateEnv(), "decisions", "list")
	if code != 0 {
		t.Fatalf("decisions list: exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "0002-bad") {
		t.Errorf("the malformed ADR is listed:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Warning: docs/adr/0002-bad.md: ") {
		t.Errorf("stderr does not name docs/adr/0002-bad.md:\n%s", stderr)
	}
	lsNoLeak(t, strings.TrimSpace(stderr), lsRealPaths(t, root)...)
}

// A folder of valid ADRs is quiet.
func TestCLIValidADRsWriteNothingToStderr(t *testing.T) {
	root := lsRepo(t, ldADRs())
	_, stderr, code := runValidate(t, root, validateEnv(), "decisions", "list")
	if code != 0 || stderr != "" {
		t.Errorf("exit %d, stderr %q, want 0 and nothing", code, stderr)
	}
}

// A folder with Markdown files of which none reads as a decision says why the
// list is empty.
func TestCLIFolderWithNoDecisionsExplainsItself(t *testing.T) {
	root := lsRepo(t, map[string]string{
		".goldie/config.yaml": ldConfig,
		"docs/adr/README.md":  ldReadme,
		"docs/adr/notes.md":   "# Notes\n\nNo status.\n",
	})
	_, stderr, code := runValidate(t, root, validateEnv(), "decisions", "list")
	if code != 0 || !strings.Contains(stderr, "none read as a decision") {
		t.Errorf("exit %d, stderr %q, want one that says none read as a decision", code, stderr)
	}
}

// A misspelt key under decisions is an error, not a repository that silently
// has no local Scope.
func TestCLIMisspeltDecisionsKeyIsAnError(t *testing.T) {
	root := lsRepo(t, map[string]string{
		".goldie/config.yaml":           "decisions:\n  paht: docs/adr\n",
		"docs/adr/0002-use-postgres.md": ldMADR,
	})
	out, err := lsRun(t, root, nil, "decisions", "list")
	if err == nil || !strings.Contains(err.Error(), ".goldie/config.yaml") {
		t.Errorf("decisions list: %q, %v, want an error naming .goldie/config.yaml", out, err)
	}
}

// A path that is well formed but names no folder says so in its own words.
func TestCLIMissingDecisionsFolderSaysItIsNotAFolder(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"missing": {"docs/other.txt": "x"},
		"a file":  {"docs/adr": "not a folder"},
	} {
		t.Run(name, func(t *testing.T) {
			files[".goldie/config.yaml"] = ldConfig
			root := lsRepo(t, files)
			_, err := lsRun(t, root, nil, "decisions", "list")
			if err == nil || !strings.Contains(err.Error(), ".goldie/config.yaml") || !strings.Contains(err.Error(), "is not a folder in this repository") {
				t.Errorf("error = %v, want one naming .goldie/config.yaml and saying it is not a folder", err)
			}
		})
	}
}
