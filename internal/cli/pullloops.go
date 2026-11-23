package cli

import (
	"bytes"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/thschue/goldie/internal/contract"
)

const loopEntryDir = ".claude/skills/" + goldiePrefix + "loop-"

// loopInvocations returns the /loop line of each loop entry skill among files,
// sorted by name. The server wrote the entry skill; its frontmatter key loop
// holds the interval or "dynamic". The archive is untrusted: a name or interval
// that is not plain, or an entry that does not parse, gives no line.
func loopInvocations(files []pulledFile) []string {
	var names []string
	lines := map[string]string{}
	for _, f := range files {
		name, ok := strings.CutPrefix(f.path, loopEntryDir)
		if !ok {
			continue
		}
		name, ok = strings.CutSuffix(name, "/SKILL.md")
		if !ok || name == "" || strings.Contains(name, "/") || contract.CleanLine(name) != name {
			continue
		}
		front, _, ok := bytes.Cut(bytes.TrimPrefix(f.data, []byte("---\n")), []byte("\n---\n"))
		if !ok || !bytes.HasPrefix(f.data, []byte("---\n")) {
			continue
		}
		var meta struct {
			Loop string `yaml:"loop"`
		}
		if yaml.Unmarshal(front, &meta) != nil {
			continue
		}
		line := "/loop /goldie-loop-" + name
		if meta.Loop != "dynamic" {
			if d, err := time.ParseDuration(meta.Loop); err != nil || d <= 0 || contract.CleanLine(meta.Loop) != meta.Loop || strings.ContainsAny(meta.Loop, " ") {
				continue
			}
			line = "/loop " + meta.Loop + " /goldie-loop-" + name
		}
		names = append(names, name)
		lines[name] = line
	}
	slices.Sort(names)
	var out []string
	for _, n := range names {
		out = append(out, lines[n])
	}
	return out
}
