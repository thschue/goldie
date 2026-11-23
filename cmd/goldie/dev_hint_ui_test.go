package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// A ui-mode server holds no content by design, so its errors never point at
// goldie server --dev.
func TestWithDevHintSkipsUIMode(t *testing.T) {
	base := errors.New("invalid configuration")
	if got := withDevHint(platform.Config{Mode: platform.ModeUI}, base); strings.Contains(got.Error(), "--dev") {
		t.Errorf("ui mode error = %q, want no --dev hint", got)
	}
	if got := withDevHint(platform.Config{Mode: platform.ModeAll}, base); !strings.Contains(got.Error(), "--dev") {
		t.Errorf("first run error = %q, want the --dev hint", got)
	}
}
