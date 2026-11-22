package app

import (
	"net/http"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	gmcp "github.com/thschue/goldie/internal/mcp"
)

// A panic in an API handler reached through a tool is the tool error of a 500,
// not the end of the process: the SDK runs tools on goroutines that recover
// nothing, so the in-process transport must. The panic value never shows.
func TestMCPToolSurvivesAPanickingAPIHandler(t *testing.T) {
	const secret = "panic-value-with-a-secret-0123456789"
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(secret) })
	ct, st := sdk.NewInMemoryTransports()
	ss, err := gmcp.New(inProcessClient(boom, "Bearer "+testToken)).Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"get_article", map[string]any{"id": "agent-loops"}},
		{"search_knowledge", map[string]any{"query": "agents"}},
	} {
		text, isErr := call(t, cs, tc.tool, tc.args)
		if !isErr || !strings.Contains(text, "500") || !strings.Contains(text, "the server could not answer") {
			t.Errorf("%s with a panicking API: error=%v, want the 500 tool error: %s", tc.tool, isErr, text)
		}
		if strings.Contains(text, secret) {
			t.Errorf("%s: the tool error carries the panic value: %s", tc.tool, text)
		}
	}
}
