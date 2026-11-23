package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpWithoutArgumentEqualsDashDashHelp(t *testing.T) {
	var viaCommand, viaFlag bytes.Buffer
	if err := run([]string{"help"}, &viaCommand); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--help"}, &viaFlag); err != nil {
		t.Fatal(err)
	}
	if viaCommand.String() != viaFlag.String() {
		t.Errorf("goldie help differs from goldie --help:\n%s\n---\n%s", &viaCommand, &viaFlag)
	}
}

// The did-you-mean hint repeats the word that failed, which may hold control
// characters: it is cleaned like the rest of the error.
func TestSingleDashLongFlagHintIsOneCleanLine(t *testing.T) {
	err := run([]string{"server", "-\x1b[31mred"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "did you mean --") || strings.ContainsAny(err.Error(), "\x1b\n") {
		t.Errorf("error %q, want a did-you-mean hint on one line with no control characters", err)
	}
}
