package cli

import (
	"strings"
	"testing"
)

// The temporary file name stays within NAME_MAX (255) for any file name.
func TestTempNameStaysWithinNameMax(t *testing.T) {
	for _, name := range []string{"CLAUDE.md", strings.Repeat("x", 255), strings.Repeat("é", 127)} {
		if got := tempName(name); len(got) > 255 || strings.Contains(got, "/") {
			t.Errorf("tempName(%d bytes) = %d bytes", len(name), len(got))
		}
	}
}

// A fence of one kind is not closed by the other kind.
func TestCheckBlockContentFenceKinds(t *testing.T) {
	if err := checkBlockContent([]byte("```\ncode\n~~~\n")); err == nil {
		t.Error("a backtick fence closed by a tilde line was accepted")
	}
	if err := checkBlockContent([]byte("```\n<div>\n[a]: x\n```\ntext\n")); err != nil {
		t.Errorf("content inside a closed fence refused: %v", err)
	}
}
