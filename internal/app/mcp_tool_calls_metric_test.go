package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/contract"
	gmcp "github.com/thschue/goldie/internal/mcp"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #599 at the MCP protocol seam: every tools/call that
// /mcp serves is counted in goldie.mcp.tool.calls by tool (one of the ten
// registered names, else "other") and result (ok, or error for IsError), and
// nothing else. Tool arguments never become attributes (PC-12). The export is
// read from an in-process OTLP collector, as for #412.

var registeredTools = []string{
	"search_knowledge", "get_article", "list_projects", "current_context",
	"find_skills", "get_skill", "list_skills", "list_agents",
	"list_loops", "get_loop", "find_decisions",
}

// toolCallTelemetry is metrics-only telemetry exporting to col.
func toolCallTelemetry(t *testing.T, col *otlpCollector) *platform.Telemetry {
	t.Helper()
	env := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": col.srv.URL,
		"OTEL_TRACES_EXPORTER":        "none",
		"OTEL_LOGS_EXPORTER":          "none",
		"OTEL_METRICS_EXPORTER":       "otlp",
	}
	tel, err := platform.SetupTelemetry(context.Background(), func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	return tel
}

// toolCallExport shuts tel down and returns the metrics export.
func toolCallExport(t *testing.T, tel *platform.Telemetry, col *otlpCollector) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tel.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	return col.body("/v1/metrics")
}

func toolCallPoints(t *testing.T, raw []byte) (counts map[string]int64, keys []string) {
	t.Helper()
	return metricCounts(t, raw, "goldie.mcp.tool.calls", func(a map[string]string) string { return a["tool"] + "|" + a["result"] })
}

func resultWord(isErr bool) string {
	if isErr {
		return "error"
	}
	return "ok"
}

func TestMCPToolCallsAreCountedByToolAndResultOverMCP(t *testing.T) {
	col := newOTLPCollector(t)
	tel := toolCallTelemetry(t, col)
	pub, err := PublicHandlerWith(platform.Config{ContentDir: testContent, APIToken: testToken, RateLimit: roomyRateLimit}, nil, tel)
	if err != nil {
		t.Fatalf("PublicHandlerWith: %v", err)
	}
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))

	// The drift test: the tools the server registers are exactly the counter's
	// eleven names, and each is counted under its own name, never "other".
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, tl := range res.Tools {
		listed = append(listed, tl.Name)
	}
	slices.Sort(listed)
	wantNames := slices.Clone(registeredTools)
	slices.Sort(wantNames)
	if !reflect.DeepEqual(listed, wantNames) {
		t.Fatalf("registered tools = %v, want %v: update the counter's name set with them", listed, wantNames)
	}

	const (
		queryMarker = "planted-query-marker-5990"
		idMarker    = "planted-article-marker-5991"
		projMarker  = "github.com/acme/planted-project-marker-5992"
	)
	args := map[string]map[string]any{
		"search_knowledge": {"query": queryMarker},
		"get_article":      {"id": idMarker},
		"list_projects":    {},
		"current_context":  {},
		"find_skills":      {"query": queryMarker},
		"get_skill":        {"name": idMarker, "project": projMarker},
		"list_skills":      {},
		"list_agents":      {},
		"list_loops":       {},
		"get_loop":         {"name": idMarker, "project": projMarker},
		"find_decisions":   {"query": queryMarker},
	}
	want := map[string]int64{}
	for _, tool := range registeredTools {
		_, isErr := call(t, cs, tool, args[tool])
		want[tool+"|"+resultWord(isErr)]++
	}
	// A search that surely answers gives the ok result at least one point.
	if _, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "agents loops"}); isErr {
		t.Fatal("search_knowledge over the test content must answer")
	}
	want["search_knowledge|ok"]++
	// An unknown name, sent by the caller, is counted as "other".
	const unknown = "planted-tool-marker-5993"
	if res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: unknown}); err == nil && !res.IsError {
		t.Fatalf("unknown tool answered ok: %v", res)
	}

	raw := toolCallExport(t, tel, col)
	counts, keys := toolCallPoints(t, raw)
	var other int64
	for k, v := range counts {
		if strings.HasPrefix(k, "other|") {
			other += v
			delete(counts, k)
		}
	}
	if other != 1 {
		t.Errorf("calls counted as tool=other = %d, want 1 (the unknown name)", other)
	}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("goldie.mcp.tool.calls by tool|result = %v, want %v", counts, want)
	}
	if !reflect.DeepEqual(keys, []string{"result", "tool"}) {
		t.Errorf("attribute keys = %v, want result and tool only", keys)
	}
	for _, planted := range []string{queryMarker, idMarker, projMarker, unknown, testToken} {
		if bytes.Contains(raw, []byte(planted)) {
			t.Errorf("the metrics export carries %q (PC-12)", planted)
		}
	}
}

// A rate-limited tool call is a tool error, and is counted as error.
func TestMCPRateLimitedToolCallIsCountedAsError(t *testing.T) {
	col := newOTLPCollector(t)
	tel := toolCallTelemetry(t, col)
	cl, err := contract.NewClientWithResponses(rateLimitedAPI(t, "7"))
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcp.NewInMemoryTransports()
	counter := tel.ToolCallCounter("goldie.mcp.tool.calls", "MCP tool calls, by tool and result.")
	ss, err := gmcp.New(cl, gmcp.CountToolCalls(counter)).Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	if _, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "agents loops"}); !isErr {
		t.Fatal("a rate-limited call must be a tool error")
	}
	counts, _ := toolCallPoints(t, toolCallExport(t, tel, col))
	if want := map[string]int64{"search_knowledge|error": 1}; !reflect.DeepEqual(counts, want) {
		t.Errorf("goldie.mcp.tool.calls = %v, want %v", counts, want)
	}
}

// Without the option (the stdio transport passes none) nothing is counted and
// the tools still answer.
func TestMCPToolCallsWithoutTheOptionCountNothing(t *testing.T) {
	col := newOTLPCollector(t)
	tel := toolCallTelemetry(t, col)
	cs := session(t, newServer(t).URL, testToken)
	if _, isErr := call(t, cs, "list_projects", nil); isErr {
		t.Fatal("list_projects must answer")
	}
	if counts, _ := toolCallPoints(t, toolCallExport(t, tel, col)); len(counts) != 0 {
		t.Errorf("goldie.mcp.tool.calls = %v, want none", counts)
	}
}
