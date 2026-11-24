package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Acceptance tests for #735 at the MCP protocol seam, against a fake API: what
// find_decisions and the resource goldie://decisions/{name} do with hostile
// answers and hostile arguments. The answers of a real server are tested in
// internal/app (mcp_decisions_test.go).

// hostileDecisionAPI answers the search and the decision read with hostile
// text (see hostile in skills_test.go), as an accepted decision, and counts
// the requests.
func hostileDecisionAPI(t *testing.T, requests *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{"query":"x","results":[{"id":"` + jsonText(hostile) + `","name":"` + jsonText(hostile) +
				`","kind":"decision","scope":"` + jsonText(hostile) + `","keywords":["` + jsonText(hostile) +
				`"],"matched":["` + jsonText(hostile) + `"]}],"hint":"` + jsonText(hostile) + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"d","title":"` + jsonText(hostile) + `","status":"accepted","date":"2023-01-01",` +
			`"scope":"` + jsonText(hostile) + `","content":"` + jsonText("# body "+hostile) + `","chain":[{"name":"d","title":"` +
			jsonText(hostile) + `","status":"accepted","scope":"global"}],"hint":"` + jsonText(hostile) + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hiddenOrForged(text string) bool {
	if strings.ContainsAny(text, "\u202e\u200b\x1b") || strings.ContainsRune(text, '\U000E0049') {
		return true
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- name: forged") {
			return true
		}
	}
	return false
}

// Abuse: every text of find_decisions passes CleanText.
func TestFindDecisionsCleansWhatASourceSays(t *testing.T) {
	var requests atomic.Int64
	cs := toolSession(t, hostileDecisionAPI(t, &requests).URL)
	text, isErr := callTool(t, cs, "find_decisions", map[string]any{"query": "x"})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	if hiddenOrForged(text) {
		t.Errorf("the answer keeps a hidden or control character, or a forged line: %q", text)
	}
	if requests.Load() == 0 {
		t.Error("the tool made no request")
	}
}

// Abuse: every text of the decision resource passes CleanText.
func TestDecisionResourceCleansWhatASourceSays(t *testing.T) {
	var requests atomic.Int64
	cs := toolSession(t, hostileDecisionAPI(t, &requests).URL)
	res, err := cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "goldie://decisions/d"})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, c := range res.Contents {
		b.WriteString(c.Text)
	}
	if b.Len() == 0 {
		t.Fatal("the resource is empty")
	}
	if hiddenOrForged(b.String()) {
		t.Errorf("the resource keeps a hidden or control character, or a forged line: %q", b.String())
	}
}

// Abuse: a look-alike project is a tool error pointing to list_projects, and
// the API sees no request.
func TestFindDecisionsRefusesLookAlikeProjectsBeforeAnyRequest(t *testing.T) {
	var requests atomic.Int64
	cs := toolSession(t, hostileDecisionAPI(t, &requests).URL)
	for name, p := range lookAlikeProjects {
		text, isErr := callTool(t, cs, "find_decisions", map[string]any{"query": "x", "project": p})
		if !isErr || !strings.Contains(text, "\nHint: ") || !strings.Contains(text, "list_projects") {
			t.Errorf("%s: want a tool error with a hint to list_projects, got %v %q", name, isErr, text)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the API got %d requests for look-alike projects, want none", n)
	}
}
