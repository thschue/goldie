package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"slices"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// loopEntryPrefix follows goldiePrefix in the directory of a loop entry skill.
const loopEntryPrefix = "loop-"

// loopEntry is the entry skill of l: a skill named goldie-loop-<name> whose
// description is the loop's and whose body is the iteration prompt and the stop
// condition. Its frontmatter key loop holds the interval, or "dynamic", which
// is how goldie pull prints the /loop invocation without asking the server
// again. The description is the only field loaded unchecked, so it is cleaned
// here; the prompt and the stop condition were checked when the loop loaded,
// and the interval is a parsed duration.
func loopEntry(l Loop) []byte {
	timing := l.Interval
	if l.Dynamic {
		timing = "dynamic"
	}
	front, err := yaml.Marshal(struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Loop        string `yaml:"loop"`
	}{goldiePrefix + loopEntryPrefix + l.Name, cleanText(l.Description), timing})
	if err != nil {
		return nil
	}
	return []byte("---\n" + string(front) + "---\n" + l.Content + "\n## Stop\n\nStop " + l.Stop + ".\n")
}

// cleanText is s on one line, without any rune HiddenRune names.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t' || r == 0x2028 || r == 0x2029:
			return ' '
		case HiddenRune(r):
			return -1
		}
		return r
	}, s)
}

// BundleFile is one file of a bundle: a clean relative slash path from the
// repository root, and its bytes as written.
type BundleFile struct {
	Path string
	// Exec is set when the file carries an executable bit.
	Exec bool
	Data []byte
}

// ValidBundleTarget reports whether t is a layout Bundle can lay out. It is
// narrower than ValidTarget, which names the instruction files.
func ValidBundleTarget(t string) bool { _, ok := bundleLayouts[t]; return ok }

// bundleLayout lays the resolved skills and agents out for one target, in no
// particular order. It is the seam a further target plugs into.
type bundleLayout func(skills []skillBundle, agents []Agent, loops []Loop) []BundleFile

var bundleLayouts = map[string]bundleLayout{TargetClaude: claudeLayout}

// goldiePrefix starts every name in the Goldie namespace, so a pull owns
// exactly these and nothing else.
const goldiePrefix = "goldie-"

// claudeLayout is the Claude target: .claude/skills/goldie-<name>/<file>,
// .claude/agents/goldie-<name>.md and, for each loop, its entry skill
// .claude/skills/goldie-loop-<name>/SKILL.md (see loopEntry). A loop whose entry
// path a skill already holds is left out.
func claudeLayout(skills []skillBundle, agents []Agent, loops []Loop) []BundleFile {
	var out []BundleFile
	for _, sk := range skills {
		if !safeElem(sk.name) {
			continue
		}
		for f, data := range sk.files {
			if safeRel(f) {
				out = append(out, BundleFile{Path: ".claude/skills/" + goldiePrefix + sk.name + "/" + f, Exec: sk.exec[f], Data: data})
			}
		}
	}
	for _, a := range agents {
		if safeElem(a.Name) {
			out = append(out, BundleFile{Path: ".claude/agents/" + goldiePrefix + a.Name + ".md", Data: a.Raw})
		}
	}
	taken := map[string]bool{}
	for _, f := range out {
		taken[f.Path] = true
	}
	for _, l := range loops {
		p := ".claude/skills/" + goldiePrefix + loopEntryPrefix + l.Name + "/SKILL.md"
		if safeElem(l.Name) && !taken[p] {
			out = append(out, BundleFile{Path: p, Data: loopEntry(l)})
		}
	}
	return out
}

// safeElem reports whether s is a plain path element: not empty, not . or ..,
// at most 200 bytes, and only printable characters without a slash or
// backslash. Hidden and bidirectional characters are not printable, so they
// never reach a file name. It does not reject names or characters reserved on
// Windows; the writer of the pull, which checks every entry again, is the
// control.
func safeElem(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 200 {
		return false
	}
	for _, r := range s {
		if r == '/' || r == '\\' || !unicode.IsGraphic(r) {
			return false
		}
	}
	return true
}

// safeRel reports whether every element of the slash path p is a safeElem.
func safeRel(p string) bool {
	for _, e := range strings.Split(p, "/") {
		if !safeElem(e) {
			return false
		}
	}
	return true
}

// lenHash feeds a hash length-prefixed parts, so two sequences of parts hash
// alike only when they are the same parts.
type lenHash struct{ hash.Hash }

func newLenHash(h hash.Hash) lenHash { return lenHash{h} }

func (h lenHash) put(b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	h.Write(n[:])
	h.Write(b)
}

func (h lenHash) putBool(v bool) {
	if v {
		h.put([]byte{1})
	} else {
		h.put([]byte{0})
	}
}

// Bundle returns the files of the resolved set in proj's view, laid out for
// target (a ValidBundleTarget), sorted by path, and the digest of the set: the
// hex SHA-256 over the target and every path, executable bit and content. The
// same set gives the same digest, whatever order it was loaded in. Drafts are
// left out, as in Skills and Agents. A skill or agent whose name, or a skill
// file whose path, cannot be a safe path element is left out too.
func (s *Snapshot) Bundle(proj, target string) ([]BundleFile, string) {
	layout, ok := bundleLayouts[target]
	if !ok {
		return nil, ""
	}
	var skills []skillBundle
	for _, sk := range s.Skills(proj) {
		if b, ok := s.skillBundleFor(proj, sk.Name, ReadOptions{}); ok {
			skills = append(skills, b)
		}
	}
	files := layout(skills, s.Agents(proj), s.Loops(proj))
	slices.SortFunc(files, func(a, b BundleFile) int { return strings.Compare(a.Path, b.Path) })
	h := newLenHash(sha256.New())
	h.put([]byte(target))
	for _, f := range files {
		h.put([]byte(f.Path))
		h.putBool(f.Exec)
		h.put(f.Data)
	}
	return files, hex.EncodeToString(h.Sum(nil))
}
