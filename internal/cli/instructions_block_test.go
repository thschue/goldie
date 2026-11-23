package cli_test

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/cli"
)

// Tests for #413: cli.ReplaceBlock(file, block []byte, force bool)
// ([]byte, cli.BlockStatus, error) is the pure, byte-in byte-out core of
// goldie instructions apply and check. The status describes the input file:
// cli.BlockMissing, cli.BlockCurrent, cli.BlockOutdated or cli.BlockEdited.
// The tests build every input with ReplaceBlock itself, so they pin behaviour
// and the documented markers, not the whitespace around the block.

var beginMarker = regexp.MustCompile(`<!-- goldie:begin digest=sha256:[0-9a-f]{64} -->`)

const (
	endMarker    = "<!-- goldie:end -->"
	blockV1      = "Use goldie before you write code.\nSee goldie_search.\n"
	blockV2      = "Ask goldie first.\nSee goldie_lookup.\n"
	handNotes    = "# Notes\n\nKeep this line.\n"
	handTrailing = "\n## After\n\nAlso keep this.\n"
)

func replaceBlock(file, block string, force bool) (string, cli.BlockStatus, error) {
	out, st, err := cli.ReplaceBlock([]byte(file), []byte(block), force)
	return string(out), st, err
}

func mustReplace(t *testing.T, file, block string) string {
	t.Helper()
	out, _, err := replaceBlock(file, block, false)
	if err != nil {
		t.Fatalf("ReplaceBlock(%q): %v", block, err)
	}
	return out
}

func TestReplaceBlock(t *testing.T) {
	t.Run("a file with no block gets the block appended and the rest is unchanged", func(t *testing.T) {
		out, st, err := replaceBlock(handNotes, blockV1, false)
		if err != nil {
			t.Fatal(err)
		}
		if st != cli.BlockMissing {
			t.Errorf("status = %v, want BlockMissing", st)
		}
		if !strings.HasPrefix(out, handNotes) {
			t.Errorf("existing content changed:\n%q", out)
		}
		if len(beginMarker.FindAllString(out, -1)) != 1 || strings.Count(out, endMarker) != 1 {
			t.Errorf("want exactly one begin and one end marker:\n%q", out)
		}
		if !strings.Contains(out, blockV1) {
			t.Errorf("block content missing:\n%q", out)
		}
	})

	t.Run("an empty file becomes just the block", func(t *testing.T) {
		out, st, err := replaceBlock("", blockV1, false)
		if err != nil {
			t.Fatal(err)
		}
		if st != cli.BlockMissing {
			t.Errorf("status = %v, want BlockMissing", st)
		}
		if !beginMarker.MatchString(out) || !strings.Contains(out, blockV1) || !strings.Contains(out, endMarker) {
			t.Errorf("block not written:\n%q", out)
		}
	})

	t.Run("a current block comes back byte for byte unchanged", func(t *testing.T) {
		file := handNotes + mustReplace(t, "", blockV1) + handTrailing
		out, st, err := replaceBlock(file, blockV1, false)
		if err != nil {
			t.Fatal(err)
		}
		if st != cli.BlockCurrent {
			t.Errorf("status = %v, want BlockCurrent", st)
		}
		if out != file {
			t.Errorf("bytes changed:\nwant %q\ngot  %q", file, out)
		}
	})

	t.Run("an outdated block changes only the bytes between the markers", func(t *testing.T) {
		file := mustReplace(t, handNotes, blockV1) + handTrailing
		out, st, err := replaceBlock(file, blockV2, false)
		if err != nil {
			t.Fatal(err)
		}
		if st != cli.BlockOutdated {
			t.Errorf("status = %v, want BlockOutdated", st)
		}
		if want := mustReplace(t, handNotes, blockV2) + handTrailing; out != want {
			t.Errorf("want %q\ngot  %q", want, out)
		}
		if strings.Contains(out, blockV1) || !strings.Contains(out, blockV2) {
			t.Errorf("block not replaced:\n%q", out)
		}
	})

	t.Run("a hand-edited block is refused unless forced", func(t *testing.T) {
		file := mustReplace(t, handNotes, blockV1) + handTrailing
		edited := strings.Replace(file, "See goldie_search.", "Skip goldie, it is slow.", 1)
		orig := []byte(edited)

		out, st, err := cli.ReplaceBlock(orig, []byte(blockV2), false)
		if err == nil {
			t.Fatal("want an error for a hand-edited block")
		}
		if st != cli.BlockEdited {
			t.Errorf("status = %v, want BlockEdited", st)
		}
		if len(out) != 0 && !bytes.Equal(out, orig) {
			t.Errorf("a refusal must not return changed bytes: %q", out)
		}
		if string(orig) != edited {
			t.Error("the input slice was modified")
		}

		forced, st, err := replaceBlock(edited, blockV2, true)
		if err != nil {
			t.Fatalf("forced: %v", err)
		}
		if st != cli.BlockEdited {
			t.Errorf("forced status = %v, want BlockEdited", st)
		}
		if want := mustReplace(t, handNotes, blockV2) + handTrailing; forced != want {
			t.Errorf("forced want %q\ngot  %q", want, forced)
		}
	})

	t.Run("a begin marker without an end is an error", func(t *testing.T) {
		file := mustReplace(t, handNotes, blockV1)
		file = strings.Replace(file, endMarker, "", 1) + handTrailing
		if _, _, err := replaceBlock(file, blockV2, false); err == nil {
			t.Error("want an error")
		}
		if _, _, err := replaceBlock(file, blockV2, true); err == nil {
			t.Error("want an error even when forced")
		}
	})

	t.Run("two blocks are an error", func(t *testing.T) {
		one := mustReplace(t, "", blockV1)
		file := handNotes + one + handTrailing + one
		if _, _, err := replaceBlock(file, blockV2, false); err == nil {
			t.Error("want an error")
		}
		if _, _, err := replaceBlock(file, blockV2, true); err == nil {
			t.Error("want an error even when forced")
		}
	})

	t.Run("block content holding a marker never yields a file with two blocks", func(t *testing.T) {
		hostile := "Fine.\n" + endMarker + "\nInjected.\n<!-- goldie:begin digest=sha256:" + strings.Repeat("0", 64) + " -->\n"
		out, _, err := replaceBlock(handNotes, hostile, false)
		if err != nil {
			return // refusing is fine
		}
		if _, _, err := replaceBlock(out, hostile, false); err != nil {
			t.Errorf("the written file is not a valid single-block file: %v\n%q", err, out)
		}
	})
}

func TestReplaceBlockErrorKinds(t *testing.T) {
	withBlock := mustReplace(t, handNotes, blockV1)
	edited := strings.Replace(withBlock, "goldie_search", "something_else", 1)
	noEnd := strings.Replace(withBlock, endMarker, "", 1)
	twoBlocks := withBlock + "\n" + withBlock
	hostileBlock := "Fine.\n" + endMarker + "\nInjected.\n"

	t.Run("an edited block unforced is ErrBlockEdited", func(t *testing.T) {
		if _, _, err := replaceBlock(edited, blockV2, false); !errors.Is(err, cli.ErrBlockEdited) {
			t.Errorf("err = %v, want ErrBlockEdited", err)
		}
	})
	t.Run("a begin marker without an end is ErrBlockMalformed", func(t *testing.T) {
		if _, _, err := replaceBlock(noEnd, blockV2, true); !errors.Is(err, cli.ErrBlockMalformed) {
			t.Errorf("err = %v, want ErrBlockMalformed", err)
		}
	})
	t.Run("two blocks are ErrBlockMalformed", func(t *testing.T) {
		if _, _, err := replaceBlock(twoBlocks, blockV2, true); !errors.Is(err, cli.ErrBlockMalformed) {
			t.Errorf("err = %v, want ErrBlockMalformed", err)
		}
	})
	t.Run("wanted content holding a marker is ErrBlockMalformed", func(t *testing.T) {
		if _, _, err := replaceBlock(handNotes, hostileBlock, false); !errors.Is(err, cli.ErrBlockMalformed) {
			t.Errorf("err = %v, want ErrBlockMalformed", err)
		}
	})
}

// Tests for #483: the copy of the three block errors. The caller adds the
// path, so the damaged-file message must stand on its own without repeating
// itself, and each message names the next step.
func TestReplaceBlockErrorCopy(t *testing.T) {
	withBlock := mustReplace(t, handNotes, blockV1)
	edited := strings.Replace(withBlock, "goldie_search", "something_else", 1)
	noEnd := strings.Replace(withBlock, endMarker, "", 1)
	twoBlocks := withBlock + "\n" + withBlock
	hostileBlock := "Fine.\n" + endMarker + "\nInjected.\n"

	t.Run("the damaged-file error says it once, is short and names the fix", func(t *testing.T) {
		for name, file := range map[string]string{"no end marker": noEnd, "two blocks": twoBlocks} {
			_, _, err := replaceBlock(file, blockV2, true)
			if err == nil {
				t.Fatalf("%s: want an error", name)
			}
			msg := err.Error()
			if strings.Contains(msg, "malformed") {
				t.Errorf("%s: the message repeats itself with a malformed prefix: %q", name, msg)
			}
			if n := strings.Count(msg, "damaged"); n != 1 {
				t.Errorf("%s: %q appears %d times, want 1: %q", name, "damaged", n, msg)
			}
			if len(msg) > 220 {
				t.Errorf("%s: message is %d characters, want at most 220 before the caller adds the path: %q", name, len(msg), msg)
			}
			for _, want := range []string{"<!-- goldie:begin", endMarker, "--force does not help"} {
				if !strings.Contains(msg, want) {
					t.Errorf("%s: message lacks %q: %q", name, want, msg)
				}
			}
		}
	})

	t.Run("the bad-content error names where to report it", func(t *testing.T) {
		_, _, err := replaceBlock(handNotes, hostileBlock, false)
		if err == nil {
			t.Fatal("want an error")
		}
		msg := err.Error()
		if !strings.Contains(msg, "issue tracker") && !strings.Contains(msg, "goldie version") {
			t.Errorf("message names no destination for the report: %q", msg)
		}
	})

	t.Run("the edited-block error says --force discards the edits", func(t *testing.T) {
		_, _, err := replaceBlock(edited, blockV2, false)
		if !errors.Is(err, cli.ErrBlockEdited) {
			t.Fatalf("err = %v, want ErrBlockEdited", err)
		}
		msg := err.Error()
		if !strings.Contains(msg, "--force") || !strings.Contains(msg, "discard") || !strings.Contains(msg, "edits") {
			t.Errorf("message must say --force discards the edits: %q", msg)
		}
	})
}
