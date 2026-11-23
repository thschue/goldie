package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

type tarEntry struct {
	name string
	typ  byte
	mode int64
	link string
	body string
}

func makeTarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if e.typ == 0 {
			e.typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Linkname: e.link, Size: int64(len(e.body))}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBundleAcceptsTheNamespaceOnly(t *testing.T) {
	good := makeTarGz(t,
		tarEntry{name: ".claude/agents/goldie-a.md", mode: 0o644, body: "agent"},
		tarEntry{name: ".claude/skills/goldie-s/run.sh", mode: 0o755, body: "#!/bin/sh"},
		tarEntry{name: ".claude/skills/goldie-s/SKILL.md", mode: 0o644, body: "skill"},
	)
	files, err := extractBundle(good)
	if err != nil {
		t.Fatalf("a valid bundle: %v", err)
	}
	if len(files) != 3 || !files[1].exec || files[0].exec || files[2].exec {
		t.Fatalf("files = %+v, want 3 with only run.sh executable", files)
	}

	bad := map[string][]tarEntry{
		"absolute":       {{name: "/etc/passwd", mode: 0o644}},
		"dotdot":         {{name: ".claude/skills/goldie-s/../../../x", mode: 0o644}},
		"outside":        {{name: "CLAUDE.md", mode: 0o644}},
		"foreign skill":  {{name: ".claude/skills/mine/SKILL.md", mode: 0o644}},
		"foreign agent":  {{name: ".claude/agents/mine.md", mode: 0o644}},
		"stamp":          {{name: ".claude/goldie-pull.digest", mode: 0o644}},
		"symlink":        {{name: ".claude/skills/goldie-s/l", typ: tar.TypeSymlink, link: "/etc"}},
		"hardlink":       {{name: ".claude/skills/goldie-s/l", typ: tar.TypeLink, link: "x"}},
		"fifo":           {{name: ".claude/skills/goldie-s/f", typ: tar.TypeFifo}},
		"device":         {{name: ".claude/skills/goldie-s/d", typ: tar.TypeChar}},
		"directory":      {{name: ".claude/skills/goldie-s/", typ: tar.TypeDir}},
		"backslash":      {{name: ".claude/skills/goldie-s/a\\b", mode: 0o644}},
		"bidi":           {{name: ".claude/skills/goldie-s/a\u202eb", mode: 0o644}},
		"duplicate":      {{name: ".claude/skills/goldie-s/a", mode: 0o644}, {name: ".claude/skills/goldie-s/a", mode: 0o644}},
		"file then dir":  {{name: ".claude/skills/goldie-s/a", mode: 0o644}, {name: ".claude/skills/goldie-s/a/b", mode: 0o644}},
		"agent subpath":  {{name: ".claude/agents/goldie-a.md/x", mode: 0o644}},
		"agent not md":   {{name: ".claude/agents/goldie-a", mode: 0o644}},
		"empty skill":    {{name: ".claude/skills/goldie-/x", mode: 0o644}},
		"skill top file": {{name: ".claude/skills/goldie-s", mode: 0o644}},
	}
	for name, entries := range bad {
		if _, err := extractBundle(makeTarGz(t, entries...)); err == nil {
			t.Errorf("%s: extractBundle accepted it", name)
		}
	}
	if _, err := extractBundle([]byte("not gzip")); err == nil {
		t.Error("a body that is not gzip was accepted")
	}
	if _, err := extractBundle(good[:len(good)-6]); err == nil {
		t.Error("a truncated archive was accepted")
	}
}
