package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance test for #494: the new overrides and enforced fields on the API
// do not change what the CLI prints for search and lookup.
func TestCLIOutputIsUnchangedByOverridesAndEnforced(t *testing.T) {
	h, err := app.PublicHandler(platform.Config{ContentDir: "../../internal/app/testdata/items-content", APIToken: cliToken, RateLimit: platform.DefaultRateLimit()}, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	for _, args := range [][]string{
		{"search", "deploy", "--project", "github.com/acme/shop"},
		{"lookup", "platform/deploy", "--project", "github.com/acme/shop"},
		{"search", "policy", "--project", "github.com/acme/shop"},
	} {
		out, err := runScoped(t, t.TempDir(), "", append(args, "--addr", srv.URL, "--token", cliToken)...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		low := strings.ToLower(out)
		if strings.Contains(low, "overrides") || strings.Contains(low, "enforced") {
			t.Errorf("%v prints the new fields:\n%s", args, out)
		}
	}
}
