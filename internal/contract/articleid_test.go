package contract

import "testing"

func TestCheckArticleIDRejectsIdsThatResolveAwayFromTheArticlePath(t *testing.T) {
	for _, id := range []string{"", ".", "..", "...", "...."} {
		if err := CheckArticleID(id); err == nil {
			t.Errorf("CheckArticleID(%q) = nil, want an error", id)
		}
	}
}

func TestCheckArticleIDAcceptsOrdinaryIds(t *testing.T) {
	for _, id := range []string{
		"platform/deploy", "a/..", "../a", ".hidden", "v1.2", "%2e%2e", "a", "a.b/c",
		// Whitespace is not trimmed: these are normal (unknown) ids.
		" .. ", " ", ". ", "\t",
	} {
		if err := CheckArticleID(id); err != nil {
			t.Errorf("CheckArticleID(%q) = %v, want nil", id, err)
		}
	}
}
