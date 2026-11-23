package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/contract"
)

// lookAlikeProjects are values that Unicode lower-casing would turn into the
// valid ProjectId github.com/acme/kube or github.com/acme/shop: each carries a
// non-ASCII letter whose lower case is ASCII, or is otherwise non-ASCII.
var lookAlikeProjects = map[string]string{
	"Kelvin sign":          "github.com/acme/\u212Aube",
	"Kelvin sign in host":  "github.com/acme/kube\u212A",
	"dotted capital I":     "g\u0130thub.com/acme/shop",
	"fullwidth letters":    "github.com/acme/\uFF53hop",
	"Latin-1 letter":       "github.com/acme/sh\u00f6p",
	"invalid UTF-8 byte":   "github.com/acme/sh\xffop",
	"non-breaking space":   "github.com/acme/shop\u00a0",
	"Cyrillic look-alikes": "github.com/\u0430cme/shop",
}

func TestProjectParamRefusesEveryNonASCIIValue(t *testing.T) {
	for name, p := range lookAlikeProjects {
		if got, ok := projectParam(p); ok {
			t.Errorf("%s: projectParam(%q) = %v, true; want it refused", name, p, *got)
		}
	}
}

func TestProjectParamLowerCasesASCII(t *testing.T) {
	got, ok := projectParam("GitHub.com/ACME/Kube")
	if !ok || got == nil || *got != "github.com/acme/kube" {
		t.Errorf("projectParam = %v, %v; want github.com/acme/kube", got, ok)
	}
	if got, ok := projectParam(""); !ok || got != nil {
		t.Errorf("projectParam(\"\") = %v, %v; want no project", got, ok)
	}
}

// At the tool seam, a look-alike project is a tool error pointing to
// list_projects, and the API sees no request.
func TestLookAlikeProjectIsRefusedBeforeAnyRequest(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
	}))
	t.Cleanup(srv.Close)
	cl, err := contract.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := New(cl).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	for name, p := range lookAlikeProjects {
		for tool, args := range map[string]map[string]any{
			"search_knowledge": {"query": "x", "project": p},
			"get_article":      {"id": "a", "project": p},
		} {
			res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
			if err != nil {
				t.Fatalf("%s/%s: %v", name, tool, err)
			}
			text := res.Content[0].(*sdk.TextContent).Text
			if !res.IsError || !strings.Contains(text, "\nHint: ") || !strings.Contains(text, "list_projects") {
				t.Errorf("%s/%s: want a tool error with a hint to list_projects, got %v %q", name, tool, res.IsError, text)
			}
			if strings.Contains(text, "kube") || strings.Contains(text, "shop") {
				t.Errorf("%s/%s: the error echoes the value: %q", name, tool, text)
			}
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the API got %d requests for look-alike projects, want none", n)
	}
}
