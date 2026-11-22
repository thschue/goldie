package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract/project"
)

// TestSourceErrorPrefixes pins the words in front of an unusable Project, as
// `goldie status` prints them, and that the value never reaches the error.
func TestSourceErrorPrefixes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".goldie"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".goldie", "config.yaml"), []byte("project: \"u:s3cret@x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, flag, env, dir, prefix string
	}{
		{"flag", "u:s3cret@x", "", t.TempDir(), "--project: "},
		{"env", "", "u:s3cret@x", t.TempDir(), "GOLDIE_PROJECT: "},
		{"config", "", "", dir, ".goldie/config.yaml: "},
	} {
		_, err := resolveProject(c.dir, c.flag, c.env)
		if err == nil || !strings.HasPrefix(err.Error(), c.prefix) || strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: error = %v, want prefix %q and no value", c.name, err, c.prefix)
		}
	}
}

// TestSourceWords pins each detection Source to the words `goldie status`
// prints and the JSON "projectSource" carries.
func TestSourceWords(t *testing.T) {
	want := map[project.Source]string{
		project.SourceFlag:     "--project",
		project.SourceEnv:      "GOLDIE_PROJECT",
		project.SourceConfig:   ".goldie/config.yaml",
		project.SourceUpstream: "git remote upstream",
		project.SourceOrigin:   "git remote origin",
	}
	for src, w := range want {
		if got := sourceWords[src]; got != w {
			t.Errorf("sourceWords[%q] = %q, want %q", src, got, w)
		}
	}
	if len(sourceWords) != len(want) {
		t.Errorf("sourceWords has %d entries, want %d", len(sourceWords), len(want))
	}
}
