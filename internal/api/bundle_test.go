package api

import "testing"

func TestEtagMatches(t *testing.T) {
	const tag = `"abc"`
	for header, want := range map[string]bool{
		`"abc"`:          true,
		`W/"abc"`:        true,
		`"x", "abc"`:     true,
		`*`:              true,
		`"abd"`:          false,
		`abc`:            false,
		``:               false,
		`"x" "abc"`:      false,
		`W/"x", W/"abd"`: false,
	} {
		if got := etagMatches(header, tag); got != want {
			t.Errorf("etagMatches(%q) = %v, want %v", header, got, want)
		}
	}
}
