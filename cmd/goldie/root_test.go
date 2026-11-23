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

// -help is -h plus the letters "elp", which is no flag: no hint then.
func TestSingleDashHintNamesOnlyARealFlag(t *testing.T) {
	err := run([]string{"server", "-help"}, &bytes.Buffer{})
	if err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("error %v, want a usage error without a did-you-mean hint", err)
	}
}

// A single-dash long flag gets the double-dash hint, on one clean line even
// when the word that failed holds control characters.
func TestSingleDashLongFlagHintIsOneCleanLine(t *testing.T) {
	err := run([]string{"server", "-dev"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "did you mean --dev?") {
		t.Errorf("error %v, want the hint did you mean --dev?", err)
	}
	err = run([]string{"server", "-\x1b[31mred"}, &bytes.Buffer{})
	if err == nil || strings.ContainsAny(err.Error(), "\x1b\n") {
		t.Errorf("error %q, want one line with no control characters", err)
	}
}
