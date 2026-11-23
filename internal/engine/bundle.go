package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"strings"
	"unicode"
)

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
type bundleLayout func(skills []skillBundle, agents []Agent) []BundleFile

var bundleLayouts = map[string]bundleLayout{TargetClaude: claudeLayout}

// goldiePrefix starts every name in the Goldie namespace, so a pull owns
// exactly these and nothing else.
const goldiePrefix = "goldie-"

// claudeLayout is the Claude target: .claude/skills/goldie-<name>/<file> and
// .claude/agents/goldie-<name>.md.
func claudeLayout(skills []skillBundle, agents []Agent) []BundleFile {
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
		if b, ok := s.skillBundleFor(proj, sk.Name); ok {
			skills = append(skills, b)
		}
	}
	files := layout(skills, s.Agents(proj))
	slices.SortFunc(files, func(a, b BundleFile) int { return strings.Compare(a.Path, b.Path) })
	h := sha256.New()
	put := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	put([]byte(target))
	for _, f := range files {
		put([]byte(f.Path))
		if f.Exec {
			put([]byte{1})
		} else {
			put([]byte{0})
		}
		put(f.Data)
	}
	return files, hex.EncodeToString(h.Sum(nil))
}
