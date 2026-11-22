package api_test

import (
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/api"
)

// CheckToken rejects a token one byte under MinTokenLength and accepts one of
// exactly that length. The error states the minimum but never the token, any
// part of it, or its length.
func TestCheckTokenMinimumLength(t *testing.T) {
	const long = "0123456789abcdefghijklmnopqrstuvwxyz"
	short, atMin := long[:api.MinTokenLength-1], long[:api.MinTokenLength]
	err := api.CheckToken(short)
	if err == nil {
		t.Fatalf("a %d-byte token was accepted", len(short))
	}
	msg := err.Error()
	if !strings.Contains(msg, "at least 32 bytes") {
		t.Errorf("error %q does not state the minimum", msg)
	}
	for _, leak := range []string{short, short[:8], short[len(short)-8:], "31"} {
		if strings.Contains(msg, leak) {
			t.Errorf("error %q contains %q", msg, leak)
		}
	}
	if err := api.CheckToken(atMin); err != nil {
		t.Errorf("a %d-byte token was rejected: %v", len(atMin), err)
	}
	if err := api.CheckToken(atMin[:31] + " "); err == nil {
		t.Error("a 32-byte token with a space was accepted: the character rules must still apply")
	}
}
