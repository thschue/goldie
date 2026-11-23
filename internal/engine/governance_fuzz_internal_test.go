package engine

import (
	"strings"
	"testing"
	"unicode"
)

// refSeeds are hostile Item references: Unicode spaces, invisible and bidi
// characters, controls, traversal, absolute paths and extra colons.
var refSeeds = []string{
	"article:ok",
	"article:Team/Next",
	"skill:x",
	"Article:x",
	"article:",
	":x",
	"article",
	"article:a:b",
	"article::",
	"article:..",
	"article:../x",
	"article:a/../b",
	"article:a/.",
	"article:/x",
	"article:/etc/passwd",
	"article:a//b",
	"article:a/",
	`article:a\b`,
	"article:a b",
	"article:a\u00a0b",
	"article:a\u3000b",
	"article:a\u2003b",
	"article:a\u202fb",
	"article:a\u200bb",
	"article:a\u200db",
	"article:a\ufeffb",
	"article:a\u202eb",
	"article:a\u2066b",
	"article:a\u2028b",
	"article:a\x00b",
	"article:a\tb",
	"article:a\nb",
	"article:a\x7fb",
	"article:a\u0085b",
	"article:a\xffb",
	"art\u00a0icle:x",
	"article :x",
}

// FuzzParseRef checks that parsing a reference never panics, and that an
// accepted reference has a lower-case letter Kind and a name with only
// non-empty, non-dot segments and no space, control, format, colon or
// backslash character, and that its key is the lower-cased reference.
func FuzzParseRef(f *testing.F) {
	for _, s := range refSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ref string) {
		key, err := parseRef(ref)
		if err != nil {
			return
		}
		checkAcceptedRef(t, ref, key)
	})
}

// FuzzParseLifecycle checks that hostile status, enforced and replaced_by
// values never panic the article parser, and that an accepted article carries
// a status from the vocabulary and a replaced_by that parseRef accepts under
// the same rules.
func FuzzParseLifecycle(f *testing.F) {
	for _, front := range []string{
		"status: draft", "status: published", "status: deprecated",
		"status: Draft", "status: \"dra\\nft\"", "status: \"draft\\u00a0\"", "status: \"\\u202edraft\"",
		"status: [draft]", "status: 1", "status: ~", "status:",
		"enforced: true", "enforced: false", "enforced: True", "enforced: yes", "enforced: on",
		"enforced: \"true\"", "enforced: 1", "enforced: [true]", "enforced: !!bool true", "enforced: !!str true",
		"status: !!str draft", "status: !!binary ZHJhZnQ=",
	} {
		f.Add([]byte("---\nname: A\n" + front + "\n---\nbody"))
	}
	for _, ref := range refSeeds {
		f.Add([]byte("---\nname: A\nreplaced_by: " + yamlQuote(ref) + "\n---\nbody"))
	}
	f.Add([]byte("---\nname: A\nreplaced_by: &r article:x\nstatus: *r\n---\n"))
	f.Add([]byte("---\nname: A\nreplaced_by: !!str article:x\n---\n"))
	f.Add([]byte("---\nname: A\nreplaced_by: {article: x}\n---\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		a, err := parseArticle("x", raw)
		if err != nil {
			return
		}
		switch a.Status {
		case StatusDraft, StatusPublished, StatusDeprecated:
		default:
			t.Fatalf("accepted status %q: %q", a.Status, raw)
		}
		if a.ReplacedBy == "" {
			return
		}
		key, err := parseRef(a.ReplacedBy)
		if err != nil {
			t.Fatalf("accepted replaced_by %q that parseRef rejects: %v", a.ReplacedBy, err)
		}
		checkAcceptedRef(t, a.ReplacedBy, key)
	})
}

// yamlQuote renders s as a YAML double-quoted scalar, escaping every rune
// outside printable ASCII so the seed survives YAML's own line handling.
func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r >= 0x20 && r < 0x7f:
			b.WriteRune(r)
		case r <= 0xffff:
			b.WriteString(`\u`)
			b.WriteString(hex4(r))
		default:
			b.WriteString(`\U`)
			b.WriteString(hex4(r>>16) + hex4(r&0xffff))
		}
	}
	b.WriteByte('"')
	return b.String()
}

func hex4(r rune) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[r>>12&0xf], digits[r>>8&0xf], digits[r>>4&0xf], digits[r&0xf]})
}

// checkAcceptedRef holds an accepted reference to the reference rules
// (ADR 0021, review round 1 of #269).
func checkAcceptedRef(t *testing.T, ref string, key itemKey) {
	t.Helper()
	kind, name, ok := strings.Cut(ref, ":")
	if !ok || kind == "" || name == "" {
		t.Fatalf("accepted %q without <kind>:<name>", ref)
	}
	if strings.Trim(kind, "abcdefghijklmnopqrstuvwxyz") != "" {
		t.Fatalf("accepted %q with a Kind that is not lower-case letters", ref)
	}
	for _, r := range name {
		switch {
		case r == ':' || r == '\\':
			t.Fatalf("accepted %q with %q in the name", ref, r)
		case unicode.IsSpace(r) || unicode.In(r, unicode.Z):
			t.Fatalf("accepted %q with the space %U in the name", ref, r)
		case unicode.In(r, unicode.Cc, unicode.Cf):
			t.Fatalf("accepted %q with the control or invisible character %U in the name", ref, r)
		}
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			t.Fatalf("accepted %q with an empty, . or .. segment", ref)
		}
	}
	if key.kind != kind || key.name != strings.ToLower(name) {
		t.Fatalf("parseRef(%q) = {%q %q}, want {%q %q}", ref, key.kind, key.name, kind, strings.ToLower(name))
	}
}
