package main

import (
	"os"
	"strings"
	"testing"
)

// #799: the README quickstart and the design docs describe the guided init
// flow, and the commands init prints in its Next block are kept in step with
// the checklist of docs/design/ui-handoff.md, with a comment on both sides.
func TestGuidedInitIsDocumentedAndKeptInStep(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	readme := read("../../README.md")
	for _, w := range []string{"export GOLDIE_ADDR=", "claude mcp add --transport http goldie", "goldie instructions apply"} {
		if !strings.Contains(readme, w) {
			t.Errorf("README.md does not show %q", w)
		}
	}

	exp := read("../../docs/design/cli-and-agent-experience.md")
	_, step, ok := strings.Cut(exp, "5. `goldie init`")
	if !ok {
		t.Fatal("§2.4 has no step 5 for goldie init")
	}
	step, _, _ = strings.Cut(step, "\n6. ")
	if !strings.Contains(step, "Next") || !strings.Contains(strings.ToLower(step), "terminal") {
		t.Errorf("§2.4 step 5 does not describe the guided flow (prompts on a terminal, a Next block): %q", step)
	}

	handoff := read("../../docs/design/ui-handoff.md")
	_, item, ok := strings.Cut(handoff, "**Put your agent on the golden path**")
	if !ok {
		t.Fatal("ui-handoff.md §3.2 has no item 4")
	}
	item, _, _ = strings.Cut(item, "### 3.3")
	if !strings.Contains(item, "keep in step") || !strings.Contains(item, "internal/cli/init.go") {
		t.Errorf("ui-handoff.md §3.2 item 4 has no \"keep in step\" comment naming internal/cli/init.go: %q", item)
	}
	if src := read("../../internal/cli/init.go"); !strings.Contains(src, "keep in step") || !strings.Contains(src, "ui-handoff.md") {
		t.Error("internal/cli/init.go has no \"keep in step\" comment naming docs/design/ui-handoff.md")
	}
}
