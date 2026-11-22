package cli

import (
	"testing"

	"github.com/thschue/goldie/internal/contract/project"
)

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
