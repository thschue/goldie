package cli

import (
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/thschue/goldie/internal/contract"
)

var loopLineRE = regexp.MustCompile(`^/loop (\S+ )?/goldie-loop-(\S+)$`)

// FuzzLoopInvocations checks that every printed line is /loop [<duration> ]
// /goldie-loop-<name> with no hidden or whitespace rune in the name.
func FuzzLoopInvocations(f *testing.F) {
	f.Add("a", "---\nloop: 30m\n---\n")
	f.Add("a b", "---\nloop: dynamic\n---\n")
	f.Add("a"+string(rune(0x202e))+"b", "---\nloop: 1h\n---\n")
	f.Add("x", "---\nloop: \"30m /evil\"\n---\n")
	f.Fuzz(func(t *testing.T, name, data string) {
		files := []pulledFile{{path: ".claude/skills/goldie-loop-" + name + "/SKILL.md", data: []byte(data)}}
		for _, l := range loopInvocations(files) {
			m := loopLineRE.FindStringSubmatch(l)
			if m == nil {
				t.Fatalf("line %q is not a /loop invocation", l)
			}
			if contract.CleanLine(m[2]) != m[2] || strings.ContainsFunc(m[2], unicode.IsSpace) || m[2] != name {
				t.Fatalf("line %q has an unsafe name", l)
			}
		}
	})
}
