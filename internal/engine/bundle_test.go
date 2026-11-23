package engine

import "testing"

func TestBundlePathElementsAreSafe(t *testing.T) {
	for s, want := range map[string]bool{
		"deploy":   true,
		"a b":      true,
		"":         false,
		".":        false,
		"..":       false,
		"a/b":      false,
		`a\b`:      false,
		"a\u202eb": false,
		"a\x00b":   false,
		"a\nb":     false,
	} {
		if got := safeElem(s); got != want {
			t.Errorf("safeElem(%q) = %v, want %v", s, got, want)
		}
	}
	for p, want := range map[string]bool{
		"SKILL.md":     true,
		"scripts/x.sh": true,
		"../x":         false,
		"a//b":         false,
		"/abs":         false,
		"scripts/./x":  false,
		"trailing/":    false,
	} {
		if got := safeRel(p); got != want {
			t.Errorf("safeRel(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestValidBundleTarget(t *testing.T) {
	if !ValidBundleTarget("claude") || ValidBundleTarget("agents") || ValidBundleTarget("") {
		t.Error("only claude is a bundle target")
	}
}
