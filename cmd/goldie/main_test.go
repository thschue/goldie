package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/platform"
)

func TestRunVersionPrintsPlatformVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"version"}, &out); err != nil {
		t.Fatalf("run version: %v", err)
	}
	first, _, _ := strings.Cut(out.String(), "\n")
	if got := strings.TrimSpace(first); got != platform.Version {
		t.Fatalf("version first line = %q, want %q (output %q)", got, platform.Version, out.String())
	}
}

func TestRunUnknownCommandFails(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"nope"}, &out); err == nil {
		t.Fatal("expected an error for an unknown command")
	}
}

func TestHandlerErrorLabelsOnlyConfigurationAsInvalidConfiguration(t *testing.T) {
	_, cfgErr := app.PublicHandler(app.Options{})
	if cfgErr == nil {
		t.Fatal("PublicHandler without a token must fail")
	}
	if got := handlerError(cfgErr).Error(); !strings.HasPrefix(got, "invalid configuration") {
		t.Errorf("a settings error reads %q, want it labelled invalid configuration", got)
	}
	internal := errors.New("embedded spec does not parse")
	got := handlerError(internal)
	if strings.Contains(got.Error(), "invalid configuration") || !errors.Is(got, internal) {
		t.Errorf("an internal error reads %q, want it wrapped without the configuration label", got)
	}
}
