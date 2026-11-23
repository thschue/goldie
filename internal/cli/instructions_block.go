package cli

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/thschue/goldie/internal/contract/project"
)

// The managed-block editor behind `goldie instructions apply` and `check`
// (ADR 0007, marker format in ADR 0025). The byte functions never touch the
// disk; the file layer is in the style of project.WriteConfig.

// BlockState is what a file holds, compared with the content the server serves.
type BlockState string

const (
	// BlockMissing: no file, or no block in it.
	BlockMissing BlockState = "missing"
	// BlockCurrent: the stamp matches the body and the server's digest.
	BlockCurrent BlockState = "current"
	// BlockOutdated: the body matches its stamp; the stamp differs from the
	// server's digest.
	BlockOutdated BlockState = "outdated"
	// BlockEdited: the body does not match its own stamp. It wins over outdated.
	BlockEdited BlockState = "edited"
)

// The refusals of the editor. They hold no content, digest, path or file byte.
var (
	ErrBlockUnterminated = errors.New("the managed block has no end marker")
	ErrBlockNested       = errors.New("a managed block begins inside another")
	ErrBlockReversed     = errors.New("a managed block end marker has no begin marker before it")
	ErrBlockDuplicate    = errors.New("the file holds more than one managed block")
	ErrBlockSymlink      = errors.New("the instruction file is a symlink")
	ErrBlockContent      = errors.New("the block content would not read back as one block")

	errBlockName    = errors.New("the instruction file name is not a plain file name")
	errBlockNotFile = errors.New("the instruction file is not a regular file")
	errBlockSize    = errors.New("the instruction file is too large")
)

const (
	beginPrefix = "<!-- goldie:begin v1 digest="
	beginSuffix = " -->"
	digestWord  = "sha256:"
	endMarker   = "<!-- goldie:end -->"
	hexLen      = 64

	// maxBlockFile caps what the editor reads: an instruction file is prose.
	maxBlockFile = 8 << 20
)

var utf8BOM = []byte("\xef\xbb\xbf")

// BlockDigest returns "sha256:<hex>" over content with CRLF turned into LF and
// one trailing LF dropped.
func BlockDigest(content []byte) string {
	return digestOf(normaliseBlockContent(content))
}

// digestOf stamps text exactly as it is: the block body is normalised once, when
// it is written, and never again when it is read.
func digestOf(text []byte) string {
	sum := sha256.Sum256(text)
	return digestWord + hex.EncodeToString(sum[:])
}

func normaliseBlockContent(content []byte) []byte {
	return bytes.TrimSuffix(bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n")), []byte("\n"))
}

type markerKind int

const (
	markBegin markerKind = iota + 1
	markEnd
)

// marker is one marker line. lineStart..lineEnd is the line without its
// ending; next is the offset after the ending.
type marker struct {
	kind      markerKind
	digest    string
	lineStart int
	lineEnd   int
	next      int
}

// scanMarkers returns the marker lines of b from offset from, skipping lines
// inside fenced code blocks, and the fence still open at the end. Only LF and
// CRLF end a line.
func scanMarkers(b []byte, from int) (out []marker, f fence) {
	for pos := from; pos < len(b); {
		lineEnd, next := len(b), len(b)
		if i := bytes.IndexByte(b[pos:], '\n'); i >= 0 {
			lineEnd, next = pos+i, pos+i+1
			if lineEnd > pos && b[lineEnd-1] == '\r' {
				lineEnd--
			}
		}
		line := string(b[pos:lineEnd])
		if !f.step(line) {
			if m, ok := parseMarker(line); ok {
				m.lineStart, m.lineEnd, m.next = pos, lineEnd, next
				out = append(out, m)
			}
		}
		pos = next
	}
	return out, f
}

// fence tracks CommonMark fenced code blocks; an unclosed one runs to the end.
type fence struct {
	char byte
	n    int
}

// step feeds one line and reports whether it is inside a fence or is one of
// its delimiters.
func (f *fence) step(line string) bool {
	s := strings.TrimLeft(line, " ")
	if len(line)-len(s) > 3 || s == "" || (s[0] != '`' && s[0] != '~') {
		return f.n > 0
	}
	run := 0
	for run < len(s) && s[run] == s[0] {
		run++
	}
	if run < 3 {
		return f.n > 0
	}
	rest := s[run:]
	if f.n > 0 {
		if s[0] == f.char && run >= f.n && strings.Trim(rest, " \t") == "" {
			f.n = 0
		}
		return true
	}
	if s[0] == '`' && strings.Contains(rest, "`") {
		return false
	}
	f.char, f.n = s[0], run
	return true
}

// parseMarker accepts the exact v1 begin line with a 64-digit lowercase hex
// digest, and the exact end line. Anything else, a v2 included, is text.
func parseMarker(line string) (marker, bool) {
	if line == endMarker {
		return marker{kind: markEnd}, true
	}
	d, ok := strings.CutPrefix(line, beginPrefix)
	if !ok {
		return marker{}, false
	}
	d, ok = strings.CutSuffix(d, beginSuffix)
	hexPart, ok2 := strings.CutPrefix(d, digestWord)
	if !ok || !ok2 || len(hexPart) != hexLen {
		return marker{}, false
	}
	for i := 0; i < len(hexPart); i++ {
		if c := hexPart[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return marker{}, false
		}
	}
	return marker{kind: markBegin, digest: d}, true
}

// block is the one begin/end pair of a file.
type block struct {
	begin, end marker
}

// findBlock returns the block of file, or nil when it holds none, the offset
// after a BOM, and the code fence the file ends inside, if any.
func findBlock(file []byte) (bl *block, from int, unclosed fence, err error) {
	if bytes.HasPrefix(file, utf8BOM) {
		from = len(utf8BOM)
	}
	var open *block
	done := false
	markers, f := scanMarkers(file, from)
	unclosed = f
	for _, m := range markers {
		switch {
		case m.kind == markBegin && open != nil:
			return nil, from, fence{}, ErrBlockNested
		case m.kind == markBegin && done:
			return nil, from, fence{}, ErrBlockDuplicate
		case m.kind == markBegin:
			open = &block{begin: m}
		case open == nil:
			return nil, from, fence{}, ErrBlockReversed
		default:
			open.end = m
			bl, open, done = open, nil, true
		}
	}
	if open != nil {
		return nil, from, fence{}, ErrBlockUnterminated
	}
	return bl, from, unclosed, nil
}

// body is the block content as the file holds it, with LF endings.
func (bl *block) body(file []byte) []byte {
	b := file[bl.begin.next:bl.end.lineStart]
	b = bytes.TrimSuffix(b, []byte("\n"))
	b = bytes.TrimSuffix(b, []byte("\r"))
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// checkContent refuses content that would not read back as one block.
func checkContent(content []byte) error {
	norm := normaliseBlockContent(content)
	// A bare CR is not a line ending here but becomes part of one when a line
	// ending is added after it, so it could forge a marker.
	if bytes.IndexByte(norm, '\r') >= 0 {
		return ErrBlockContent
	}
	markers, f := scanMarkers(norm, 0)
	if len(markers) > 0 || f.n > 0 {
		return ErrBlockContent
	}
	return nil
}

// InspectBlock reports the state of the block in file against content, the
// bytes the server serves. A nil file is no file. It never writes.
func InspectBlock(file, content []byte) (BlockState, error) {
	if err := checkContent(content); err != nil {
		return "", err
	}
	bl, _, _, err := findBlock(file)
	if err != nil {
		return "", err
	}
	switch {
	case bl == nil:
		return BlockMissing, nil
	case digestOf(bl.body(file)) != bl.begin.digest:
		return BlockEdited, nil
	case bl.begin.digest != BlockDigest(content):
		return BlockOutdated, nil
	}
	return BlockCurrent, nil
}

// ApplyBlock returns the whole new file: the block written or refreshed, every
// byte outside the marker lines as it was. A current file comes back as it is.
func ApplyBlock(file, content []byte) ([]byte, error) {
	state, err := InspectBlock(file, content)
	if err != nil {
		return nil, err
	}
	if state == BlockCurrent {
		return append([]byte{}, file...), nil
	}
	bl, from, f, _ := findBlock(file)
	eol := detectEOL(file, from, bl)
	var blk bytes.Buffer
	blk.WriteString(beginPrefix + BlockDigest(content) + beginSuffix + eol)
	blk.Write(bytes.ReplaceAll(normaliseBlockContent(content), []byte("\n"), []byte(eol)))
	blk.WriteString(eol + endMarker)

	if bl != nil {
		out := append([]byte{}, file[:bl.begin.lineStart]...)
		out = append(out, blk.Bytes()...)
		return verifyBlock(append(out, file[bl.end.lineEnd:]...), content)
	}
	out := append([]byte{}, file...)
	if len(file) > from { // anything beyond a BOM
		if !bytes.HasSuffix(file, []byte("\n")) {
			out = append(out, eol...)
		}
		if f.n > 0 {
			// An unclosed fence runs to the end of the file and would swallow
			// the block. Closing it changes no rendering and keeps the file as
			// the prefix.
			out = append(out, bytes.Repeat([]byte{f.char}, f.n)...)
			out = append(out, eol...)
		}
		out = append(out, eol...)
	}
	out = append(out, blk.Bytes()...)
	return verifyBlock(append(out, eol...), content)
}

// verifyBlock is the last guard of ApplyBlock: what it returns must read back
// as the current block, whatever the file and content were.
func verifyBlock(out, content []byte) ([]byte, error) {
	if s, err := InspectBlock(out, content); err != nil || s != BlockCurrent {
		return nil, ErrBlockContent
	}
	return out, nil
}

// detectEOL is CRLF when the begin marker line, or the first line of a file
// without a block, ends in CRLF, else LF.
func detectEOL(file []byte, from int, bl *block) string {
	at := from
	if bl != nil {
		at = bl.begin.lineStart
	}
	if i := bytes.IndexByte(file[at:], '\n'); i > 0 && file[at+i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// CheckBlockFile reads dir/name and reports its block state. A missing file is
// BlockMissing. It never writes.
func CheckBlockFile(dir, name string, content []byte) (BlockState, error) {
	file, _, err := readBlockFile(dir, name)
	if err != nil {
		return "", err
	}
	return InspectBlock(file, content)
}

// ApplyBlockFile writes the block into dir/name and reports whether the file
// changed. It never follows a symlink, writes a temp file beside the file with
// the old mode and renames it over, and writes nothing when the bytes would
// not change. A new file gets mode 0644 less the umask.
func ApplyBlockFile(dir, name string, content []byte) (changed bool, err error) {
	file, mode, err := readBlockFile(dir, name)
	if err != nil {
		return false, err
	}
	out, err := ApplyBlock(file, content)
	if err != nil {
		return false, err
	}
	if file != nil && bytes.Equal(out, file) {
		return false, nil
	}
	if err := replaceBlockFile(dir, name, out, file != nil, mode); err != nil {
		return false, err
	}
	return true, nil
}

// plainBlockName reports whether name is one file name, with no directory part.
func plainBlockName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}

// readBlockFile returns the bytes and permission bits of dir/name. file is nil
// when it does not exist; mode is then the one a new file gets.
func readBlockFile(dir, name string) (file []byte, mode fs.FileMode, err error) {
	if !plainBlockName(name) {
		return nil, 0, errBlockName
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot open the directory: %w", project.Cause(err))
	}
	defer func() { _ = r.Close() }()
	fi, err := r.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, 0o644, nil
	case err != nil:
		return nil, 0, fmt.Errorf("cannot read the instruction file: %w", project.Cause(err))
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, 0, ErrBlockSymlink
	case !fi.Mode().IsRegular():
		return nil, 0, errBlockNotFile
	}
	f, err := r.OpenFile(name, os.O_RDONLY|project.OpenFlags|project.NoFollow, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot read the instruction file: %w", project.Cause(err))
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(fi, opened) {
		return nil, 0, errBlockNotFile
	}
	b, err := io.ReadAll(io.LimitReader(f, maxBlockFile+1))
	if err != nil {
		return nil, 0, fmt.Errorf("cannot read the instruction file: %w", project.Cause(err))
	}
	if len(b) > maxBlockFile {
		return nil, 0, errBlockSize
	}
	return append([]byte{}, b...), fi.Mode().Perm(), nil
}

// replaceBlockFile writes data to a temp file in dir and renames it over name.
// An existing file's mode is kept, past the umask; a new file gets 0644 less the
// umask. The temp file is removed on any failure. Another writer that changes
// the file between the read and the rename loses its edit: the window is that
// of one read-modify-write, as for any editor.
func replaceBlockFile(dir, name string, data []byte, existing bool, mode fs.FileMode) (err error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("cannot open the directory: %w", project.Cause(err))
	}
	defer func() { _ = r.Close() }()
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	tmp := "." + name + ".goldie-" + hex.EncodeToString(suffix)
	create := fs.FileMode(0o644)
	if existing {
		create = 0o600 // widened to the old mode only after the write
	}
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|project.NoFollow, create)
	if err != nil {
		return fmt.Errorf("cannot write the instruction file: %w", project.Cause(err))
	}
	defer func() {
		if err != nil {
			_ = r.Remove(tmp)
		}
	}()
	_, werr := f.Write(data)
	if werr == nil && existing {
		werr = f.Chmod(mode)
	}
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("cannot write the instruction file: %w", project.Cause(werr))
	}
	if err := r.Rename(tmp, name); err != nil {
		return fmt.Errorf("cannot write the instruction file: %w", project.Cause(err))
	}
	return nil
}
