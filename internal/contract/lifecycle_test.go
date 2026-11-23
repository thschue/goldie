package contract_test

import (
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// A replaced_by that cleans to nothing, only hidden runes or spaces, gives
// the notice without a replacement rather than "Use  instead." (#273).
func TestDeprecationNoticeWithAnEmptyReplacement(t *testing.T) {
	status := "deprecated"
	for _, ref := range []string{"", " ", "​‮", "\x1b\a\n"} {
		got := contract.DeprecationNotice(contract.Article{Status: &status, ReplacedBy: &ref})
		if want := "Warning: this article is deprecated."; got != want {
			t.Errorf("replaced_by %q: notice %q, want %q", ref, got, want)
		}
	}
	if got := contract.DeprecationNotice(contract.Article{}); got != "" {
		t.Errorf("no status: notice %q, want none", got)
	}
}
