package cli_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/thschue/goldie/internal/cli"
)

// Round-trip tests for the managed-block editor (#413, fix round 1): whatever
// ApplyBlock returns reads back as the current block, and applying it again
// changes nothing.

func TestApplyThenInspectIsCurrentForAnyAcceptedContent(t *testing.T) {
	files := map[string][]byte{
		"no file":   nil,
		"empty":     {},
		"notes":     []byte("# t\n\nnotes\n"),
		"crlf":      []byte("# t\r\nnotes\r\n"),
		"old block": blockFixture(t, "outdated", "in.md"),
	}
	for _, content := range []string{
		"x", "x\n", "x\n\n", "x\n\n\n", "\n", "\n\nx", "a\n\nb\n\n", "", "x\r\n\r\n",
		"```\ncode\n```\n\n",
	} {
		for fname, file := range files {
			out, err := cli.ApplyBlock(file, []byte(content))
			if err != nil {
				t.Errorf("%s, content %q: %v", fname, content, err)
				continue
			}
			if s, err := cli.InspectBlock(out, []byte(content)); err != nil || s != cli.BlockCurrent {
				t.Errorf("%s, content %q: read back as %q, %v; want current", fname, content, s, err)
			}
			if again, err := cli.ApplyBlock(out, []byte(content)); err != nil || !bytes.Equal(again, out) {
				t.Errorf("%s, content %q: second apply = %q, %v", fname, content, again, err)
			}
		}
	}
}

func TestContentWithABareCRCannotForgeAMarker(t *testing.T) {
	end := "<!-- goldie:end -->"
	begin := "<!-- goldie:begin v1 digest=" + digestNew + " -->"
	for name, content := range map[string]string{
		"end marker then CR":   "text\n" + end + "\r",
		"begin marker then CR": "text\n" + begin + "\r",
		"CR in a line":         "a\rb",
		"CR before CRLF":       "a\r\r\nb",
	} {
		out, err := cli.ApplyBlock([]byte("# keep\n"), []byte(content))
		if !errors.Is(err, cli.ErrBlockContent) || out != nil {
			t.Errorf("%s: ApplyBlock = %q, %v; want nil and ErrBlockContent", name, out, err)
		}
	}
}

func TestAFileEndingInsideAFenceGetsItsFenceClosedBeforeTheBlock(t *testing.T) {
	content := blockFixture(t, "content.md")
	for name, file := range map[string]string{
		"backticks":     "# t\n\n```sh\nrun\n",
		"tildes":        "~~~\nrun\n",
		"longer fence":  "````\n```\nrun\n",
		"no final EOL":  "```\nrun",
		"CRLF":          "```\r\nrun\r\n",
		"BOM and fence": "\xef\xbb\xbf```\nrun\n",
	} {
		out, err := cli.ApplyBlock([]byte(file), content)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !bytes.HasPrefix(out, []byte(file)) {
			t.Errorf("%s: the file is not kept as the prefix: %q", name, out)
		}
		if s, err := cli.InspectBlock(out, content); err != nil || s != cli.BlockCurrent {
			t.Errorf("%s: read back as %q, %v; want current", name, s, err)
		}
		if again, err := cli.ApplyBlock(out, content); err != nil || !bytes.Equal(again, out) {
			t.Errorf("%s: every apply must not append another block: %q, %v", name, again, err)
		}
	}
}

// FuzzApplyBlock checks that for any file and content ApplyBlock returns an
// error, or bytes that read back as the current block and stay put on a second
// apply; a file without a block stays as the prefix.
func FuzzApplyBlock(f *testing.F) {
	content := []byte("Use goldie.\nRun `goldie ask`.\n")
	for _, s := range []string{"", "# t\n", "```\n", "<!-- goldie:end -->\r", "\xef\xbb\xbf# t\r\n",
		"<!-- goldie:begin v1 digest=" + digestNew + " -->\nx\n<!-- goldie:end -->\n"} {
		f.Add([]byte(s), content)
		f.Add([]byte("# keep\n"), []byte(s))
	}
	f.Fuzz(func(t *testing.T, file, content []byte) {
		before, _ := cli.InspectBlock(file, content)
		out, err := cli.ApplyBlock(file, content)
		if err != nil {
			if out != nil {
				t.Errorf("an error came with output %q", out)
			}
			return
		}
		if s, err := cli.InspectBlock(out, content); err != nil || s != cli.BlockCurrent {
			t.Fatalf("ApplyBlock(%q, %q) = %q reads back as %q, %v", file, content, out, s, err)
		}
		if again, err := cli.ApplyBlock(out, content); err != nil || !bytes.Equal(again, out) {
			t.Errorf("second ApplyBlock changed %q to %q, %v", out, again, err)
		}
		if before == cli.BlockMissing && !bytes.HasPrefix(out, file) {
			t.Errorf("a file without a block lost its bytes: %q -> %q", file, out)
		}
	})
}
