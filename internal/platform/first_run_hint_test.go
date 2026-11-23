package platform

import (
	"strings"
	"testing"
)

// #512: the generated configuration reference quotes the first-run hint as it
// is, with no trailing " .".
func TestConfigurationReferenceQuotesTheFirstRunHint(t *testing.T) {
	const want = "To try goldie on this machine: goldie server --dev"
	if FirstRunHint != want {
		t.Errorf("FirstRunHint = %q, want %q", FirstRunHint, want)
	}
	ref := committedReference(t)
	if !strings.Contains(ref, `"`+want+`"`) {
		t.Errorf("committed reference does not quote %q (run: ./run regen)", want)
	}
	if strings.Contains(ref, want+" .") {
		t.Error("committed reference still quotes the hint with a trailing \" .\"")
	}
}
