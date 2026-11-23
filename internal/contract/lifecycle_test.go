package contract_test

import (
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// A replaced_by that cleans to nothing, only hidden runes or spaces, is a
// deprecation without a replacement, so no client says "Use  instead." (#273).
func TestDeprecationWithAnEmptyReplacement(t *testing.T) {
	status := contract.ItemStatusDeprecated
	for _, ref := range []string{"", " ", "\u200b\u202e", "\x1b\a\n"} {
		got, ok := contract.Deprecation(contract.Article{Status: &status, ReplacedBy: &ref})
		if !ok || got != "" {
			t.Errorf("replaced_by %q: Deprecation = %q, %v; want \"\", true", ref, got, ok)
		}
	}
	if _, ok := contract.Deprecation(contract.Article{}); ok {
		t.Error("no status: Deprecation reports a deprecated article")
	}
}
