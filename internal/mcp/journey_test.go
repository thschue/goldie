package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/journeytest"
)

// Journey J4: an agent reads Items over MCP, on the built binary. The test
// starts `goldie server --dev` on examples/knowledge and speaks MCP to its
// /mcp endpoint over streamable HTTP, as a remote agent does. A red step names
// what the agent could not do.

type journeyBearer struct{ token string }

func (b journeyBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func journeyConnect(ctx context.Context, addr, token string) (*sdk.ClientSession, error) {
	return sdk.NewClient(&sdk.Implementation{Name: "journey", Version: "v0"}, nil).Connect(ctx,
		&sdk.StreamableClientTransport{
			Endpoint:   addr + "/mcp",
			HTTPClient: &http.Client{Transport: journeyBearer{token}},
			MaxRetries: -1,
		}, nil)
}

func journeyCall(ctx context.Context, t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", tool, b.String())
	}
	return b.String()
}

func TestJ4AgentOverMCP(t *testing.T) {
	// The dev token is an operator token, not the reader token the issue names:
	// the criterion is adjusted until a reader-scoped token can be minted.
	_, addr, token := journeytest.Server(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	var cs *sdk.ClientSession
	t.Run("connect", func(t *testing.T) {
		var err error
		cs, err = journeyConnect(ctx, addr, token)
		if err != nil {
			t.Fatalf("an agent with a token could not connect to /mcp: %v", err)
		}
	})
	if cs == nil {
		return
	}
	defer func() { _ = cs.Close() }()

	t.Run("list-tools", func(t *testing.T) {
		res, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		got := map[string]bool{}
		for _, tl := range res.Tools {
			got[tl.Name] = true
		}
		if !got["search_knowledge"] || !got["get_article"] {
			t.Errorf("tools = %v, want search_knowledge and get_article", got)
		}
	})

	t.Run("search", func(t *testing.T) {
		text := journeyCall(ctx, t, cs, "search_knowledge", map[string]any{"query": "agent loops"})
		if !strings.Contains(text, "agent-loops") || !strings.Contains(text, "Autonomous agent loops") {
			t.Errorf("search for a term from the examples found no Item:\n%s", text)
		}
	})

	t.Run("get", func(t *testing.T) {
		text := journeyCall(ctx, t, cs, "get_article", map[string]any{"id": "agent-loops"})
		if !strings.Contains(text, "Autonomous agent loops") ||
			!strings.Contains(text, "Three cooperating loops turn a well-groomed backlog into reviewed, merged pull requests:") ||
			!strings.Contains(text, "- the **product loop** shapes the work;") {
			t.Errorf("the fetched Item does not carry its Markdown:\n%s", text)
		}
	})

	t.Run("no-token", func(t *testing.T) {
		if bad, err := journeyConnect(ctx, addr, ""); err == nil {
			_ = bad.Close()
			t.Fatal("an agent without a token connected, want it refused")
		}
		const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
			`"capabilities":{},"clientInfo":{"name":"journey","version":"v0"}}}`
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr+"/mcp", strings.NewReader(initialize))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test URL
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status %d, want 401: %s", resp.StatusCode, body)
		}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Details struct {
					Hint string `json:"hint"`
				} `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("body is not the error envelope: %s", body)
		}
		if env.Error.Code != "unauthorized" || env.Error.Message == "" || env.Error.Details.Hint == "" {
			t.Errorf("envelope = %s, want code unauthorized, a message and a hint (ADR 0014)", body)
		}
		if strings.Contains(string(body), token) {
			t.Errorf("the refusal echoes the token: %s", body)
		}
	})
}
