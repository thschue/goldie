package engine

import (
	"bytes"
	"strings"
	"testing"
)

func TestValidBundleName(t *testing.T) {
	for _, n := range []string{"a.md", "SKILL.md", ".gitignore", ".github", "notes.md.txt", "Makefile", "x-y_z", "é.md", "console", "com10", "lpt", "a b"} {
		if !validBundleName(n) {
			t.Errorf("validBundleName(%q) = false", n)
		}
	}
	for _, n := range []string{
		"", ".", "..", ".git", ".GIT", ".Git", "a/b", `a\b`, "a\x00", "a\u202eb", "\xff", "a\xc3",
		"a:b", "a:stream", "a.", "a ", "a<b", "a?", "con", "CON", "Nul.txt", "aux.md", "COM1", "lpt9.sh", "con .txt",
	} {
		if validBundleName(n) {
			t.Errorf("validBundleName(%q) = true", n)
		}
	}
}

// FuzzValidBundleName checks that an accepted name is one plain, valid path element.
func FuzzValidBundleName(f *testing.F) {
	for _, s := range []string{"a.md", "..", ".", "", "a/b", "a\x00", ".git", ".GIT", "a:b", "con", "a.", "\xff", "a\\b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if !validBundleName(name) {
			return
		}
		if name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00") || strings.EqualFold(name, ".git") || strings.HasSuffix(name, ".") {
			t.Fatalf("accepted %q", name)
		}
	})
}

// FuzzParseSkillFrontmatter checks that parsing never panics and that an
// accepted SKILL.md has the name asked for and a description.
func FuzzParseSkillFrontmatter(f *testing.F) {
	f.Add("x", []byte("---\nname: x\ndescription: d\n---\nbody\n"))
	f.Add("x", []byte("---\nname: [x]\n---\n"))
	f.Add("x", []byte("---\r\nname: x\r\n"))
	f.Add("", []byte(""))
	f.Fuzz(func(t *testing.T, name string, raw []byte) {
		desc, err := parseSkillFrontmatter(name, raw)
		if err == nil && (desc == "" || strings.TrimSpace(name) == "") {
			t.Fatalf("accepted name %q with description %q", name, desc)
		}
	})
}

// FuzzDigestFraming checks that two bundles that differ in the path of a file,
// its content or its executable bit never share a digest.
func FuzzDigestFraming(f *testing.F) {
	f.Add("a", []byte("bc"), false, "ab", []byte("c"), false)
	f.Add("a", []byte("1\x00c"), true, "a1", []byte("c"), false)
	f.Fuzz(func(t *testing.T, p1 string, c1 []byte, x1 bool, p2 string, c2 []byte, x2 bool) {
		if !validBundleName(p1) || !validBundleName(p2) {
			return
		}
		a := []SkillFile{{Path: p1, Content: c1, Executable: x1}}
		b := []SkillFile{{Path: p2, Content: c2, Executable: x2}}
		same := p1 == p2 && bytes.Equal(c1, c2) && x1 == x2
		if (digestFiles(a) == digestFiles(b)) != same {
			t.Fatalf("digest equality %v for %q/%q and %q/%q", !same, p1, c1, p2, c2)
		}
	})
}
