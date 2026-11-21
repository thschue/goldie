package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

func TestRunVersionPrintsPlatformVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"version"}, &out); err != nil {
		t.Fatalf("run version: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != platform.Version {
		t.Fatalf("version output = %q, want %q", got, platform.Version)
	}
}

func TestRunUnknownCommandFails(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"nope"}, &out); err == nil {
		t.Fatal("expected an error for an unknown command")
	}
}
