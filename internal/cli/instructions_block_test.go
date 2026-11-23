package cli_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/cli"
)

// Acceptance tests for #413 (slice 1 of #18): the managed-block editor.
//
// The editor is a set of pure byte functions in internal/cli plus a thin file
// layer in the style of project.WriteConfig. The tests fix these names:
//
//	type BlockState string
//	const (
//		BlockMissing  BlockState = "missing"  // no file, or no block in it
//		BlockCurrent  BlockState = "current"  // stamp matches the body and the server
//		BlockOutdated BlockState = "outdated" // body matches its stamp; stamp differs from the server's digest
//		BlockEdited   BlockState = "edited"   // body does not match its own stamp
//	)
//
//	func BlockDigest(content []byte) string
//	func InspectBlock(file, content []byte) (BlockState, error)
//	func ApplyBlock(file, content []byte) ([]byte, error)
//	func CheckBlockFile(dir, name string, content []byte) (BlockState, error)
//	func ApplyBlockFile(dir, name string, content []byte) (changed bool, err error)
//
// content is what the server serves. Line endings in it are normalised to LF
// and one trailing LF is dropped; BlockDigest returns "sha256:<hex>" over that.
// file is the bytes of CLAUDE.md or AGENTS.md; nil means there is no file.
// InspectBlock never writes; ApplyBlock returns the whole new file and leaves
// every byte outside the begin and end marker lines as it was. It replaces an
// edited block too: refusing to overwrite without --force is policy for the
// command in a later slice, which reads InspectBlock first.
//
// Block layout, with EOL the file's line ending (CRLF when the begin marker
// line, or the first line of a file without a block, ends in CRLF, else LF):
//
//	<!-- goldie:begin v1 digest=sha256:<hex> -->EOL<content>EOL<!-- goldie:end -->EOL
//
// No block, existing file: the block is appended after the existing bytes,
// which stay as the prefix. A missing final newline is added (EOL), then one
// empty line (EOL) unless the file is empty, then the block. No file: the
// block alone, with LF and no BOM.
//
// Errors are sentinels that never hold the content, the digest, a path or a
// byte of the file:
//
//	ErrBlockUnterminated  a begin marker with no end marker after it
//	ErrBlockNested        a second begin marker before the first one's end
//	ErrBlockReversed      an end marker with no begin marker before it
//	ErrBlockDuplicate     more than one complete pair
//	ErrBlockSymlink       the file is a symlink
//	ErrBlockContent       content that would not read back as one block: it
//	                      holds a marker line, or leaves a code fence open
//
// A marker is a whole line: the exact begin form with a 64-digit lowercase hex
// digest, or the exact end form. A line inside a fenced code block (``` or
// ~~~, CommonMark rules, an unclosed fence runs to the end of the file) is not
// a marker, and neither is a marker mentioned inside a longer line.

const blocksDir = "testdata/blocks"

var (
	bom  = []byte("\xef\xbb\xbf")
	crlf = func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n")) }
)

func blockFixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{blocksDir}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func blockFixtureOptional(t *testing.T, parts ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{blocksDir}, parts...)...))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The digest of testdata/blocks/content.md and content_old.md.
const (
	digestNew = "sha256:9714e828159ee96c402d75e156bc573e1bbde17d7305ec90e3c844bf25ed4a2a"
	digestOld = "sha256:7ff46c49dd8dd4a6ae15610acbefe718a92e8ef182b3f757a464be0e73d91817"
)

// blockSpan returns the offsets of the block in b: the begin marker through
// the line ending after the end marker.
func blockSpan(t *testing.T, b []byte) (begin, end int) {
	t.Helper()
	const endMarker = "<!-- goldie:end -->"
	begin = bytes.Index(b, []byte("<!-- goldie:begin"))
	i := bytes.Index(b, []byte(endMarker))
	if begin < 0 || i < 0 {
		t.Fatalf("fixture has no block: %q", b)
	}
	end = i + len(endMarker)
	if bytes.HasPrefix(b[end:], []byte("\r\n")) {
		end += 2
	} else if bytes.HasPrefix(b[end:], []byte("\n")) {
		end++
	}
	return begin, end
}

func mustTime(t *testing.T) time.Time {
	t.Helper()
	return time.Now().Add(-48 * time.Hour).Truncate(time.Second)
}

func TestBlockDigestCoversLFNormalisedContent(t *testing.T) {
	content := blockFixture(t, "content.md")
	for name, in := range map[string][]byte{
		"LF with trailing newline": content,
		"no trailing newline":      bytes.TrimSuffix(content, []byte("\n")),
		"CRLF":                     crlf(content),
	} {
		if got := cli.BlockDigest(in); got != digestNew {
			t.Errorf("%s: digest = %q, want %q", name, got, digestNew)
		}
	}
	if got := cli.BlockDigest(blockFixture(t, "content_old.md")); got != digestOld {
		t.Errorf("old content: digest = %q, want %q", got, digestOld)
	}
	if cli.BlockDigest(content) == cli.BlockDigest([]byte("different")) {
		t.Error("different content, same digest")
	}
}

// blockCases is the matrix. wantErr is nil for the cases that edit.
var blockCases = []struct {
	dir     string
	state   cli.BlockState
	wantErr error
}{
	{"no_file", cli.BlockMissing, nil},
	{"empty_file", cli.BlockMissing, nil},
	{"no_block", cli.BlockMissing, nil},
	{"no_block_no_final_newline", cli.BlockMissing, nil},
	{"current", cli.BlockCurrent, nil},
	{"outdated", cli.BlockOutdated, nil},
	{"edited", cli.BlockEdited, nil},
	{"edited_outdated", cli.BlockEdited, nil},
	{"fenced_only", cli.BlockMissing, nil},
	{"fenced_then_real", cli.BlockOutdated, nil},
	{"inline_mention", cli.BlockMissing, nil},
	{"duplicate", "", cli.ErrBlockDuplicate},
	{"unterminated", "", cli.ErrBlockUnterminated},
	{"nested", "", cli.ErrBlockNested},
	{"reversed", "", cli.ErrBlockReversed},
	{"lone_end", "", cli.ErrBlockReversed},
}

// variants re-encode a fixture as a file would arrive: LF, CRLF, with a BOM,
// or both. A file that does not exist stays absent.
var variants = []struct {
	name string
	enc  func([]byte) []byte
}{
	{"LF", func(b []byte) []byte { return b }},
	{"CRLF", crlf},
	{"BOM", func(b []byte) []byte { return append(append([]byte{}, bom...), b...) }},
	{"BOM+CRLF", func(b []byte) []byte { return append(append([]byte{}, bom...), crlf(b)...) }},
}

func TestManagedBlockGoldenMatrix(t *testing.T) {
	content := blockFixture(t, "content.md")
	for _, tc := range blockCases {
		in := blockFixtureOptional(t, tc.dir, "in.md")
		want := blockFixtureOptional(t, tc.dir, "want.md")
		for _, v := range variants {
			t.Run(tc.dir+"/"+v.name, func(t *testing.T) {
				var file, wantFile []byte
				if in != nil {
					file = v.enc(in)
				} else if v.name != "LF" {
					t.Skip("a missing file has no encoding")
				}
				if tc.dir == "empty_file" && v.name != "LF" {
					t.Skip("an empty file has no line ending to follow, and a file of only a BOM is not pinned")
				}
				if want != nil {
					wantFile = v.enc(want)
					if in == nil {
						wantFile = want // a new file is LF without a BOM
					}
				}
				orig := append([]byte(nil), file...)

				state, err := cli.InspectBlock(file, content)
				if tc.wantErr != nil {
					if !errors.Is(err, tc.wantErr) {
						t.Fatalf("InspectBlock err = %v, want %v", err, tc.wantErr)
					}
					out, err := cli.ApplyBlock(file, content)
					if !errors.Is(err, tc.wantErr) || out != nil {
						t.Fatalf("ApplyBlock = %q, %v; want nil and %v", out, err, tc.wantErr)
					}
				} else {
					if err != nil || state != tc.state {
						t.Fatalf("InspectBlock = %q, %v; want %q", state, err, tc.state)
					}
					out, err := cli.ApplyBlock(file, content)
					if err != nil {
						t.Fatalf("ApplyBlock: %v", err)
					}
					if !bytes.Equal(out, wantFile) {
						t.Errorf("ApplyBlock:\n got %q\nwant %q", out, wantFile)
					}
					// Applying twice changes nothing more, and the result reads as current.
					again, err := cli.ApplyBlock(out, content)
					if err != nil || !bytes.Equal(again, out) {
						t.Errorf("second ApplyBlock = %q, %v; want the same bytes", again, err)
					}
					if s, err := cli.InspectBlock(out, content); err != nil || s != cli.BlockCurrent {
						t.Errorf("InspectBlock after apply = %q, %v; want current", s, err)
					}
				}
				if !bytes.Equal(file, orig) {
					t.Error("the input slice was modified")
				}
			})
		}
	}
}

func TestCurrentBlockIsReturnedByteForByte(t *testing.T) {
	content := blockFixture(t, "content.md")
	in := blockFixture(t, "current", "in.md")
	for _, v := range variants {
		file := v.enc(in)
		out, err := cli.ApplyBlock(file, content)
		if err != nil || !bytes.Equal(out, file) {
			t.Errorf("%s: ApplyBlock on a current block = %q, %v; want the file unchanged", v.name, out, err)
		}
	}
}

func TestEditedBeatsOutdatedAndBothAreReported(t *testing.T) {
	// An edited block whose stamp is also not the server's digest is edited:
	// a hand edit must never be reported as a plain, safe refresh.
	state, err := cli.InspectBlock(blockFixture(t, "edited_outdated", "in.md"), blockFixture(t, "content.md"))
	if err != nil || state != cli.BlockEdited {
		t.Errorf("state = %q, %v; want edited", state, err)
	}
}

func TestBlockWithoutFinalNewlineKeepsItsEnd(t *testing.T) {
	content := blockFixture(t, "content.md")
	cur := bytes.TrimSuffix(blockFixture(t, "current", "want.md"), []byte("\nAfter.\n"))
	cur = bytes.TrimSuffix(cur, []byte("\n")) // ends with the end marker, no newline
	if !bytes.HasSuffix(cur, []byte("<!-- goldie:end -->")) {
		t.Fatalf("fixture setup: %q", cur)
	}
	if s, err := cli.InspectBlock(cur, content); err != nil || s != cli.BlockCurrent {
		t.Errorf("end marker at EOF: state = %q, %v; want current", s, err)
	}
	old := blockFixture(t, "outdated", "in.md")
	old = bytes.TrimSuffix(old, []byte("\nAfter the block.\n"))
	old = bytes.TrimSuffix(old, []byte("\n"))
	out, err := cli.ApplyBlock(old, content)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(out, []byte("<!-- goldie:end -->")) || !bytes.Contains(out, []byte(digestNew)) {
		t.Errorf("outdated block at EOF: got %q, want it refreshed and still ending at the marker", out)
	}
}

func TestBytesOutsideTheMarkersAreUntouched(t *testing.T) {
	content := blockFixture(t, "content.md")
	old := crlf(blockFixture(t, "outdated", "in.md"))
	begin, end := blockSpan(t, old)
	block := old[begin:end]
	wantCRLF := crlf(blockFixture(t, "outdated", "want.md"))
	wb, we := blockSpan(t, wantCRLF)

	// Hostile bytes: a BOM, lone CR, NUL, invalid UTF-8, bidi and zero-width
	// characters, trailing spaces, mixed endings, and no final newline.
	prefix := []byte("\xef\xbb\xbf# t\u202e\u200b \r\n\rlone cr\x00 \xff\xfe bad\nmixed lf \r\n")
	suffix := []byte("\r\n tail  \r\n\r\n\ttab\nno newline at end \u2028")
	file := bytes.Join([][]byte{prefix, block, suffix}, nil)

	out, err := cli.ApplyBlock(file, content)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, prefix) || !bytes.HasSuffix(out, suffix) {
		t.Fatalf("bytes outside the markers changed:\n got %q", out)
	}
	mid := out[len(prefix) : len(out)-len(suffix)]
	if !bytes.Equal(mid, wantCRLF[wb:we]) {
		t.Errorf("block = %q, want the refreshed block with CRLF endings", mid)
	}
}

func TestMarkerLookalikesAreText(t *testing.T) {
	content := blockFixture(t, "content.md")
	for name, file := range map[string]string{
		"zero-width inside begin": "<!-- goldie:begin\u200b v1 digest=" + digestNew + " -->\nx\n",
		"zero-width inside end":   "<!-- goldie:end\u200b -->\n",
		"upper-case digest":       "<!-- goldie:begin v1 digest=sha256:" + strings.ToUpper(digestNew[7:]) + " -->\nx\n",
		"leading text":            "x <!-- goldie:end -->\n",
		"trailing text":           "<!-- goldie:end --> x\n",
		"indented fence opener":   "   ```\n<!-- goldie:end -->\n```\n",
		"unclosed fence":          "```\n<!-- goldie:end -->\n",
		"longer fence":            "````\n```\n<!-- goldie:end -->\n```\n````\n",
	} {
		state, err := cli.InspectBlock([]byte(file), content)
		if err != nil || state != cli.BlockMissing {
			t.Errorf("%s: state = %q, %v; want missing and no error", name, state, err)
		}
		out, err := cli.ApplyBlock([]byte(file), content)
		if err != nil || !strings.HasPrefix(string(out), file) {
			t.Errorf("%s: ApplyBlock = %q, %v; want the file kept as the prefix", name, out, err)
		}
	}
}

func TestErrorsNeverEchoFileOrContent(t *testing.T) {
	content := blockFixture(t, "content.md")
	secret := "SECRET-TOKEN-0123456789"
	for _, bad := range []string{"unterminated", "nested", "reversed", "duplicate", "lone_end"} {
		file := append([]byte("# "+secret+"\n"), blockFixture(t, bad, "in.md")...)
		_, err := cli.InspectBlock(file, content)
		if err == nil {
			t.Fatalf("%s: no error", bad)
		}
		for _, leak := range []string{secret, "goldie ask", "sha256", "9714e828", "\n"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: error %q echoes %q", bad, err, leak)
			}
		}
	}
}

func TestContentThatWouldNotReadBackIsRefused(t *testing.T) {
	file := []byte("# keep\n")
	for name, content := range map[string]string{
		"end marker line":   "text\n<!-- goldie:end -->\nmore",
		"begin marker line": "<!-- goldie:begin v1 digest=" + digestNew + " -->\nmore",
		"unclosed fence":    "Run:\n```sh\ngoldie ask",
	} {
		out, err := cli.ApplyBlock(file, []byte(content))
		if !errors.Is(err, cli.ErrBlockContent) || out != nil {
			t.Errorf("%s: ApplyBlock = %q, %v; want nil and ErrBlockContent", name, out, err)
		}
		if err != nil && strings.Contains(err.Error(), "goldie ask") {
			t.Errorf("%s: error echoes the content: %v", name, err)
		}
	}

	// A closed fence, and a marker mentioned inside one, are fine and read back.
	ok := []byte("Run:\n```sh\ngoldie ask\n```\nand\n```\n<!-- goldie:end -->\n```\n")
	out, err := cli.ApplyBlock(file, ok)
	if err != nil {
		t.Fatalf("closed fence: %v", err)
	}
	if s, err := cli.InspectBlock(out, ok); err != nil || s != cli.BlockCurrent {
		t.Errorf("closed fence read back as %q, %v; want current", s, err)
	}
}

func TestContentLineEndingsAreNormalisedBeforeWriting(t *testing.T) {
	lf := blockFixture(t, "content.md")
	in := blockFixture(t, "no_block", "in.md")
	fromLF, err1 := cli.ApplyBlock(in, lf)
	fromCRLF, err2 := cli.ApplyBlock(in, crlf(lf))
	noNL, err3 := cli.ApplyBlock(in, bytes.TrimSuffix(lf, []byte("\n")))
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatal(err1, err2, err3)
	}
	if !bytes.Equal(fromLF, fromCRLF) || !bytes.Equal(fromLF, noNL) {
		t.Errorf("content spellings give different files:\n%q\n%q\n%q", fromLF, fromCRLF, noNL)
	}
}

// File layer.

func blockEntries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestApplyBlockFileCreatesAndEditsBothInstructionFiles(t *testing.T) {
	content := blockFixture(t, "content.md")
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		dir := t.TempDir()
		changed, err := cli.ApplyBlockFile(dir, name, content)
		if err != nil || !changed {
			t.Fatalf("%s: create: changed = %v, err = %v", name, changed, err)
		}
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if !bytes.Equal(got, blockFixture(t, "no_file", "want.md")) {
			t.Errorf("%s: created %q", name, got)
		}
		fi, _ := os.Stat(filepath.Join(dir, name))
		if p := fi.Mode().Perm(); p&^0o644 != 0 || p&0o600 != 0o600 {
			t.Errorf("%s: new file mode %v, want 0644 less the umask", name, p)
		}
		if es := blockEntries(t, dir); len(es) != 1 {
			t.Errorf("%s: directory holds %v, want only the file", name, es)
		}

		changed, err = cli.ApplyBlockFile(dir, name, content)
		if err != nil || changed {
			t.Errorf("%s: second apply: changed = %v, err = %v; want a no-op", name, changed, err)
		}
	}
}

func TestApplyBlockFileLeavesACurrentFileAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, blockFixture(t, "current", "in.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := mustTime(t)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	changed, err := cli.ApplyBlockFile(dir, "CLAUDE.md", blockFixture(t, "content.md"))
	if err != nil || changed {
		t.Fatalf("changed = %v, err = %v; want a no-op", changed, err)
	}
	fi, _ := os.Stat(path)
	if !fi.ModTime().Equal(past) {
		t.Errorf("a current file was rewritten: mtime %v, want %v", fi.ModTime(), past)
	}
}

func TestApplyBlockFileWritesNothingOnAnError(t *testing.T) {
	content := blockFixture(t, "content.md")
	for _, bad := range []string{"unterminated", "nested", "reversed", "duplicate", "lone_end"} {
		dir := t.TempDir()
		in := append([]byte("# "+dir+" SECRET-TOKEN\n"), blockFixture(t, bad, "in.md")...)
		path := filepath.Join(dir, "CLAUDE.md")
		if err := os.WriteFile(path, in, 0o640); err != nil {
			t.Fatal(err)
		}
		changed, err := cli.ApplyBlockFile(dir, "CLAUDE.md", content)
		if err == nil || changed {
			t.Fatalf("%s: changed = %v, err = %v; want an error", bad, changed, err)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, in) {
			t.Errorf("%s: the file changed: %q", bad, got)
		}
		if es := blockEntries(t, dir); len(es) != 1 {
			t.Errorf("%s: directory holds %v, want no leftover", bad, es)
		}
		for _, leak := range []string{dir, "CLAUDE.md", "SECRET-TOKEN", "goldie ask"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: error %q echoes %q", bad, err, leak)
			}
		}
	}
}

func TestCheckBlockFileReportsTheStateAndNeverWrites(t *testing.T) {
	content := blockFixture(t, "content.md")
	dir := t.TempDir()
	if s, err := cli.CheckBlockFile(dir, "CLAUDE.md", content); err != nil || s != cli.BlockMissing {
		t.Errorf("no file: %q, %v; want missing", s, err)
	}
	if es := blockEntries(t, dir); len(es) != 0 {
		t.Errorf("check created %v", es)
	}
	for _, tc := range []struct {
		fixture string
		want    cli.BlockState
	}{{"current", cli.BlockCurrent}, {"outdated", cli.BlockOutdated}, {"edited", cli.BlockEdited}, {"no_block", cli.BlockMissing}} {
		in := blockFixture(t, tc.fixture, "in.md")
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), in, 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := cli.CheckBlockFile(dir, "AGENTS.md", content)
		if err != nil || s != tc.want {
			t.Errorf("%s: %q, %v; want %q", tc.fixture, s, err, tc.want)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")); !bytes.Equal(got, in) {
			t.Errorf("%s: check changed the file", tc.fixture)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), blockFixture(t, "duplicate", "in.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CheckBlockFile(dir, "AGENTS.md", content); !errors.Is(err, cli.ErrBlockDuplicate) {
		t.Errorf("duplicate: err = %v, want ErrBlockDuplicate", err)
	}
}

func TestBlockFileNameMustBeAPlainFileName(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../CLAUDE.md", "sub/CLAUDE.md", "", ".", "..", "/etc/passwd"} {
		if changed, err := cli.ApplyBlockFile(dir, name, blockFixture(t, "content.md")); err == nil || changed {
			t.Errorf("ApplyBlockFile(%q) = %v, %v; want an error", name, changed, err)
		}
		if _, err := cli.CheckBlockFile(dir, name, blockFixture(t, "content.md")); err == nil {
			t.Errorf("CheckBlockFile(%q): want an error", name)
		}
	}
	if es := blockEntries(t, root); len(es) != 1 {
		t.Errorf("root holds %v, want nothing written beside the repo", es)
	}
	if es := blockEntries(t, dir); len(es) != 0 {
		t.Errorf("repo holds %v, want nothing written", es)
	}
}

func TestApplyBlockFileRefusesADirectoryNamedLikeTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "CLAUDE.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if changed, err := cli.ApplyBlockFile(dir, "CLAUDE.md", blockFixture(t, "content.md")); err == nil || changed {
		t.Errorf("changed = %v, err = %v; want an error", changed, err)
	}
	if es := blockEntries(t, dir); len(es) != 1 {
		t.Errorf("directory holds %v", es)
	}
}
