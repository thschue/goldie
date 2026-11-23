package cli

import (
	"path"
	"strings"
	"testing"
)

// FuzzInNamespace checks that an accepted archive entry name is always a clean,
// relative path inside goldie's namespace with no ".." element.
func FuzzInNamespace(f *testing.F) {
	for _, s := range []string{
		".claude/agents/goldie-a.md", ".claude/skills/goldie-s/run.sh", "/etc/passwd",
		".claude/skills/goldie-s/../../../x", "CLAUDE.md", ".claude/skills/mine/SKILL.md",
		".claude/skills/goldie-s/a\\b", ".claude/skills/goldie-s/a\u202eb", ".claude/skills/goldie-/x",
		".claude/agents/goldie-a.md/x", ".claude/skills/goldie-s/", "", "..", ".claude/skills/goldie-s/..",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if !inNamespace(name) {
			return
		}
		if path.Clean(name) != name || path.IsAbs(name) || strings.Contains(name, "\\") {
			t.Fatalf("accepted unclean name %q", name)
		}
		if !strings.HasPrefix(name, ".claude/skills/goldie-") && !strings.HasPrefix(name, ".claude/agents/goldie-") {
			t.Fatalf("accepted %q outside the namespace", name)
		}
		for _, el := range strings.Split(name, "/") {
			if el == ".." {
				t.Fatalf("accepted %q with a .. element", name)
			}
		}
	})
}
