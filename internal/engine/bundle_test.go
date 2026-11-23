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

func TestClaudeLayoutLeavesOutLoopsThatCollideOrSplit(t *testing.T) {
	skills := []skillBundle{{name: "Loop-Review", files: map[string][]byte{"SKILL.md": []byte("x")}}}
	loops := []Loop{
		{Name: "review", Interval: "30m", Content: "p", Stop: "s"},
		{Name: "a b", Interval: "30m", Content: "p", Stop: "s"},
		{Name: "ok", Interval: "30m", Content: "p", Stop: "s"},
	}
	var got []string
	for _, f := range claudeLayout(skills, nil, loops) {
		got = append(got, f.Path)
	}
	want := map[string]bool{".claude/skills/goldie-Loop-Review/SKILL.md": true, ".claude/skills/goldie-loop-ok/SKILL.md": true}
	if len(got) != len(want) {
		t.Fatalf("layout paths = %q, want %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected path %q in %q", p, got)
		}
	}
}
