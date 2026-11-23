package contract

import (
	"errors"
	"fmt"
	"testing"
)

func TestRewordMiss(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"plain miss is reworded", &APIError{Code: "not_found", Hint: ArticleMissHint}, "reworded"},
		{"wrapped plain miss is reworded", fmt.Errorf("wrap: %w", &APIError{Code: "not_found", Hint: ArticleMissHint}), "reworded"},
		{"other not_found hint is kept", &APIError{Code: "not_found", Hint: "remove the entry"}, "remove the entry"},
		{"other code is kept", &APIError{Code: "forbidden", Hint: ArticleMissHint}, ArticleMissHint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ae *APIError
			if got := RewordMiss(tc.err, ArticleMissHint, "reworded"); got != tc.err || !errors.As(got, &ae) {
				t.Fatalf("RewordMiss changed the error value: %v", got)
			}
			if ae.Hint != tc.want {
				t.Errorf("hint = %q, want %q", ae.Hint, tc.want)
			}
		})
	}
	if got := RewordMiss(nil, "a", "b"); got != nil {
		t.Errorf("nil error became %v", got)
	}
}
