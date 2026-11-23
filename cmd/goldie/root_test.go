package main

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/cli"
)

func helpOf(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatalf("goldie %v: %v", args, err)
	}
	return out.String()
}

func TestHelpWithoutArgumentEqualsDashDashHelp(t *testing.T) {
	got, want := helpOf(t, "help"), helpOf(t, "--help")
	if got != want {
		t.Errorf("goldie help differs from goldie --help:\n%s\n---\n%s", got, want)
	}
	if !strings.Contains(got, "-h, --help") {
		t.Errorf("root help has no -h, --help flag:\n%s", got)
	}
}

func TestClientHelpCoversEveryClientCommand(t *testing.T) {
	var keys []string
	for k := range clientHelp {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := slices.Sorted(slices.Values(cli.Commands))
	if !slices.Equal(keys, want) {
		t.Errorf("clientHelp keys %v, cli.Commands %v", keys, want)
	}
}

func TestSingleDashLongFlagSuggestsTheDoubleDash(t *testing.T) {
	err := run([]string{"server", "-dev"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "did you mean --dev?") {
		t.Errorf("error %v, want a did-you-mean --dev hint", err)
	}
}

func TestMCPWithoutTransportIsMisuse(t *testing.T) {
	var ue *usageErr
	err := run([]string{"mcp"}, &bytes.Buffer{})
	if !errors.As(err, &ue) || ue.path != "goldie mcp" || strings.Contains(ue.msg, "\n") {
		t.Errorf("error %v, want a one-line usage error for goldie mcp", err)
	}
}
