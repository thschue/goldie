package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
)

// BlockStatus describes the managed block found in a file by ReplaceBlock.
type BlockStatus int

const (
	// BlockMissing: the file holds no managed block.
	BlockMissing BlockStatus = iota
	// BlockCurrent: the block is intact and equals the wanted one.
	BlockCurrent
	// BlockOutdated: the block is intact but differs from the wanted one.
	BlockOutdated
	// BlockEdited: the content between the markers no longer matches the digest.
	BlockEdited
)

func (s BlockStatus) String() string {
	switch s {
	case BlockMissing:
		return "missing"
	case BlockCurrent:
		return "current"
	case BlockOutdated:
		return "outdated"
	case BlockEdited:
		return "edited"
	}
	return "unknown"
}

var (
	// ErrBlockEdited is returned for a hand-edited block when not forced.
	ErrBlockEdited = errors.New("the goldie block was edited by hand, and --force discards those edits")
	// ErrBlockMalformed is returned for a file whose markers do not form
	// exactly one block, or for block content that holds a marker.
	ErrBlockMalformed = errors.New("the goldie block markers are malformed")
)

// malformedError is an ErrBlockMalformed that carries its own message, so the
// text is not prefixed with the sentinel's and does not say the same thing twice.
type malformedError string

func (e malformedError) Error() string        { return string(e) }
func (e malformedError) Is(target error) bool { return target == ErrBlockMalformed }

// errDamagedFile and errBadContent are ErrBlockMalformed with the cause and the
// next step; forcing does not help in either case. The caller adds the path.
var (
	errDamagedFile = malformedError("the goldie block in this file is damaged (a begin marker with no end, or more than one block). " +
		"Fix or delete the lines from \"<!-- goldie:begin\" to \"<!-- goldie:end -->\" by hand, then run again. --force does not help.")
	errBadContent = malformedError("the block goldie wants to write contains a goldie marker. This is a goldie bug, not a problem in your file: " +
		"leave the file as it is and report it on the goldie issue tracker, with the output of `goldie version`.")
)

const (
	beginPrefix = "<!-- goldie:begin"
	endMarker   = "<!-- goldie:end -->"
)

var beginMarkerRE = regexp.MustCompile(`^<!-- goldie:begin digest=sha256:([0-9a-f]{64}) -->`)

// blockBody is block with a trailing newline, the bytes the digest covers.
func blockBody(block []byte) []byte {
	if len(block) == 0 || block[len(block)-1] != '\n' {
		return append(append([]byte{}, block...), '\n')
	}
	return append([]byte{}, block...)
}

func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// renderBlock is the begin marker, the body and the end marker, without a
// trailing newline.
func renderBlock(body []byte) []byte {
	out := []byte(beginPrefix + " digest=sha256:" + digestOf(body) + " -->\n")
	out = append(out, body...)
	return append(out, endMarker...)
}

// ReplaceBlock puts block between goldie's markers in file and returns the new
// file with the status the input file had. It touches only the bytes from the
// begin marker to the end marker, or appends the block when the file has none.
// A hand-edited block is refused (nil bytes, ErrBlockEdited) unless force is
// set; a begin marker without an end, two blocks, or block content that holds
// a marker is always ErrBlockMalformed (wrapped with the cause). file is never
// modified.
//
// The block in a file is the begin marker, the body and the end marker:
//
//	<!-- goldie:begin digest=sha256:<hex> -->
//	...body, on the lines between the markers...
//	<!-- goldie:end -->
//
// <hex> is the lowercase sha256 of the body, with a trailing newline added to
// the body if it has none. A body that no longer matches its digest counts as
// hand-edited (BlockEdited). A block with the same digest as the wanted one is
// BlockCurrent; an intact block with another digest is BlockOutdated.
func ReplaceBlock(file, block []byte, force bool) ([]byte, BlockStatus, error) {
	body := blockBody(block)
	if bytes.Contains(body, []byte(beginPrefix)) || bytes.Contains(body, []byte(endMarker)) {
		return nil, BlockMissing, errBadContent
	}
	wanted := renderBlock(body)

	begins := bytes.Count(file, []byte(beginPrefix))
	ends := bytes.Count(file, []byte(endMarker))
	if begins == 0 && ends == 0 {
		out := append([]byte{}, file...)
		if len(out) > 0 {
			if out[len(out)-1] != '\n' {
				out = append(out, '\n')
			}
			out = append(out, '\n')
		}
		out = append(out, wanted...)
		return append(out, '\n'), BlockMissing, nil
	}
	if begins != 1 || ends != 1 {
		return nil, BlockMissing, errDamagedFile
	}
	start := bytes.Index(file, []byte(beginPrefix))
	end := bytes.Index(file, []byte(endMarker))
	m := beginMarkerRE.FindSubmatch(file[start:])
	if m == nil || end < start+len(m[0]) {
		return nil, BlockMissing, errDamagedFile
	}
	inner := file[start+len(m[0]) : end]
	if len(inner) == 0 || inner[0] != '\n' {
		return nil, BlockMissing, errDamagedFile
	}
	found := inner[1:]
	status := BlockOutdated
	switch {
	case digestOf(found) != string(m[1]):
		status = BlockEdited
	case string(m[1]) == digestOf(body):
		return append([]byte{}, file...), BlockCurrent, nil
	}
	if status == BlockEdited && !force {
		return nil, BlockEdited, ErrBlockEdited
	}
	after := file[end+len(endMarker):]
	out := make([]byte, 0, start+len(wanted)+len(after))
	out = append(out, file[:start]...)
	out = append(out, wanted...)
	return append(out, after...), status, nil
}
