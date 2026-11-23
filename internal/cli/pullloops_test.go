package cli

import (
	"slices"
	"testing"
)

func TestLoopInvocationsSkipsWhatIsNotAPlainEntry(t *testing.T) {
	entry := func(p, loop string) pulledFile {
		return pulledFile{path: ".claude/skills/goldie-loop-" + p + "/SKILL.md", data: []byte("---\nname: x\nloop: " + loop + "\n---\nbody\n")}
	}
	files := []pulledFile{
		entry("b", "30m"), entry("a", "dynamic"), entry("bad", "30m /evil"), entry("neg", "-5m"),
		{path: ".claude/skills/goldie-loop-x/notes.md", data: []byte("---\nloop: 1h\n---\n")},
		{path: ".claude/skills/goldie-other/SKILL.md", data: []byte("---\nloop: 1h\n---\n")},
	}
	want := []string{"/loop /goldie-loop-a", "/loop 30m /goldie-loop-b"}
	if got := loopInvocations(files); !slices.Equal(got, want) {
		t.Errorf("loopInvocations = %q, want %q", got, want)
	}
}
